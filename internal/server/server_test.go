package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/dbaio/webhookgo/internal/config"
	"github.com/dbaio/webhookgo/internal/hub"
	"github.com/dbaio/webhookgo/internal/store"
)

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.DiscardHandler))
	os.Exit(m.Run())
}

type env struct {
	t   *testing.T
	srv *httptest.Server
	mr  *miniredis.Miniredis
	hub *hub.Hub
}

func setup(t *testing.T, mutate ...func(*config.Config)) *env {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })

	cfg := config.Config{
		AppName: "Test", MaxRequests: 500, TokenTTL: time.Hour, MaxBodySize: 1 << 20,
		MaxDelay: 5, RateLimit: 0, TokenLimit: 0,
	}
	for _, m := range mutate {
		m(&cfg)
	}
	st := store.New(rdb, cfg.TokenTTL, cfg.MaxRequests, cfg.MaxStorage, cfg.Retention)
	h := hub.New(store.EventChannelPrefix)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go h.Run(ctx, st.Subscribe(ctx))

	ui := fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><title>ui</title>")},
		"assets/app.js": {Data: []byte(strings.Repeat("console.log(1);", 200))},
	}
	s, err := New(cfg, st, h, ui, "test")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	t.Cleanup(s.Close)
	return &env{t: t, srv: srv, mr: mr, hub: h}
}

func (e *env) do(method, path string, body io.Reader, headers ...string) *http.Response {
	e.t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, body)
	if err != nil {
		e.t.Fatal(err)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func (e *env) json(resp *http.Response, wantStatus int, v any) {
	e.t.Helper()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		e.t.Fatalf("%s %s: status %d, want %d: %s", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, wantStatus, b)
	}
	if v != nil {
		if err := json.Unmarshal(b, v); err != nil {
			e.t.Fatalf("decode %s: %v", b, err)
		}
	}
}

func (e *env) newToken(body string) map[string]any {
	e.t.Helper()
	var tok map[string]any
	e.json(e.do("POST", "/token", strings.NewReader(body), "Content-Type", "application/json"), 201, &tok)
	return tok
}

type listResp struct {
	Data       []store.Request `json:"data"`
	Total      int             `json:"total"`
	IsLastPage bool            `json:"is_last_page"`
}

func (e *env) list(id, query string) listResp {
	e.t.Helper()
	var l listResp
	e.json(e.do("GET", "/token/"+id+"/requests?"+query, nil), 200, &l)
	return l
}

func TestCaptureAndInspect(t *testing.T) {
	e := setup(t)
	tok := e.newToken(`{}`)
	id := tok["uuid"].(string)
	if tok["default_status"].(float64) != 200 || tok["url"] != e.srv.URL+"/"+id {
		t.Fatalf("unexpected token: %v", tok)
	}

	resp := e.do("POST", "/"+id+"/hooks/github?b=2&a=1&a=3", strings.NewReader(`{"hello":"world"}`),
		"Content-Type", "application/json", "X-Custom", "yes")
	if resp.StatusCode != 200 || resp.Header.Get("X-Request-Id") == "" || resp.Header.Get("X-Token-Id") != id {
		t.Fatalf("capture: %d %v", resp.StatusCode, resp.Header)
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatal("custom responses must be sandboxed")
	}

	l := e.list(id, "")
	if l.Total != 1 || len(l.Data) != 1 || !l.IsLastPage {
		t.Fatalf("list: %+v", l)
	}
	r := l.Data[0]
	if r.Method != "POST" || r.Path != "/hooks/github" || r.Content != `{"hello":"world"}` || r.Size != 17 {
		t.Fatalf("request: %+v", r)
	}
	if got := r.Headers["x-custom"]; len(got) != 1 || got[0] != "yes" {
		t.Fatalf("headers: %v", r.Headers)
	}
	if len(r.Query) != 3 || r.Query[0].Name != "b" || r.Query[2] != (store.KV{Name: "a", Value: "3"}) {
		t.Fatalf("query order: %v", r.Query)
	}

	var one store.Request
	e.json(e.do("GET", "/token/"+id+"/request/"+r.ID, nil), 200, &one)
	if one.ID != r.ID {
		t.Fatal("get by id")
	}
	e.json(e.do("GET", "/token/"+id+"/request/latest", nil), 200, &one)
	if one.ID != r.ID {
		t.Fatal("latest")
	}
	raw := e.do("GET", "/token/"+id+"/request/"+r.ID+"/raw", nil)
	b, _ := io.ReadAll(raw.Body)
	if string(b) != `{"hello":"world"}` || !strings.HasPrefix(raw.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("raw: %s %s", b, raw.Header.Get("Content-Type"))
	}
}

