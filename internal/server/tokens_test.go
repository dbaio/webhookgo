package server

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dbaio/webhookgo/internal/accounts"
	"github.com/dbaio/webhookgo/internal/accounts/accountstest"
	"github.com/dbaio/webhookgo/internal/config"
	"github.com/dbaio/webhookgo/internal/store"
)

func tokenKey(id string) string { return "wh:{" + id + "}:token" }

// get reads a string key from miniredis ("" when missing).
func (e *env) get(key string) string {
	e.t.Helper()
	v, _ := e.mr.Get(key)
	return v
}

// as sends a request signed in with c (nil = anonymous); body is JSON text.
func (e *env) as(c *http.Cookie, method, path, body string) *http.Response {
	e.t.Helper()
	headers := []string{"Content-Type", "application/json"}
	if c != nil {
		headers = append(headers, cookieHeader(c)...)
	}
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	return e.do(method, path, r, headers...)
}

// invite creates a user through an invite link and returns the user and
// the session cookie.
func (e *env) invite(email, password string) (map[string]any, *http.Cookie) {
	e.t.Helper()
	token := e.link(accounts.NewLink{Kind: accounts.LinkInvite, Email: email})
	resp := e.post("POST", "/api/links/redeem", map[string]string{"token": token, "name": "Someone", "password": password})
	var out struct {
		User map[string]any `json:"user"`
	}
	e.json(resp, 200, &out)
	c := sessionCookie(resp)
	if c == nil {
		e.t.Fatal("invite must sign the user in")
	}
	return out.User, c
}

// ownedToken creates a URL signed in as c.
func (e *env) ownedToken(c *http.Cookie, body string) map[string]any {
	e.t.Helper()
	var tok map[string]any
	e.json(e.as(c, "POST", "/token", body), 201, &tok)
	return tok
}

// urlRow reads the owner of a URL from Postgres.
func (e *env) urlRow(id string) (owner string, found bool) {
	e.t.Helper()
	err := e.db.QueryRow(context.Background(), "select owner_id::text from urls where token_id = $1", id).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false
	}
	if err != nil {
		e.t.Fatal(err)
	}
	return owner, true
}

// streamEvents opens the live stream of a URL and returns its events; the
// channel is closed when the stream ends.
func (e *env) streamEvents(ctx context.Context, c *http.Cookie, id string) <-chan map[string]any {
	e.t.Helper()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/token/"+id+"/stream", nil)
	if c != nil {
		req.Header.Set("Cookie", c.Name+"="+c.Value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		e.t.Fatalf("stream: %d %s", resp.StatusCode, b)
	}
	events := make(chan map[string]any, 16)
	go func() {
		defer close(events)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				var ev map[string]any
				_ = json.Unmarshal([]byte(data), &ev)
				events <- ev
			}
		}
	}()
	return events
}

// nextEvent waits for an event; ok is false once the stream has ended.
func (e *env) nextEvent(ctx context.Context, events <-chan map[string]any) (ev map[string]any, ok bool) {
	e.t.Helper()
	select {
	case ev, ok = <-events:
		return ev, ok
	case <-ctx.Done():
		e.t.Fatal("timed out waiting for an event")
		return nil, false
	}
}

// warmStream captures requests until one shows up on the stream: the hub's
// pattern subscription is asynchronous.
func (e *env) warmStream(ctx context.Context, events <-chan map[string]any, id string) {
	e.t.Helper()
	if ev, _ := e.nextEvent(ctx, events); ev["type"] != "connected" {
		e.t.Fatalf("hello: %v", ev)
	}
	for {
		e.do("POST", "/"+id+"/warm", nil)
		select {
		case ev, ok := <-events:
			if !ok {
				e.t.Fatal("stream ended while warming up")
			}
			if ev["type"] == "request.created" {
				return
			}
		case <-time.After(200 * time.Millisecond):
		case <-ctx.Done():
			e.t.Fatal("no live event")
		}
	}
}

