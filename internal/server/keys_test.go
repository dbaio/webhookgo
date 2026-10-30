package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dbaio/webhookgo/internal/accounts"
	"github.com/dbaio/webhookgo/internal/accounts/accountstest"
	"github.com/dbaio/webhookgo/internal/config"
)

// keyed sends a request authenticated with an API key, in the Api-Key
// header or as a Bearer token; body is JSON text.
func (e *env) keyed(key, method, path, body string, bearer bool) *http.Response {
	e.t.Helper()
	headers := []string{"Content-Type", "application/json", "Api-Key", key}
	if bearer {
		headers = []string{"Content-Type", "application/json", "Authorization", "Bearer " + key}
	}
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	return e.do(method, path, r, headers...)
}

// createKey makes an API key through the session API and returns the answer,
// the key itself included.
func (e *env) createKey(c *http.Cookie, name string) map[string]any {
	e.t.Helper()
	var out map[string]any
	e.json(e.as(c, "POST", "/api/keys", `{"name":"`+name+`"}`), 201, &out)
	return out
}

// streamKeyed opens the live stream of a URL with an API key and returns its
// events, as streamEvents does for a session.
func (e *env) streamKeyed(ctx context.Context, key, id string) <-chan map[string]any {
	e.t.Helper()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/token/"+id+"/stream", nil)
	req.Header.Set("Api-Key", key)
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

func TestAPIKeys(t *testing.T) {
	e := setupAccounts(t)
	_, c := e.firstAdmin("root@example.com", "correct horse battery")
	_, other := e.invite("other@example.com", "other's password")

	e.json(e.do("GET", "/api/keys", nil), 401, nil)
	e.json(e.post("POST", "/api/keys", map[string]string{"name": "x"}), 401, nil)
	e.json(e.as(c, "POST", "/api/keys", `{"name":""}`), 422, nil)
	e.json(e.as(c, "POST", "/api/keys", `{"name":"`+strings.Repeat("x", 101)+`"}`), 422, nil)
	e.json(e.as(c, "POST", "/api/keys", ``), 422, nil)

	k := e.createKey(c, " CI deploy ")
	key, _ := k["key"].(string)
	if !strings.HasPrefix(key, "wgo_") || len(key) != 47 || k["name"] != "CI deploy" || k["prefix"] != key[4:12] || k["last_used_at"] != nil || k["id"] == nil || k["created_at"] == nil {
		t.Fatalf("new key: %v", k)
	}
	var hashed bool
	if err := e.db.QueryRow(context.Background(), "select hash = sha256($1) and name = 'CI deploy' from api_keys where id = $2", []byte(key), k["id"]).Scan(&hashed); err != nil || !hashed {
		t.Fatalf("stored row: %v %v", hashed, err)
	}

	// Listed without the key, newest first, each user their own.
	second := e.createKey(c, "laptop")
	e.createKey(other, "theirs")
	var l struct {
		Data []map[string]any `json:"data"`
	}
	e.json(e.as(c, "GET", "/api/keys", ""), 200, &l)
	if len(l.Data) != 2 || l.Data[0]["id"] != second["id"] || l.Data[1]["id"] != k["id"] || l.Data[1]["prefix"] != key[4:12] {
		t.Fatalf("list: %v", l.Data)
	}
	for _, item := range l.Data {
		if _, has := item["key"]; has {
			t.Fatal("the key must appear only on creation")
		}
		if _, has := item["user_id"]; has {
			t.Fatal("user_id in the list")
		}
	}
	e.json(e.as(other, "GET", "/api/keys", ""), 200, &l)
	if len(l.Data) != 1 || l.Data[0]["name"] != "theirs" {
		t.Fatalf("other's list: %v", l.Data)
	}

	// Revoking: only one's own, once; the row stays.
	e.json(e.as(other, "DELETE", "/api/keys/"+k["id"].(string), ""), 404, nil)
	e.json(e.as(c, "DELETE", "/api/keys/not-a-uuid", ""), 404, nil)
	e.json(e.as(c, "DELETE", "/api/keys/11111111-2222-7333-8444-555555555555", ""), 404, nil)
	e.json(e.do("DELETE", "/api/keys/"+k["id"].(string), nil), 401, nil)
	if resp := e.as(c, "DELETE", "/api/keys/"+k["id"].(string), ""); resp.StatusCode != 204 {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}
	e.json(e.as(c, "DELETE", "/api/keys/"+k["id"].(string), ""), 404, nil)
	e.json(e.as(c, "GET", "/api/keys", ""), 200, &l)
	if len(l.Data) != 1 || l.Data[0]["id"] != second["id"] {
		t.Fatalf("list after revoke: %v", l.Data)
	}
	var revoked bool
	if err := e.db.QueryRow(context.Background(), "select revoked_at is not null from api_keys where id = $1", k["id"]).Scan(&revoked); err != nil || !revoked {
		t.Fatalf("revoked row: %v %v", revoked, err)
	}
}

func TestAPIKeyAuth(t *testing.T) {
	e := setupAccounts(t)
	u, c := e.firstAdmin("root@example.com", "correct horse battery")
	key := e.createKey(c, "ci")["key"].(string)

	for _, bearer := range []bool{false, true} {
		var tok map[string]any
		e.json(e.keyed(key, "POST", "/token", `{"description":"From CI"}`, bearer), 201, &tok)
		id := tok["uuid"].(string)
		if tok["owner_id"] != u["id"] || tok["expires_at"] != nil || tok["claim_key"] != nil {
			t.Fatalf("bearer=%v: token: %v", bearer, tok)
		}
		if owner, ok := e.urlRow(id); !ok || owner != u["id"] {
			t.Fatalf("postgres row: %q %v", owner, ok)
		}
		var p struct {
			Data  []map[string]any `json:"data"`
			Total int              `json:"total"`
		}
		e.json(e.keyed(key, "GET", "/token", "", bearer), 200, &p)
		if p.Total < 1 || p.Data[0]["uuid"] != id {
			t.Fatalf("bearer=%v: list: %+v", bearer, p)
		}
		e.do("POST", "/"+id+"/hook", strings.NewReader("hi"))
		var l listResp
		e.json(e.keyed(key, "GET", "/token/"+id+"/requests", "", bearer), 200, &l)
		if l.Total != 1 || l.Data[0].Path != "/hook" {
			t.Fatalf("bearer=%v: requests: %+v", bearer, l)
		}
		var req map[string]any
		e.json(e.keyed(key, "GET", "/token/"+id+"/wait?timeout=0&since=0", "", bearer), 200, &req)
		if req["path"] != "/hook" {
			t.Fatalf("bearer=%v: wait: %v", bearer, req)
		}
		e.json(e.keyed(key, "GET", "/token/"+id, "", bearer), 200, &tok)
		if tok["description"] != "From CI" {
			t.Fatalf("bearer=%v: get: %v", bearer, tok)
		}
		e.json(e.keyed(key, "PATCH", "/token/"+id, `{"description":"Renamed"}`, bearer), 200, nil)
		// Anyone else is still kept out.
		e.json(e.do("GET", "/token/"+id, nil), 403, nil)
		if resp := e.keyed(key, "DELETE", "/token/"+id, "", bearer); resp.StatusCode != 204 {
			t.Fatalf("bearer=%v: delete: %d", bearer, resp.StatusCode)
		}
	}
	// The use is noted on the key.
	var l struct {
		Data []map[string]any `json:"data"`
	}
	e.json(e.as(c, "GET", "/api/keys", ""), 200, &l)
	if len(l.Data) != 1 || l.Data[0]["last_used_at"] == nil {
		t.Fatalf("last_used_at: %v", l.Data)
	}
	// The owner's limit applies to keys too.
	limited := setupAccounts(t, func(c *config.Config) { c.MaxURLs = 1 })
	_, lc := limited.firstAdmin("root@example.com", "correct horse battery")
	lkey := limited.createKey(lc, "ci")["key"].(string)
	limited.json(limited.keyed(lkey, "POST", "/token", `{}`, false), 201, nil)
	var fail map[string]any
	limited.json(limited.keyed(lkey, "POST", "/token", `{}`, false), 403, &fail)
	if fail["error"] != "You have reached the limit of 1 URLs; delete one first" {
		t.Fatalf("limit: %v", fail)
	}
}

func TestAPIKeyRejected(t *testing.T) {
	e := setupAccounts(t)
	_, c := e.firstAdmin("root@example.com", "correct horse battery")
	_, owner := e.invite("owner@example.com", "owner's password")
	k := e.createKey(owner, "ci")
	key := k["key"].(string)
	id := e.keyed(key, "POST", "/token", `{}`, false)
	var tok map[string]any
	e.json(id, 201, &tok)
	url := tok["uuid"].(string)

	// A key never authenticates on /api: the request is simply anonymous.
	var me struct {
		User map[string]any `json:"user"`
	}
	for _, bearer := range []bool{false, true} {
		e.json(e.keyed(key, "GET", "/api/me", "", bearer), 200, &me)
		if me.User != nil {
			t.Fatalf("bearer=%v: /api/me with a key: %v", bearer, me.User)
		}
		var fail map[string]any
		e.json(e.keyed(key, "GET", "/api/keys", "", bearer), 401, &fail)
		if fail["error"] != "Not signed in" {
			t.Fatalf("bearer=%v: /api/keys with a key: %v", bearer, fail)
		}
		e.json(e.keyed(key, "POST", "/api/keys", `{"name":"x"}`, bearer), 401, nil)
		e.json(e.keyed(key, "PATCH", "/api/account", `{"name":"x"}`, bearer), 401, nil)
	}

	// A garbage key is refused outright, never treated as anonymous.
	for _, bad := range []string{"nope", "wgo_" + strings.Repeat("x", 43), key[:46], key + "x"} {
		for _, bearer := range []bool{false, true} {
			var fail map[string]any
			e.json(e.keyed(bad, "POST", "/token", `{}`, bearer), 401, &fail)
			if fail["error"] != "Invalid API key" || fail["status"].(float64) != 401 {
				t.Fatalf("bearer=%v: garbage key %q: %v", bearer, bad, fail)
			}
			e.json(e.keyed(bad, "GET", "/token", "", bearer), 401, nil)
			e.json(e.keyed(bad, "GET", "/token/"+url, "", bearer), 401, nil)
			e.json(e.keyed(bad, "GET", "/token/"+url+"/requests", "", bearer), 401, nil)
			e.json(e.keyed(bad, "GET", "/token/"+url+"/wait?timeout=0", "", bearer), 401, nil)
		}
	}
	e.json(e.do("POST", "/token", strings.NewReader(`{}`), "Content-Type", "application/json", "Authorization", "Basic abc"), 201, nil) // not a key: anonymous

	// A revoked key, and a disabled user's.
	if resp := e.as(owner, "DELETE", "/api/keys/"+k["id"].(string), ""); resp.StatusCode != 204 {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}
	e.json(e.keyed(key, "GET", "/token/"+url, "", false), 401, nil)
	e.json(e.keyed(key, "POST", "/token", `{}`, true), 401, nil)
	key2 := e.createKey(owner, "again")["key"].(string)
	e.json(e.keyed(key2, "GET", "/token/"+url, "", false), 200, nil)
	e.exec("update users set disabled_at = now() where email = $1", "owner@example.com")
	e.json(e.keyed(key2, "GET", "/token/"+url, "", false), 401, nil)
	e.json(e.keyed(key2, "GET", "/token", "", true), 401, nil)
	// Captures ignore the headers, as they ignore everything else.
	if resp := e.do("POST", "/"+url+"/hook", strings.NewReader("x"), "Api-Key", "nope"); resp.StatusCode != 200 {
		t.Fatalf("capture with a bad key: %d", resp.StatusCode)
	}
	// The admin's session still works alongside.
	e.json(e.as(c, "GET", "/token/"+url, ""), 200, nil)
}

func TestAPIKeyIsNeverAdmin(t *testing.T) {
	e := setupAccounts(t, func(c *config.Config) { c.Retention = retention })
	_, admin := e.firstAdmin("root@example.com", "correct horse battery")
	_, owner := e.invite("owner@example.com", "owner's password")
	id := e.ownedToken(owner, `{}`)["uuid"].(string)
	key := e.createKey(admin, "admin's key")["key"].(string)

	// The session sees it; the key, acting as a plain user, does not.
	e.json(e.as(admin, "GET", "/token/"+id, ""), 200, nil)
	var fail map[string]any
	e.json(e.keyed(key, "GET", "/token/"+id, "", false), 403, &fail)
	if fail["error"] != "URL is private" {
		t.Fatalf("admin's key on someone else's URL: %v", fail)
	}
	e.json(e.keyed(key, "GET", "/token/"+id+"/requests", "", true), 403, nil)
	e.json(e.keyed(key, "DELETE", "/token/"+id, "", false), 403, nil)
	e.json(e.keyed(key, "POST", "/token/"+id+"/release", "", false), 403, nil)
	// Deleted private URLs leave no trace to a key either.
	if resp := e.as(owner, "DELETE", "/token/"+id, ""); resp.StatusCode != 204 {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	e.json(e.as(admin, "GET", "/token/"+id, ""), 410, nil)
	e.json(e.keyed(key, "GET", "/token/"+id, "", false), 404, nil)
	// Its own URLs it sees, of course.
	own := e.keyed(key, "POST", "/token", `{}`, false)
	var tok map[string]any
	e.json(own, 201, &tok)
	e.json(e.keyed(key, "GET", "/token/"+tok["uuid"].(string), "", true), 200, nil)
}

func TestSessionWinsOverKey(t *testing.T) {
	e := setupAccounts(t)
	u, c := e.firstAdmin("root@example.com", "correct horse battery")
	o, owner := e.invite("owner@example.com", "owner's password")
	ownerKey := e.createKey(owner, "owner's key")["key"].(string)

	// Signed in as the admin and carrying the owner's key: the session wins.
	var tok map[string]any
	e.json(e.do("POST", "/token", strings.NewReader(`{}`), "Content-Type", "application/json", "Api-Key", ownerKey, "Cookie", c.Name+"="+c.Value), 201, &tok)
	if tok["owner_id"] != u["id"] {
		t.Fatalf("owner with session and key: %v", tok)
	}
	// Even a garbage key is ignored next to a valid session.
	e.json(e.do("GET", "/token/"+tok["uuid"].(string), nil, "Api-Key", "nope", "Cookie", c.Name+"="+c.Value), 200, nil)
	// A stale cookie counts for nothing: the key authenticates.
	stale := sessionCookie(e.login("owner@example.com", "owner's password"))
	if resp := e.do("POST", "/api/logout", nil, cookieHeader(stale)...); resp.StatusCode != 204 {
		t.Fatalf("logout: %d", resp.StatusCode)
	}
	e.json(e.do("POST", "/token", strings.NewReader(`{}`), "Content-Type", "application/json", "Api-Key", ownerKey, "Cookie", stale.Name+"="+stale.Value), 201, &tok)
	if tok["owner_id"] != o["id"] {
		t.Fatalf("stale cookie with key: %v", tok)
	}
}

func TestAPIKeyLimit(t *testing.T) {
	e := setupAccounts(t)
	_, c := e.firstAdmin("root@example.com", "correct horse battery")
	_, other := e.invite("other@example.com", "other's password")
	var last map[string]any
	for i := range accounts.MaxAPIKeys {
		last = e.createKey(c, "key "+string(rune('a'+i)))
	}
	var fail map[string]any
	e.json(e.as(c, "POST", "/api/keys", `{"name":"one too many"}`), 403, &fail)
	if fail["error"] != "You can have at most 20 API keys" {
		t.Fatalf("limit: %v", fail)
	}
	e.createKey(other, "someone else's") // the limit is per user
	if resp := e.as(c, "DELETE", "/api/keys/"+last["id"].(string), ""); resp.StatusCode != 204 {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}
	e.createKey(c, "room again") // revoked keys do not count
}

func TestStreamClosesOnKeyRevoke(t *testing.T) {
	acc, db := accountstest.Open(t)
	e := setupServer(t, acc, func(s *Server) { s.streamRecheck = 100 * time.Millisecond },
		func(c *config.Config) { c.Accounts = true; c.SessionTTL = 2 * time.Hour })
	e.db = db
	_, c := e.firstAdmin("root@example.com", "correct horse battery")
	k := e.createKey(c, "ci")
	key := k["key"].(string)
	var tok map[string]any
	e.json(e.keyed(key, "POST", "/token", `{}`, false), 201, &tok)
	id := tok["uuid"].(string)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// A valid key keeps the stream across many intervals; revoking it ends
	// the stream within an interval or so, and the key cannot come back.
	events := e.streamKeyed(ctx, key, id)
	e.warmStream(ctx, events, id)
	e.stillOpen(ctx, events, id)
	if resp := e.as(c, "DELETE", "/api/keys/"+k["id"].(string), ""); resp.StatusCode != 204 {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}
	e.expectClosed(ctx, events, time.Second)
	if resp := e.do("GET", "/token/"+id+"/stream", nil, "Api-Key", key); resp.StatusCode != 401 {
		t.Fatalf("reconnect with a revoked key: %d", resp.StatusCode)
	}

	// A revoked key closes a stream on an anonymous URL too: it would get
	// 401 on a new request, and the stream is no different.
	key2 := e.createKey(c, "two")["key"].(string)
	anon := e.newToken(`{}`)["uuid"].(string)
	anonEvents := e.streamKeyed(ctx, key2, anon)
	e.warmStream(ctx, anonEvents, anon)
	e.stillOpen(ctx, anonEvents, anon)
	var l struct {
		Data []map[string]any `json:"data"`
	}
	e.json(e.as(c, "GET", "/api/keys", ""), 200, &l)
	if resp := e.as(c, "DELETE", "/api/keys/"+l.Data[0]["id"].(string), ""); resp.StatusCode != 204 {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}
	e.expectClosed(ctx, anonEvents, time.Second)

	// A disabled user's open stream closes as well.
	key3 := e.createKey(c, "three")["key"].(string)
	events = e.streamKeyed(ctx, key3, id)
	e.warmStream(ctx, events, id)
	e.exec("update users set disabled_at = now()")
	e.expectClosed(ctx, events, time.Second)
}

func TestAccountsOffIgnoresKeys(t *testing.T) {
	e := setup(t)
	for _, bearer := range []bool{false, true} {
		var tok map[string]any
		e.json(e.keyed("wgo_"+strings.Repeat("x", 43), "POST", "/token", `{}`, bearer), 201, &tok)
		if tok["owner_id"] != nil || tok["expires_at"] == nil {
			t.Fatalf("bearer=%v: token with a key and accounts off: %v", bearer, tok)
		}
		e.json(e.keyed("nope", "GET", "/token/"+tok["uuid"].(string), "", bearer), 200, nil)
	}
	e.json(e.do("GET", "/api/keys", nil), 404, nil)
	e.json(e.post("POST", "/api/keys", map[string]string{"name": "x"}), 404, nil)
	e.json(e.do("DELETE", "/api/keys/11111111-2222-7333-8444-555555555555", nil), 404, nil)
}