func TestCustomResponse(t *testing.T) {
	e := setup(t)
	id := e.newToken(`{"default_status":"201","default_content":"<ok/>","default_content_type":"application/xml",
		"timeout":0,"response_headers":[{"name":"X-One","value":"1"},{"name":"X-Multi","value":"a"},{"name":"X-Multi","value":"b"}]}`)["uuid"].(string)

	resp := e.do("PUT", "/"+id, strings.NewReader("x"))
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 201 || string(b) != "<ok/>" || resp.Header.Get("Content-Type") != "application/xml" ||
		resp.Header.Get("X-One") != "1" || len(resp.Header.Values("X-Multi")) != 2 {
		t.Fatalf("custom response: %d %q %v", resp.StatusCode, b, resp.Header)
	}

	// A status code in the first path segment wins.
	if resp := e.do("GET", "/"+id+"/418/teapot", nil); resp.StatusCode != 418 {
		t.Fatalf("status override: %d", resp.StatusCode)
	}
	if resp := e.do("GET", "/"+id+"/12345", nil); resp.StatusCode != 201 {
		t.Fatalf("non-status segment: %d", resp.StatusCode)
	}
	if l := e.list(id, "sorting=newest"); l.Data[0].ResponseStatus != 201 || l.Data[1].ResponseStatus != 418 {
		t.Fatalf("recorded response status: %d %d", l.Data[0].ResponseStatus, l.Data[1].ResponseStatus)
	}

	// Partial update keeps other fields.
	var tok map[string]any
	e.json(e.do("PATCH", "/token/"+id, strings.NewReader(`{"description":"Stripe"}`), "Content-Type", "application/json"), 200, &tok)
	if tok["description"] != "Stripe" || tok["default_status"].(float64) != 201 {
		t.Fatalf("patch: %v", tok)
	}
	// Validation.
	e.json(e.do("PUT", "/token/"+id, strings.NewReader(`{"default_status":99}`)), 422, nil)
	e.json(e.do("PUT", "/token/"+id, strings.NewReader(`{"timeout":60}`)), 422, nil)
	e.json(e.do("PUT", "/token/"+id, strings.NewReader(`{"response_headers":[{"name":"Content-Length","value":"1"}]}`)), 422, nil)
	e.json(e.do("PUT", "/token/"+id, strings.NewReader(`{"response_headers":[{"name":"Bad Name","value":"1"}]}`)), 422, nil)
	for _, h := range []string{"Set-Cookie", "clear-site-data", "Service-Worker-Allowed"} {
		e.json(e.do("PUT", "/token/"+id, strings.NewReader(`{"response_headers":[{"name":"`+h+`","value":"x"}]}`)), 422, nil)
	}
	// Legacy form-encoded body.
	e.json(e.do("PUT", "/token/"+id, strings.NewReader("default_status=202&default_content=hi"),
		"Content-Type", "application/x-www-form-urlencoded"), 200, &tok)
	if tok["default_status"].(float64) != 202 || tok["default_content"] != "hi" {
		t.Fatalf("form update: %v", tok)
	}
}

func TestCORS(t *testing.T) {
	e := setup(t)
	id := e.newToken(`{}`)["uuid"].(string)
	if resp := e.do("POST", "/"+id, nil, "Origin", "https://example.com"); resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS headers must be off by default")
	}
	var res map[string]bool
	e.json(e.do("PUT", "/token/"+id+"/cors/toggle", nil), 200, &res)
	if !res["enabled"] {
		t.Fatal("toggle")
	}
	resp := e.do("OPTIONS", "/"+id, nil, "Origin", "https://example.com",
		"Access-Control-Request-Method", "POST", "Access-Control-Request-Headers", "x-sig")
	if resp.StatusCode != 204 || resp.Header.Get("Access-Control-Allow-Origin") != "https://example.com" ||
		resp.Header.Get("Access-Control-Allow-Headers") != "x-sig" {
		t.Fatalf("preflight: %d %v", resp.StatusCode, resp.Header)
	}
}