func TestOwnedURL(t *testing.T) {
	e := setupAccounts(t)
	u, c := e.firstAdmin("root@example.com", "correct horse battery")
	tok := e.ownedToken(c, `{"description":"Mine"}`)
	id := tok["uuid"].(string)
	if tok["owner_id"] != u["id"] || tok["expires_at"] != nil || tok["claim_key"] != nil || tok["claim_hash"] != nil {
		t.Fatalf("owned token: %v", tok)
	}
	if owner, ok := e.urlRow(id); !ok || owner != u["id"] {
		t.Fatalf("postgres row: %q %v", owner, ok)
	}
	if !strings.Contains(e.get(tokenKey(id)), `"owner_id":"`+u["id"].(string)+`"`) {
		t.Fatalf("redis copy: %s", e.get(tokenKey(id)))
	}

	// Capture stays public; reading does not.
	if resp := e.do("POST", "/"+id+"/hook", strings.NewReader("hi")); resp.StatusCode != 200 {
		t.Fatalf("capture: %d", resp.StatusCode)
	}
	var private map[string]any
	e.json(e.do("GET", "/token/"+id, nil), 403, &private)
	if private["error"] != "URL is private" || private["status"].(float64) != 403 {
		t.Fatalf("403 body: %v", private)
	}
	e.json(e.as(c, "GET", "/token/"+id, ""), 200, &tok)
	if tok["expires_at"] != nil || tok["request_count"].(float64) != 1 || tok["description"] != "Mine" {
		t.Fatalf("owner view: %v", tok)
	}
	var l listResp
	e.json(e.as(c, "GET", "/token/"+id+"/requests", ""), 200, &l)
	if l.Total != 1 || l.Data[0].Path != "/hook" {
		t.Fatalf("owner list: %+v", l)
	}
}

func TestPrivateURLAuthorization(t *testing.T) {
	e := setupAccounts(t)
	_, admin := e.firstAdmin("root@example.com", "correct horse battery")
	_, owner := e.invite("owner@example.com", "owner's password")
	_, other := e.invite("other@example.com", "other's password")
	id := e.ownedToken(owner, `{}`)["uuid"].(string)
	e.do("POST", "/"+id+"/one", strings.NewReader("1"))
	var l listResp
	e.json(e.as(owner, "GET", "/token/"+id+"/requests", ""), 200, &l)
	rid := l.Data[0].ID

	routes := []struct {
		method, path string
		destructive  bool
	}{
		{"GET", "/token/" + id, false},
		{"PUT", "/token/" + id, false},
		{"PATCH", "/token/" + id, false},
		{"PUT", "/token/" + id + "/cors/toggle", false},
		{"GET", "/token/" + id + "/requests", false},
		{"GET", "/token/" + id + "/request/latest", false},
		{"GET", "/token/" + id + "/request/" + rid, false},
		{"GET", "/token/" + id + "/request/" + rid + "/raw", false},
		{"GET", "/token/" + id + "/wait?timeout=0&since=0", false},
		{"GET", "/token/" + id + "/stream", false},
		{"POST", "/token/" + id + "/release", true},
		{"DELETE", "/token/" + id + "/request/" + rid, true},
		{"DELETE", "/token/" + id + "/request", true},
		{"DELETE", "/token/" + id, true},
	}
	for _, who := range []struct {
		name string
		c    *http.Cookie
	}{{"anonymous", nil}, {"another user", other}} {
		for _, rt := range routes {
			var body map[string]any
			e.json(e.as(who.c, rt.method, rt.path, "{}"), 403, &body)
			if body["error"] != "URL is private" {
				t.Fatalf("%s %s %s: %v", who.name, rt.method, rt.path, body)
			}
		}
	}
	for _, who := range []struct {
		name string
		c    *http.Cookie
	}{{"owner", owner}, {"admin", admin}} {
		for _, rt := range routes {
			if rt.destructive {
				continue
			}
			if resp := e.as(who.c, rt.method, rt.path, "{}"); resp.StatusCode >= 400 {
				b, _ := io.ReadAll(resp.Body)
				t.Fatalf("%s %s %s: %d %s", who.name, rt.method, rt.path, resp.StatusCode, b)
			}
		}
	}
	e.json(e.as(admin, "DELETE", "/token/"+id+"/request/"+rid, ""), 200, nil)
	e.json(e.as(owner, "DELETE", "/token/"+id+"/request", ""), 200, nil)
	if resp := e.as(admin, "DELETE", "/token/"+id, ""); resp.StatusCode != 204 {
		t.Fatalf("admin delete: %d", resp.StatusCode)
	}
}

