package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dbaio/webhookgo/internal/store"
)

func (e *env) wait(id string, q url.Values) *http.Response {
	e.t.Helper()
	return e.do("GET", "/token/"+id+"/wait?"+q.Encode(), nil)
}

// waitFor fails the test unless cond holds within two seconds.
func (e *env) waitFor(what string, cond func() bool) {
	e.t.Helper()
	for end := time.Now().Add(2 * time.Second); !cond(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(end) {
			e.t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func TestWaitFindsStoredRequests(t *testing.T) {
	e := setup(t)
	id := e.newToken(`{}`)["uuid"].(string)
	t0 := time.Now()
	e.do("POST", "/"+id+"/orders", strings.NewReader(`{"data":{"id":"evt_1"}}`), "Content-Type", "application/json")
	e.do("POST", "/"+id+"/orders", strings.NewReader(
		`{"data":{"id":"evt_2","amount":12345678901234567890,"paid":true,"note":null,"items":[{"sku":"A1"}]}}`),
		"Content-Type", "application/json")
	e.do("POST", "/"+id+"/form?src=e2e", strings.NewReader("a=1&b=two"),
		"Content-Type", "application/x-www-form-urlencoded", "X-Event", "paid")

	found := func(q url.Values) store.Request {
		t.Helper()
		var r store.Request
		e.json(e.wait(id, q), http.StatusOK, &r)
		return r
	}
	since := strconv.FormatInt(t0.UnixMilli(), 10)

	// By default only requests from the moment of the call count.
	e.json(e.wait(id, url.Values{"timeout": {"0"}, "match": {"body.data.id=evt_1"}}), http.StatusRequestTimeout, nil)

	if r := found(url.Values{"since": {since}, "match": {"body.data.id=evt_2"}}); !strings.Contains(r.Content, "evt_2") {
		t.Fatalf("matched %s", r.Content)
	}
	r := found(url.Values{"since": {since}, "match": {
		"body.data.amount=12345678901234567890", "body.data.paid=true", "body.data.note=null", "body.data.items.0.sku=A1",
	}})
	if !strings.Contains(r.Content, "evt_2") {
		t.Fatalf("scalar match: %s", r.Content)
	}
	// The oldest match wins; since=0 reads the whole history, RFC 3339 works too.
	if r := found(url.Values{"since": {"0"}, "method": {"post"}}); !strings.Contains(r.Content, "evt_1") {
		t.Fatalf("oldest: %s", r.Content)
	}
	if r := found(url.Values{"since": {t0.Add(-time.Second).Format(time.RFC3339)}, "q": {"EVT_2"}}); !strings.Contains(r.Content, "evt_2") {
		t.Fatalf("text search: %s", r.Content)
	}
	if r := found(url.Values{"since": {since}, "match": {
		"header.X-Event=paid", "query.src=e2e", "form.b=two", "path=/form",
	}}); r.Path != "/form" {
		t.Fatalf("header/query/form/path: %+v", r)
	}

	for _, m := range []string{"body.data.id=evt_3", "body.data.items.1.sku=A1", "body.data=evt_2", "header.x-event=PAID", "path=/orders/"} {
		e.json(e.wait(id, url.Values{"since": {since}, "timeout": {"0"}, "match": {m}}), http.StatusRequestTimeout, nil)
	}
	future := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	e.json(e.wait(id, url.Values{"since": {future}, "timeout": {"0"}}), http.StatusRequestTimeout, nil)
}

func TestWaitForLiveRequest(t *testing.T) {
	e := setup(t)
	id := e.newToken(`{}`)["uuid"].(string)
	e.waitFor("pub/sub", func() bool { return e.mr.PubSubNumPat() > 0 })

	type result struct {
		status int
		req    store.Request
		took   time.Duration
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		resp, err := http.Get(e.srv.URL + "/token/" + id + "/wait?timeout=5&match=body.id=42")
		if err != nil {
			done <- result{}
			return
		}
		defer resp.Body.Close()
		var r store.Request
		_ = json.NewDecoder(resp.Body).Decode(&r)
		done <- result{resp.StatusCode, r, time.Since(start)}
	}()
	e.waitFor("the wait to subscribe", func() bool { return e.hub.Clients(id) == 1 })

	e.do("POST", "/"+id+"/x", strings.NewReader(`{"id":41}`), "Content-Type", "application/json")
	e.do("POST", "/"+id+"/x", strings.NewReader(`{"id":42}`), "Content-Type", "application/json")
	res := <-done
	if res.status != http.StatusOK || res.req.Content != `{"id":42}` || res.took > 3*time.Second {
		t.Fatalf("live wait: %+v", res)
	}
	if n := e.hub.Clients(id); n != 0 {
		t.Fatalf("%d subscriptions left behind", n)
	}
}

func TestWaitTimeoutsAndErrors(t *testing.T) {
	e := setup(t)
	id := e.newToken(`{}`)["uuid"].(string)

	start := time.Now()
	resp := e.wait(id, url.Values{"timeout": {"1"}})
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusRequestTimeout || time.Since(start) < time.Second || !strings.Contains(string(b), "within 1s") {
		t.Fatalf("timeout: %d after %s: %s", resp.StatusCode, time.Since(start), b)
	}

	for _, q := range []url.Values{
		{"timeout": {"61"}}, {"timeout": {"-1"}}, {"timeout": {"soon"}},
		{"since": {"yesterday"}}, {"since": {"-5"}}, {"since": {"NaN"}},
		{"match": {"nope"}}, {"match": {"cookie.a=1"}}, {"match": {"body=1"}}, {"match": {"header.=1"}}, {"match": {"path.x=1"}},
		{"match": strings.Split(strings.Repeat("path=/,", maxMatches+1), ",")[:maxMatches+1]},
	} {
		e.json(e.wait(id, q), http.StatusUnprocessableEntity, nil)
	}
	e.json(e.wait("6f1c2a54-0000-4000-8000-000000000000", nil), http.StatusNotFound, nil)
}

func TestWaitEndsWhenURLIsDeleted(t *testing.T) {
	e := setup(t)
	id := e.newToken(`{}`)["uuid"].(string)
	e.waitFor("pub/sub", func() bool { return e.mr.PubSubNumPat() > 0 })

	status := make(chan int, 1)
	go func() {
		resp, err := http.Get(e.srv.URL + "/token/" + id + "/wait?timeout=5")
		if err != nil {
			status <- 0
			return
		}
		resp.Body.Close()
		status <- resp.StatusCode
	}()
	e.waitFor("the wait to subscribe", func() bool { return e.hub.Clients(id) == 1 })
	e.json(e.do("DELETE", "/token/"+id, nil), http.StatusNoContent, nil)
	if s := <-status; s != http.StatusNotFound {
		t.Fatalf("status %d after delete", s)
	}
}

func TestWaitEndsForAnonymousAfterClaim(t *testing.T) {
	e := setupAccounts(t)
	_, c := e.invite("owner@example.com", "owner's password")
	tok := e.newToken(`{}`)
	id := tok["uuid"].(string)
	e.waitFor("pub/sub", func() bool { return e.mr.PubSubNumPat() > 0 })

	// An anonymous caller and the account about to claim the URL both wait.
	open := func(c *http.Cookie) <-chan *http.Response {
		req, _ := http.NewRequest("GET", e.srv.URL+"/token/"+id+"/wait?timeout=10", nil)
		if c != nil {
			req.AddCookie(c)
		}
		done := make(chan *http.Response, 1)
		go func() {
			resp, _ := http.DefaultClient.Do(req)
			done <- resp
		}()
		return done
	}
	recv := func(done <-chan *http.Response) *http.Response {
		t.Helper()
		resp := <-done
		if resp == nil {
			t.Fatal("the wait failed")
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}
	anon, owner := open(nil), open(c)
	e.waitFor("the waits to subscribe", func() bool { return e.hub.Clients(id) == 2 })
	e.json(e.as(c, "POST", "/token/"+id+"/claim", `{"claim_key":"`+tok["claim_key"].(string)+`"}`), 200, nil)

	// The anonymous one is refused, as loadToken would refuse it now; the
	// owner's goes on and gets the next request.
	var fail map[string]any
	e.json(recv(anon), http.StatusForbidden, &fail)
	if fail["error"] != "URL is private" {
		t.Fatalf("anonymous wait after the claim: %v", fail)
	}
	e.do("POST", "/"+id+"/after", strings.NewReader("x"))
	var req store.Request
	e.json(recv(owner), http.StatusOK, &req)
	if req.Path != "/after" {
		t.Fatalf("owner's wait after the claim: %+v", req)
	}
}

func TestParseSince(t *testing.T) {
	want := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	for _, v := range []string{"1792238400", "1792238400.000", "1792238400000", "2026-10-17T12:00:00Z", "2026-10-17T09:00:00-03:00"} {
		got, err := parseSince(v)
		if err != nil || !got.Equal(want) {
			t.Errorf("parseSince(%q) = %v, %v", v, got, err)
		}
	}
	if got, _ := parseSince("1792238400.25"); got.Sub(want) != 250*time.Millisecond {
		t.Errorf("fractional seconds: %v", got)
	}
}