func TestEvictsOldest(t *testing.T) {
	e := setup(t, func(c *config.Config) { c.MaxRequests = 3 })
	id := e.newToken(`{}`)["uuid"].(string)
	for i := range 5 {
		e.do("POST", "/"+id+"/"+string(rune('a'+i)), nil)
	}
	l := e.list(id, "sorting=newest")
	if l.Total != 3 || len(l.Data) != 3 || l.Data[0].Path != "/e" || l.Data[2].Path != "/c" {
		t.Fatalf("eviction: total=%d %v", l.Total, paths(l.Data))
	}
	l = e.list(id, "sorting=oldest&per_page=2&page=2")
	if len(l.Data) != 1 || l.Data[0].Path != "/e" || !l.IsLastPage {
		t.Fatalf("pagination: %v", paths(l.Data))
	}
}

func paths(rs []store.Request) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Path)
	}
	return out
}

func TestBodies(t *testing.T) {
	e := setup(t, func(c *config.Config) { c.MaxBodySize = 1023 })
	id := e.newToken(`{}`)["uuid"].(string)

	e.do("POST", "/"+id+"/form", strings.NewReader("name=Jo%C3%A3o+Silva&tag=a&tag=b"),
		"Content-Type", "application/x-www-form-urlencoded")

	var mp bytes.Buffer
	mw := multipart.NewWriter(&mp)
	_ = mw.WriteField("title", "report")
	fw, _ := mw.CreateFormFile("file", "a.bin")
	_, _ = fw.Write([]byte{0, 1, 2})
	_ = mw.Close()
	e.do("POST", "/"+id+"/multipart", &mp, "Content-Type", mw.FormDataContentType())

	e.do("POST", "/"+id+"/binary", bytes.NewReader([]byte{0xff, 0xfe, 0x00, 0x01}), "Content-Type", "application/octet-stream")
	e.do("POST", "/"+id+"/big", strings.NewReader(strings.Repeat("é", 1000)), "Content-Type", "text/plain")

	byPath := map[string]store.Request{}
	for _, r := range e.list(id, "").Data {
		byPath[r.Path] = r
	}

	form := byPath["/form"]
	if len(form.Form) != 3 || form.Form[0] != (store.KV{Name: "name", Value: "João Silva"}) || form.Form[2].Value != "b" {
		t.Fatalf("urlencoded: %v", form.Form)
	}
	m := byPath["/multipart"]
	if len(m.Form) != 1 || m.Form[0].Value != "report" || len(m.Files) != 1 || m.Files[0].Filename != "a.bin" || m.Files[0].Size != 3 {
		t.Fatalf("multipart: %v %v", m.Form, m.Files)
	}
	bin := byPath["/binary"]
	if bin.Encoding != "base64" || bin.Content != "//4AAQ==" {
		t.Fatalf("binary: %q %q", bin.Encoding, bin.Content)
	}
	raw := e.do("GET", "/token/"+id+"/request/"+bin.ID+"/raw", nil)
	if b, _ := io.ReadAll(raw.Body); !bytes.Equal(b, []byte{0xff, 0xfe, 0x00, 0x01}) {
		t.Fatalf("raw binary: %v", b)
	}
	big := byPath["/big"]
	// 1023 bytes split the last two-byte "é": the partial rune is dropped.
	if !big.Truncated || big.Size != 2000 || big.Encoding != "" || len(big.Content) != 1022 {
		t.Fatalf("truncated: %v %d %q %d", big.Truncated, big.Size, big.Encoding, len(big.Content))
	}
}