func TestRehydration(t *testing.T) {
	e := setupAccounts(t, func(c *config.Config) { c.Retention = retention })
	_, c := e.firstAdmin("root@example.com", "correct horse battery")
	id := e.ownedToken(c, `{}`)["uuid"].(string)

	// The cache is lost: reads and captures fill it again from Postgres.
	e.mr.Del(tokenKey(id))
	e.json(e.as(c, "GET", "/token/"+id, ""), 200, nil)
	if !e.mr.Exists(tokenKey(id)) {
		t.Fatal("the token must be back in redis after a read")
	}
	e.mr.Del(tokenKey(id))
	if resp := e.do("POST", "/"+id+"/after-evict", strings.NewReader("x")); resp.StatusCode != 200 {
		t.Fatalf("capture after eviction: %d", resp.StatusCode)
	}
	if !e.mr.Exists(tokenKey(id)) || e.mr.TTL(tokenKey(id)) != time.Hour {
		t.Fatalf("restored token: exists %v ttl %v", e.mr.Exists(tokenKey(id)), e.mr.TTL(tokenKey(id)))
	}
	if r, err := e.record(id); err != nil || r.Status != "live" || r.Requests != 1 || r.CreatedAt == nil {
		t.Fatalf("audit record after restore: %+v, %v", r, err)
	}

	// A write made before the cache expired is what comes back.
	e.json(e.as(c, "PATCH", "/token/"+id, `{"description":"Kept"}`), 200, nil)
	e.mr.FastForward(2 * time.Hour)
	var tok map[string]any
	e.json(e.as(c, "GET", "/token/"+id, ""), 200, &tok)
	if tok["description"] != "Kept" || tok["expires_at"] != nil {
		t.Fatalf("after the TTL: %v", tok)
	}

	// The token layer retries a capture whose token vanished in between.
	e.mr.Del(tokenKey(id))
	req := &store.Request{ID: "0192a000-0000-7000-8000-000000000001", TokenID: id, Method: "POST", Headers: map[string][]string{}, CreatedAt: time.Now().UTC()}
	if total, _, err := e.server.tokens.addRequest(context.Background(), req, []byte("retry")); err != nil || total != 1 {
		t.Fatalf("addRequest after eviction: total %d, %v", total, err)
	}

	// An ID that exists nowhere is remembered for a minute.
	unknown := "11111111-2222-7333-8444-555555555555"
	e.json(e.do("GET", "/token/"+unknown, nil), 404, nil)
	if !e.mr.Exists("wh:{"+unknown+"}:none") || e.mr.TTL("wh:{"+unknown+"}:none") != time.Minute {
		t.Fatalf("negative cache: exists %v ttl %v", e.mr.Exists("wh:{"+unknown+"}:none"), e.mr.TTL("wh:{"+unknown+"}:none"))
	}
	e.json(e.do("POST", "/"+unknown, nil), 404, nil)
	e.json(e.do("GET", "/token/"+unknown, nil), 404, nil)
}

func TestPostgresDownIs503(t *testing.T) {
	e := setupAccounts(t)
	_, c := e.firstAdmin("root@example.com", "correct horse battery")
	id := e.ownedToken(c, `{}`)["uuid"].(string)
	e.mr.Del(tokenKey(id))
	e.acc.Close()
	// Never 404: webhook senders give up on those, but retry a 503.
	e.json(e.do("GET", "/token/"+id, nil), 503, nil)
	e.json(e.do("POST", "/"+id+"/hook", strings.NewReader("x")), 503, nil)
	if e.mr.Exists("wh:{" + id + "}:none") {
		t.Fatal("a database error must not be cached as unknown")
	}
}