func TestSearchAndDelete(t *testing.T) {
	e := setup(t)
	id := e.newToken(`{}`)["uuid"].(string)
	e.do("POST", "/"+id, strings.NewReader(`{"event":"invoice.paid"}`))
	e.do("GET", "/"+id+"?q=1", nil, "X-Signature", "abc123")
	e.do("DELETE", "/"+id, nil)

	if l := e.list(id, "q=INVOICE"); l.Total != 1 || l.Data[0].Method != "POST" {
		t.Fatalf("search body: %+v", l)
	}
	if l := e.list(id, "q=abc123"); l.Total != 1 || l.Data[0].Method != "GET" {
		t.Fatalf("search header: %+v", l)
	}
	if l := e.list(id, "method=get,delete"); l.Total != 2 {
		t.Fatalf("method filter: %+v", l)
	}
	var sl struct {
		Data  []store.Summary `json:"data"`
		Total int             `json:"total"`
	}
	e.json(e.do("GET", "/token/"+id+"/requests?summary=1&method=POST&q=paid", nil), 200, &sl)
	if sl.Total != 1 || sl.Data[0].Method != "POST" {
		t.Fatalf("summary search: %+v", sl)
	}
	// Absurd page numbers must not overflow.
	e.json(e.do("GET", "/token/"+id+"/requests?page=4611686018427387905&per_page=3&q=a", nil), 200, nil)
	var sum struct {
		Data []map[string]any `json:"data"`
	}
	e.json(e.do("GET", "/token/"+id+"/requests?summary=1", nil), 200, &sum)
	if _, has := sum.Data[0]["content"]; has {
		t.Fatal("summary must not include the body")
	}

	first := e.list(id, "").Data[0]
	e.json(e.do("DELETE", "/token/"+id+"/request/"+first.ID, nil), 200, nil)
	e.json(e.do("GET", "/token/"+id+"/request/"+first.ID, nil), 404, nil)
	if l := e.list(id, ""); l.Total != 2 {
		t.Fatalf("after delete: %d", l.Total)
	}
	e.json(e.do("DELETE", "/token/"+id+"/request", nil), 200, nil)
	if l := e.list(id, ""); l.Total != 0 {
		t.Fatalf("after clear: %d", l.Total)
	}
	if resp := e.do("DELETE", "/token/"+id, nil); resp.StatusCode != 204 {
		t.Fatalf("delete token: %d", resp.StatusCode)
	}
	e.json(e.do("POST", "/"+id, nil), 404, nil)
	e.json(e.do("GET", "/token/"+id, nil), 404, nil)
}

func TestExpiryIsRefreshed(t *testing.T) {
	e := setup(t)
	id := e.newToken(`{}`)["uuid"].(string)
	e.do("POST", "/"+id, nil)
	e.mr.FastForward(50 * time.Minute)
	e.json(e.do("GET", "/token/"+id, nil), 200, nil) // touch
	e.mr.FastForward(50 * time.Minute)
	if l := e.list(id, ""); l.Total != 1 {
		t.Fatal("requests must live as long as the URL is used")
	}
	e.mr.FastForward(61 * time.Minute)
	e.json(e.do("GET", "/token/"+id, nil), 404, nil)
}

func TestRateLimit(t *testing.T) {
	e := setup(t, func(c *config.Config) { c.RateLimit = 2 })
	id := e.newToken(`{}`)["uuid"].(string)
	e.do("POST", "/"+id, nil)
	e.do("POST", "/"+id, nil)
	resp := e.do("POST", "/"+id, nil)
	if resp.StatusCode != 429 || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("rate limit: %d", resp.StatusCode)
	}
}

func TestLiveStream(t *testing.T) {
	e := setup(t)
	id := e.newToken(`{}`)["uuid"].(string)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/token/"+id+"/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	events := make(chan map[string]any, 8)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				var ev map[string]any
				_ = json.Unmarshal([]byte(data), &ev)
				events <- ev
			}
		}
	}()
	next := func() map[string]any {
		select {
		case ev := <-events:
			return ev
		case <-ctx.Done():
			t.Fatal("timed out waiting for event")
			return nil
		}
	}
	if ev := next(); ev["type"] != "connected" {
		t.Fatalf("hello: %v", ev)
	}
	// The pattern subscription is asynchronous; retry until it is active.
	var ev map[string]any
	for ev == nil {
		e.do("POST", "/"+id+"/live", strings.NewReader("hi"))
		select {
		case ev = <-events:
		case <-time.After(200 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("no live event")
		}
	}
	if ev["type"] != "request.created" || ev["request"].(map[string]any)["path"] != "/live" {
		t.Fatalf("event: %v", ev)
	}
}

func TestStaticUI(t *testing.T) {
	e := setup(t)
	resp := e.do("GET", "/", nil)
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatalf("index: %d %v", resp.StatusCode, resp.Header)
	}
	req, _ := http.NewRequest("GET", e.srv.URL+"/assets/app.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("Content-Encoding") != "gzip" || !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("asset: %v", resp.Header)
	}
	e.json(e.do("GET", "/nope.js", nil), 404, nil)
	e.json(e.do("GET", "/healthz", nil), 200, nil)
	var cfg map[string]any
	e.json(e.do("GET", "/api/config", nil), 200, &cfg)
	if cfg["max_requests"].(float64) != 500 {
		t.Fatalf("config: %v", cfg)
	}
}

func TestStorageQuotaAndRawBodies(t *testing.T) {
	e := setup(t, func(c *config.Config) { c.MaxStorage = 3000 })
	id := e.newToken(`{}`)["uuid"].(string)
	body := strings.Repeat("<&>", 500) // 1500 bytes that JSON would escape to 9000
	for _, p := range []string{"/a", "/b", "/c"} {
		e.do("POST", "/"+id+p, strings.NewReader(body), "Content-Type", "text/html")
	}
	l := e.list(id, "sorting=newest")
	if l.Total != 1 || l.Data[0].Path != "/c" || l.Data[0].Content != body {
		t.Fatalf("quota eviction: total=%d %v", l.Total, paths(l.Data))
	}
	stored := e.mr.HGet("wh:{"+id+"}:body", l.Data[0].ID)
	if len(stored) != len(body) {
		t.Fatalf("body stored as %d bytes, want raw %d", len(stored), len(body))
	}
	if meta := e.mr.HGet("wh:{"+id+"}:data", l.Data[0].ID); strings.Contains(meta, "<&>") || strings.Contains(meta, `\u003c`) {
		t.Fatalf("metadata must not carry the body: %s", meta)
	}
}

func TestRepairsMissingData(t *testing.T) {
	e := setup(t)
	id := e.newToken(`{}`)["uuid"].(string)
	e.do("POST", "/"+id+"/keep", nil)
	e.do("POST", "/"+id+"/lost", nil)
	lost := e.list(id, "sorting=newest").Data[0]
	// Simulate Redis evicting the data of one request under memory pressure.
	e.mr.HDel("wh:{"+id+"}:sum", lost.ID)
	e.mr.HDel("wh:{"+id+"}:data", lost.ID)
	if l := e.list(id, "summary=1"); len(l.Data) != 1 {
		t.Fatalf("missing entry must be skipped: %d", len(l.Data))
	}
	if l := e.list(id, ""); l.Total != 1 {
		t.Fatalf("index must be repaired: total %d", l.Total)
	}
}

func TestProxyHops(t *testing.T) {
	e := setup(t, func(c *config.Config) { c.ProxyHops = 1 })
	id := e.newToken(`{}`)["uuid"].(string)
	// The client sent "6.6.6.6"; the trusted proxy appended the real address.
	e.do("POST", "/"+id, nil, "X-Forwarded-For", "6.6.6.6, 203.0.113.9", "X-Forwarded-Proto", "https", "X-Forwarded-Host", "hooks.example.com")
	r := e.list(id, "").Data[0]
	if r.IP != "203.0.113.9" || !strings.HasPrefix(r.URL, "https://hooks.example.com/") {
		t.Fatalf("ip %q url %q", r.IP, r.URL)
	}

	plain := setup(t)
	pid := plain.newToken(`{}`)["uuid"].(string)
	plain.do("POST", "/"+pid, nil, "X-Forwarded-For", "6.6.6.6")
	if ip := plain.list(pid, "").Data[0].IP; ip != "127.0.0.1" {
		t.Fatalf("X-Forwarded-For must be ignored without TRUST_PROXY: %q", ip)
	}
}

func TestAPIRateLimit(t *testing.T) {
	e := setup(t, func(c *config.Config) { c.APILimit = 3 })
	id := e.newToken(`{}`)["uuid"].(string)          // 1
	e.json(e.do("GET", "/token/"+id, nil), 200, nil) // 2
	e.json(e.do("GET", "/token/"+id, nil), 200, nil) // 3
	e.json(e.do("GET", "/token/"+id, nil), 429, nil)
	if resp := e.do("POST", "/"+id, nil); resp.StatusCode != 200 {
		t.Fatalf("captures have their own limit: %d", resp.StatusCode)
	}
}