func TestDeletedPrivateURL(t *testing.T) {
	e := setupAccounts(t, func(c *config.Config) { c.Retention = retention })
	_, admin := e.firstAdmin("root@example.com", "correct horse battery")
	_, owner := e.invite("owner@example.com", "owner's password")
	id := e.ownedToken(owner, `{}`)["uuid"].(string)
	e.do("POST", "/"+id, strings.NewReader("x"))
	if resp := e.as(owner, "DELETE", "/token/"+id, ""); resp.StatusCode != 204 {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if _, ok := e.urlRow(id); ok {
		t.Fatal("the postgres row must go")
	}
	if e.mr.HGet("wh:{"+id+"}:audit", "owned") != "1" {
		t.Fatalf("audit record: %v", e.mr.HGet("wh:{"+id+"}:audit", "owned"))
	}
	// It leaves no public trace, and does not come back.
	for _, c := range []*http.Cookie{nil, owner} {
		e.json(e.as(c, "GET", "/token/"+id, ""), 404, nil)
	}
	e.json(e.do("POST", "/"+id, strings.NewReader("more")), 404, nil)
	var gone map[string]any
	e.json(e.as(admin, "GET", "/token/"+id, ""), 410, &gone)
	if gone["deleted_at"] == nil {
		t.Fatalf("admin 410: %v", gone)
	}
	e.mr.FastForward(2 * time.Minute) // past the negative cache
	e.json(e.do("GET", "/token/"+id, nil), 404, nil)
	e.json(e.as(owner, "GET", "/token/"+id, ""), 404, nil)
	if e.mr.Exists(tokenKey(id)) {
		t.Fatal("a deleted private URL must not be restored")
	}
	if r, err := e.record(id); err != nil || r.Status != "deleted" {
		t.Fatalf("record: %+v, %v", r, err)
	}
}

func TestClaim(t *testing.T) {
	e := setupAccounts(t, func(c *config.Config) { c.Retention = retention })
	u, c := e.firstAdmin("root@example.com", "correct horse battery")
	tok := e.newToken(`{}`)
	id := tok["uuid"].(string)
	key, _ := tok["claim_key"].(string)
	if len(key) != 43 || tok["owner_id"] != nil || tok["expires_at"] == nil || tok["claim_hash"] != nil {
		t.Fatalf("anonymous token with accounts on: %v", tok)
	}
	sum := sha256.Sum256([]byte(key))
	if !strings.Contains(e.get(tokenKey(id)), `"claim_hash":"`+hex.EncodeToString(sum[:])+`"`) {
		t.Fatalf("redis must keep the hash only: %s", e.get(tokenKey(id)))
	}
	// The key is given out once; the hash never.
	e.json(e.do("GET", "/token/"+id, nil), 200, &tok)
	if tok["claim_key"] != nil || tok["claim_hash"] != nil {
		t.Fatalf("view: %v", tok)
	}

	claim := func(c *http.Cookie, id, key string) *http.Response {
		return e.as(c, "POST", "/token/"+id+"/claim", `{"claim_key":"`+key+`"}`)
	}
	e.json(claim(nil, id, key), 401, nil)
	e.json(claim(c, "11111111-2222-7333-8444-555555555555", key), 404, nil)
	var fail map[string]any
	e.json(claim(c, id, "wrong"), 403, &fail)
	if fail["error"] != "Wrong claim key" {
		t.Fatalf("wrong key: %v", fail)
	}
	e.json(claim(c, id, ""), 403, nil)
	e.json(e.as(c, "POST", "/token/"+id+"/claim", `{}`), 403, nil)

	e.json(claim(c, id, key), 200, &tok)
	if tok["owner_id"] != u["id"] || tok["expires_at"] != nil || tok["claim_key"] != nil || tok["claim_hash"] != nil {
		t.Fatalf("claimed: %v", tok)
	}
	if owner, ok := e.urlRow(id); !ok || owner != u["id"] {
		t.Fatalf("postgres row: %q %v", owner, ok)
	}
	if strings.Contains(e.get(tokenKey(id)), "claim_hash") {
		t.Fatal("the claim hash must be cleared")
	}
	audit := "wh:{" + id + "}:audit"
	if e.mr.HGet(audit, "owner_id") != u["id"] || e.mr.HGet(audit, "claimed_at") == "" {
		t.Fatalf("audit record: owner %q claimed_at %q", e.mr.HGet(audit, "owner_id"), e.mr.HGet(audit, "claimed_at"))
	}
	e.json(e.do("GET", "/token/"+id, nil), 403, nil)
	e.json(claim(c, id, key), 409, &fail)
	if fail["error"] != "This URL already belongs to an account" {
		t.Fatalf("second claim: %v", fail)
	}

	// Deleted URLs cannot be claimed.
	other := e.newToken(`{}`)
	e.json(e.do("DELETE", "/token/"+other["uuid"].(string), nil), 204, nil)
	e.json(claim(c, other["uuid"].(string), other["claim_key"].(string)), 404, nil)
}

// slowPUT opens PUT /token/{id} and sends everything but the last byte of
// the body, so the handler sits waiting for it; finish sends that byte and
// returns the status.
func slowPUT(t *testing.T, addr, id string) (finish func() int) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"description":"stale"}`
	fmt.Fprintf(conn, "PUT /token/%s HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s",
		id, len(body), body[:len(body)-1])
	return func() int {
		defer conn.Close()
		fmt.Fprint(conn, body[len(body)-1:])
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		_, _ = io.ReadAll(resp.Body)
		return resp.StatusCode
	}
}

func TestStaleAnonymousWriteAfterClaim(t *testing.T) {
	e := setupAccounts(t)
	u, c := e.invite("owner@example.com", "owner's password")
	claim := func(tok map[string]any) {
		t.Helper()
		e.json(e.as(c, "POST", "/token/"+tok["uuid"].(string)+"/claim", `{"claim_key":"`+tok["claim_key"].(string)+`"}`), 200, nil)
	}

	// A PUT whose body is still arriving when the URL is claimed must not
	// bring the anonymous copy back.
	tok := e.newToken(`{}`)
	id := tok["uuid"].(string)
	finish := slowPUT(t, e.srv.Listener.Addr().String(), id)
	time.Sleep(200 * time.Millisecond) // the handler is at the body by now
	claim(tok)
	if st := finish(); st != 403 {
		t.Fatalf("stale PUT after the claim: %d, want 403", st)
	}
	var v map[string]any
	e.json(e.as(c, "GET", "/token/"+id, ""), 200, &v)
	if v["owner_id"] != u["id"] || v["description"] != "" {
		t.Fatalf("after the stale PUT: %v", v)
	}
	e.json(e.do("GET", "/token/"+id, nil), 403, nil)

	// Without a body to wait on the window is the write itself: a copy read
	// before the claim is refused there, whatever the write.
	tok = e.newToken(`{}`)
	id = tok["uuid"].(string)
	e.do("POST", "/"+id+"/one", strings.NewReader("1"))
	ctx := context.Background()
	stale, err := e.server.tokens.read(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	claim(tok)
	stale.Description = "stale"
	if err := e.server.tokens.update(ctx, stale); !errors.Is(err, store.ErrOwned) {
		t.Fatalf("stale update: %v", err)
	}
	if err := e.server.tokens.delete(ctx, stale, "10.0.0.1", "test"); !errors.Is(err, store.ErrOwned) {
		t.Fatalf("stale delete: %v", err)
	}
	var l listResp
	e.json(e.as(c, "GET", "/token/"+id+"/requests", ""), 200, &l)
	rid := l.Data[0].ID
	if _, err := e.server.store.DeleteRequest(ctx, stale, rid); !errors.Is(err, store.ErrOwned) {
		t.Fatalf("stale request delete: %v", err)
	}
	if err := e.server.store.DeleteRequests(ctx, stale); !errors.Is(err, store.ErrOwned) {
		t.Fatalf("stale clear: %v", err)
	}
	e.json(e.as(c, "GET", "/token/"+id, ""), 200, &v)
	if v["owner_id"] != u["id"] || v["description"] != "" || v["request_count"].(float64) != 1 {
		t.Fatalf("after the stale writes: %v", v)
	}
	// A current copy writes as usual.
	owned, err := e.server.tokens.read(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if removed, err := e.server.store.DeleteRequest(ctx, owned, rid); err != nil || !removed {
		t.Fatalf("owner's request delete: %v %v", removed, err)
	}
	if err := e.server.store.DeleteRequests(ctx, owned); err != nil {
		t.Fatalf("owner's clear: %v", err)
	}
}

func TestRestoreRefusedAfterDelete(t *testing.T) {
	for name, keep := range map[string]time.Duration{"with audit records": retention, "without audit records": 0} {
		t.Run(name, func(t *testing.T) {
			e := setupAccounts(t, func(c *config.Config) { c.Retention = keep })
			_, c := e.invite("owner@example.com", "owner's password")
			id := e.ownedToken(c, `{}`)["uuid"].(string)
			ctx := context.Background()
			// A replica read the row to fill the cache again...
			row, err := e.acc.URL(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			// ...and the owner deleted the URL before it wrote.
			if resp := e.as(c, "DELETE", "/token/"+id, ""); resp.StatusCode != 204 {
				t.Fatalf("delete: %d", resp.StatusCode)
			}
			if _, err := e.server.tokens.restore(ctx, row); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("restore after the delete: %v", err)
			}
			if e.mr.Exists(tokenKey(id)) {
				t.Fatal("a deleted URL must not come back")
			}
			e.json(e.as(c, "GET", "/token/"+id, ""), 404, nil)
			e.json(e.do("POST", "/"+id, strings.NewReader("x")), 404, nil)
		})
	}
}

func TestRelease(t *testing.T) {
	e := setupAccounts(t)
	_, admin := e.firstAdmin("root@example.com", "correct horse battery")
	u, owner := e.invite("owner@example.com", "owner's password")
	_, other := e.invite("other@example.com", "other's password")
	id := e.ownedToken(owner, `{"description":"Shared"}`)["uuid"].(string)
	e.do("POST", "/"+id, strings.NewReader("x"))

	e.json(e.as(nil, "POST", "/token/"+id+"/release", ""), 403, nil)
	e.json(e.as(other, "POST", "/token/"+id+"/release", ""), 403, nil)
	var tok map[string]any
	e.json(e.as(owner, "POST", "/token/"+id+"/release", ""), 200, &tok)
	key, _ := tok["claim_key"].(string)
	if len(key) != 43 || tok["owner_id"] != nil || tok["expires_at"] == nil || tok["description"] != "Shared" || tok["request_count"].(float64) != 1 {
		t.Fatalf("released: %v", tok)
	}
	if _, ok := e.urlRow(id); ok {
		t.Fatal("the postgres row must go")
	}
	e.json(e.do("GET", "/token/"+id, nil), 200, &tok)
	if tok["claim_key"] != nil || tok["expires_at"] == nil {
		t.Fatalf("anonymous again: %v", tok)
	}
	var fail map[string]any
	e.json(e.as(owner, "POST", "/token/"+id+"/release", ""), 409, &fail)
	if fail["error"] != "This URL is already anonymous" {
		t.Fatalf("release twice: %v", fail)
	}

	// The new key claims it back; an admin may release it too.
	e.json(e.as(other, "POST", "/token/"+id+"/claim", `{"claim_key":"`+key+`"}`), 200, &tok)
	if tok["owner_id"] == u["id"] || tok["owner_id"] == nil {
		t.Fatalf("claimed by someone else: %v", tok)
	}
	e.json(e.as(owner, "GET", "/token/"+id, ""), 403, nil)
	e.json(e.as(admin, "POST", "/token/"+id+"/release", ""), 200, &tok)
	if tok["claim_key"] == nil || tok["owner_id"] != nil {
		t.Fatalf("admin release: %v", tok)
	}
}

func TestURLLimit(t *testing.T) {
	e := setupAccounts(t, func(c *config.Config) { c.MaxURLs = 2 })
	_, c := e.firstAdmin("root@example.com", "correct horse battery")
	first := e.ownedToken(c, `{}`)["uuid"].(string)
	e.ownedToken(c, `{}`)
	var fail map[string]any
	e.json(e.as(c, "POST", "/token", `{}`), 403, &fail)
	if fail["error"] != "You have reached the limit of 2 URLs; delete one first" {
		t.Fatalf("limit: %v", fail)
	}
	anon := e.newToken(`{}`) // anonymous URLs do not count
	e.json(e.as(c, "POST", "/token/"+anon["uuid"].(string)+"/claim", `{"claim_key":"`+anon["claim_key"].(string)+`"}`), 403, &fail)
	if fail["error"] != "You have reached the limit of 2 URLs; delete one first" {
		t.Fatalf("claim over the limit: %v", fail)
	}
	if _, ok := e.urlRow(anon["uuid"].(string)); ok {
		t.Fatal("a refused claim must not insert")
	}
	e.json(e.do("GET", "/token/"+anon["uuid"].(string), nil), 200, nil) // still anonymous
	if resp := e.as(c, "DELETE", "/token/"+first, ""); resp.StatusCode != 204 {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	e.ownedToken(c, `{}`)
	e.json(e.as(c, "POST", "/token/"+anon["uuid"].(string)+"/claim", `{"claim_key":"`+anon["claim_key"].(string)+`"}`), 403, nil)
}

func TestAnonymousURLsOff(t *testing.T) {
	e := setupAccounts(t, func(c *config.Config) { c.AnonymousURLs = false })
	var cfg map[string]any
	e.json(e.do("GET", "/api/config", nil), 200, &cfg)
	if cfg["anonymous_urls"] != false {
		t.Fatalf("config: %v", cfg)
	}
	var fail map[string]any
	e.json(e.do("POST", "/token", strings.NewReader(`{}`), "Content-Type", "application/json"), 401, &fail)
	if fail["error"] != "Sign in to create URLs" {
		t.Fatalf("anonymous create: %v", fail)
	}
	_, c := e.firstAdmin("root@example.com", "correct horse battery")
	id := e.ownedToken(c, `{}`)["uuid"].(string)
	if resp := e.do("POST", "/"+id, strings.NewReader("x")); resp.StatusCode != 200 {
		t.Fatalf("capture: %d", resp.StatusCode)
	}

	on := setupAccounts(t)
	on.json(on.do("GET", "/api/config", nil), 200, &cfg)
	if cfg["anonymous_urls"] != true {
		t.Fatalf("default: %v", cfg)
	}
}

func TestListTokens(t *testing.T) {
	e := setupAccounts(t)
	_, c := e.firstAdmin("root@example.com", "correct horse battery")
	_, other := e.invite("other@example.com", "other's password")
	var fail map[string]any
	e.json(e.do("GET", "/token", nil), 401, &fail)
	if fail["error"] != "Sign in to list your URLs" {
		t.Fatalf("anonymous list: %v", fail)
	}
	var ids []string
	for _, d := range []string{"Stripe", "GitHub", "Shopify"} {
		ids = append(ids, e.ownedToken(c, `{"description":"`+d+`"}`)["uuid"].(string))
	}
	e.newToken(`{}`) // anonymous: not listed
	before := time.Now().UTC().Truncate(time.Second)
	e.do("POST", "/"+ids[1], strings.NewReader("x"))

	type page struct {
		Data       []map[string]any `json:"data"`
		Total      int              `json:"total"`
		PerPage    int              `json:"per_page"`
		Page       int              `json:"current_page"`
		IsLastPage bool             `json:"is_last_page"`
		From, To   int
	}
	var p page
	e.json(e.as(c, "GET", "/token", ""), 200, &p)
	if p.Total != 3 || len(p.Data) != 3 || p.PerPage != 20 || p.Page != 1 || !p.IsLastPage || p.From != 1 || p.To != 3 {
		t.Fatalf("list: %+v", p)
	}
	if p.Data[0]["description"] != "Shopify" || p.Data[1]["description"] != "GitHub" || p.Data[2]["description"] != "Stripe" {
		t.Fatalf("newest first: %v", p.Data)
	}
	gh := p.Data[1]
	if gh["uuid"] != ids[1] || gh["request_count"].(float64) != 1 || gh["latest_request_at"] == nil || gh["expires_at"] != nil || gh["url"] != e.srv.URL+"/"+ids[1] {
		t.Fatalf("item: %v", gh)
	}
	if at, err := time.Parse(time.RFC3339Nano, gh["latest_request_at"].(string)); err != nil || at.Before(before) {
		t.Fatalf("latest_request_at %v: %v", gh["latest_request_at"], err)
	}
	if p.Data[0]["latest_request_at"] != nil || p.Data[0]["request_count"].(float64) != 0 {
		t.Fatalf("idle item: %v", p.Data[0])
	}
	for _, k := range []string{"claim_key", "claim_hash"} {
		if _, has := gh[k]; has {
			t.Fatalf("%s in the list", k)
		}
	}

	e.json(e.as(c, "GET", "/token?per_page=2", ""), 200, &p)
	if len(p.Data) != 2 || p.Total != 3 || p.IsLastPage || p.From != 1 || p.To != 2 || p.Data[1]["description"] != "GitHub" {
		t.Fatalf("page 1: %+v", p)
	}
	e.json(e.as(c, "GET", "/token?per_page=2&page=2", ""), 200, &p)
	if len(p.Data) != 1 || p.Total != 3 || !p.IsLastPage || p.From != 3 || p.To != 3 || p.Data[0]["description"] != "Stripe" {
		t.Fatalf("page 2: %+v", p)
	}
	e.json(e.as(c, "GET", "/token?per_page=2&page=3", ""), 200, &p)
	if len(p.Data) != 0 || p.Total != 3 || !p.IsLastPage || p.From != 0 || p.To != 4 {
		t.Fatalf("past the end: %+v", p)
	}
	e.json(e.as(c, "GET", "/token?q=STRIPE", ""), 200, &p)
	if len(p.Data) != 1 || p.Total != 1 || p.Data[0]["description"] != "Stripe" {
		t.Fatalf("search: %+v", p)
	}
	e.json(e.as(c, "GET", "/token?q=%25", ""), 200, &p)
	if p.Total != 0 {
		t.Fatalf("search is literal: %+v", p)
	}
	e.json(e.as(c, "GET", "/token?per_page=500", ""), 200, &p)
	if p.PerPage != 100 {
		t.Fatalf("per_page cap: %d", p.PerPage)
	}
	e.json(e.as(other, "GET", "/token", ""), 200, &p)
	if p.Total != 0 || len(p.Data) != 0 {
		t.Fatalf("someone else's list: %+v", p)
	}
}

func TestStreamClosesOnClaimAndRelease(t *testing.T) {
	e := setupAccounts(t)
	if e.server.streamRecheck != time.Minute {
		t.Fatalf("stream re-check with accounts: %v", e.server.streamRecheck)
	}
	_, c := e.firstAdmin("root@example.com", "correct horse battery")
	tok := e.newToken(`{}`)
	id := tok["uuid"].(string)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	events := e.streamEvents(ctx, nil, id)
	e.warmStream(ctx, events, id)
	e.json(e.as(c, "POST", "/token/"+id+"/claim", `{"claim_key":"`+tok["claim_key"].(string)+`"}`), 200, nil)
	ev, ok := e.nextEvent(ctx, events)
	if !ok || ev["type"] != "token.claimed" || ev["token"] != nil {
		t.Fatalf("claim event: %v %v", ev, ok)
	}
	if _, ok := e.nextEvent(ctx, events); ok {
		t.Fatal("the stream must end after a claim")
	}
	// Anonymous viewers cannot come back; the owner can.
	if resp := e.do("GET", "/token/"+id+"/stream", nil); resp.StatusCode != 403 {
		t.Fatalf("reconnect: %d", resp.StatusCode)
	}
	events = e.streamEvents(ctx, c, id)
	e.warmStream(ctx, events, id)
	e.json(e.as(c, "POST", "/token/"+id+"/release", ""), 200, nil)
	if ev, ok := e.nextEvent(ctx, events); !ok || ev["type"] != "token.released" || ev["token"] != nil {
		t.Fatalf("release event: %v %v", ev, ok)
	}
	if _, ok := e.nextEvent(ctx, events); ok {
		t.Fatal("the stream must end after a release")
	}
}

// expectClosed waits for a stream to end, with no further event.
func (e *env) expectClosed(ctx context.Context, events <-chan map[string]any, within time.Duration) {
	e.t.Helper()
	deadline := time.After(within)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return
			}
			e.t.Fatalf("event before the stream closed: %v", ev)
		case <-deadline:
			e.t.Fatalf("the stream stayed open for %v", within)
		case <-ctx.Done():
			e.t.Fatal("timed out")
		}
	}
}

// stillOpen captures a request after a few re-check intervals and expects
// the stream to relay it.
func (e *env) stillOpen(ctx context.Context, events <-chan map[string]any, id string) {
	e.t.Helper()
	time.Sleep(5 * e.server.streamRecheck)
	e.do("POST", "/"+id+"/still-open", nil)
	if ev, ok := e.nextEvent(ctx, events); !ok || ev["type"] != "request.created" {
		e.t.Fatalf("after several re-checks: %v %v", ev, ok)
	}
}

func TestStreamRecheck(t *testing.T) {
	if setup(t).server.streamRecheck != 0 {
		t.Fatal("no re-check without accounts")
	}
	acc, db := accountstest.Open(t)
	e := setupServer(t, acc, func(s *Server) { s.streamRecheck = 100 * time.Millisecond },
		func(c *config.Config) { c.Accounts = true; c.SessionTTL = 2 * time.Hour })
	e.db = db
	_, admin := e.firstAdmin("root@example.com", "correct horse battery")
	_, owner := e.invite("owner@example.com", "owner's password")
	id := e.ownedToken(owner, `{}`)["uuid"].(string)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Authorized viewers stay across many intervals: the owner and an admin
	// of a private URL, anyone on an anonymous one.
	events := e.streamEvents(ctx, owner, id)
	e.warmStream(ctx, events, id)
	e.stillOpen(ctx, events, id)
	// Signing out elsewhere ends it within an interval or so.
	if resp := e.do("POST", "/api/logout", nil, cookieHeader(owner)...); resp.StatusCode != 204 {
		t.Fatalf("logout: %d", resp.StatusCode)
	}
	e.expectClosed(ctx, events, time.Second)

	adminEvents := e.streamEvents(ctx, admin, id)
	e.warmStream(ctx, adminEvents, id)
	anon := e.newToken(`{}`)["uuid"].(string)
	anonEvents := e.streamEvents(ctx, nil, anon)
	e.warmStream(ctx, anonEvents, anon)
	e.stillOpen(ctx, adminEvents, id)
	e.stillOpen(ctx, anonEvents, anon)

	// A disabled user's open stream on a private URL closes within an
	// interval or so.
	owner = sessionCookie(e.login("owner@example.com", "owner's password"))
	events = e.streamEvents(ctx, owner, id)
	e.warmStream(ctx, events, id)
	e.exec("update users set disabled_at = now() where email = $1", "owner@example.com")
	e.expectClosed(ctx, events, time.Second)
	// A deleted URL ends its streams too.
	if resp := e.as(admin, "DELETE", "/token/"+id, ""); resp.StatusCode != 204 {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	for {
		// Captures made while warming the other streams reached this one too.
		if ev, ok := e.nextEvent(ctx, adminEvents); !ok {
			break
		} else if ev["type"] != "token.deleted" && ev["type"] != "request.created" {
			t.Fatalf("event after delete: %v", ev)
		}
	}
}

func TestAccountsOffURLs(t *testing.T) {
	e := setup(t, func(c *config.Config) { c.AnonymousURLs = false }) // ignored without accounts
	var cfg map[string]any
	e.json(e.do("GET", "/api/config", nil), 200, &cfg)
	if cfg["anonymous_urls"] != true {
		t.Fatalf("config: %v", cfg)
	}
	tok := e.newToken(`{}`)
	id := tok["uuid"].(string)
	if tok["claim_key"] != nil || tok["claim_hash"] != nil || tok["owner_id"] != nil || tok["expires_at"] == nil {
		t.Fatalf("token without accounts: %v", tok)
	}
	if strings.Contains(e.get(tokenKey(id)), "claim_hash") {
		t.Fatal("no claim hash without accounts")
	}
	e.json(e.do("GET", "/token", nil), 404, nil)
	e.json(e.do("POST", "/token/"+id+"/claim", strings.NewReader(`{"claim_key":"x"}`), "Content-Type", "application/json"), 404, nil)
	e.json(e.do("POST", "/token/"+id+"/release", nil), 404, nil)
	e.json(e.do("GET", "/token/"+id, nil), 200, &tok)
	if tok["expires_at"] == nil {
		t.Fatalf("view: %v", tok)
	}
	e.mr.Del(tokenKey(id))
	e.json(e.do("GET", "/token/"+id, nil), 404, nil) // nowhere to restore from
	if e.mr.Exists("wh:{" + id + "}:none") {
		t.Fatal("no negative cache without accounts")
	}
}
