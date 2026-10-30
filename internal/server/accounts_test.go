package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dbaio/webhookgo/internal/accounts"
	"github.com/dbaio/webhookgo/internal/accounts/accountstest"
	"github.com/dbaio/webhookgo/internal/config"
)

// freshWindow waits for the next rate-limit window when fewer than need
// remain in the current one, so a test's hits all land in the same window.
func freshWindow(t *testing.T, window, need time.Duration) {
	t.Helper()
	if left := window - time.Duration(time.Now().UnixNano())%window; left < need {
		time.Sleep(left + 50*time.Millisecond)
	}
}

// setupAccounts builds a server with accounts on, over a throwaway Postgres
// schema (skipped without TEST_DATABASE_URL).
func setupAccounts(t *testing.T, mutate ...func(*config.Config)) *env {
	t.Helper()
	acc, db := accountstest.Open(t)
	on := func(c *config.Config) { c.Accounts = true; c.SessionTTL = 2 * time.Hour }
	e := setupWith(t, acc, append([]func(*config.Config){on}, mutate...)...)
	e.db = db
	return e
}

func jsonBody(v any) io.Reader {
	b, _ := json.Marshal(v)
	return strings.NewReader(string(b))
}

// post sends JSON; extra headers follow in pairs.
func (e *env) post(method, path string, body any, headers ...string) *http.Response {
	e.t.Helper()
	return e.do(method, path, jsonBody(body), append([]string{"Content-Type", "application/json"}, headers...)...)
}

// sessionCookie returns the session cookie a response set, or nil.
func sessionCookie(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if (c.Name == "wg_session" || c.Name == "__Host-wg_session") && c.MaxAge > 0 {
			return c
		}
	}
	return nil
}

func cookieHeader(c *http.Cookie) []string {
	return []string{"Cookie", c.Name + "=" + c.Value}
}

func (e *env) link(l accounts.NewLink) string {
	e.t.Helper()
	if l.TTL == 0 {
		l.TTL = time.Hour
	}
	token, err := e.acc.CreateLink(context.Background(), l)
	if err != nil {
		e.t.Fatal(err)
	}
	return token
}

// firstAdmin creates the admin through a setup link and returns the user
// and the session cookie.
func (e *env) firstAdmin(email, password string) (map[string]any, *http.Cookie) {
	e.t.Helper()
	token := e.link(accounts.NewLink{Kind: accounts.LinkSetup})
	resp := e.post("POST", "/api/links/redeem", map[string]string{"token": token, "email": email, "name": "Root", "password": password})
	var out struct {
		User map[string]any `json:"user"`
	}
	e.json(resp, 200, &out)
	c := sessionCookie(resp)
	if c == nil {
		e.t.Fatal("setup must sign the admin in")
	}
	return out.User, c
}

func (e *env) me(c *http.Cookie) map[string]any {
	e.t.Helper()
	var out struct {
		User map[string]any `json:"user"`
	}
	var headers []string
	if c != nil {
		headers = cookieHeader(c)
	}
	e.json(e.do("GET", "/api/me", nil, headers...), 200, &out)
	return out.User
}

func (e *env) login(email, password string) *http.Response {
	e.t.Helper()
	return e.post("POST", "/api/login", map[string]string{"email": email, "password": password})
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.db.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatal(err)
	}
}

func TestAccountsOff(t *testing.T) {
	e := setup(t)
	var cfg map[string]any
	e.json(e.do("GET", "/api/config", nil), 200, &cfg)
	if cfg["accounts"] != false {
		t.Fatalf("config: %v", cfg)
	}
	if u := e.me(nil); u != nil {
		t.Fatalf("me: %v", u)
	}
	for _, p := range []string{"/login", "/setup", "/invite", "/reset", "/account"} {
		e.json(e.do("GET", p, nil), 404, nil)
	}
	e.json(e.login("a@example.com", "password-123"), 404, nil)
	e.json(e.post("POST", "/api/links/inspect", map[string]string{"token": "x"}), 404, nil)
	e.json(e.post("POST", "/api/links/redeem", map[string]string{"token": "x"}), 404, nil)
	e.json(e.do("POST", "/api/logout", nil), 404, nil)
	e.json(e.post("PATCH", "/api/account", map[string]string{"name": "x"}), 404, nil)
	e.json(e.post("POST", "/api/account/password", map[string]string{}), 404, nil)
	e.json(e.do("POST", "/api/account/logout-others", nil), 404, nil)
	// Nothing is wrapped in cross-origin checks.
	e.json(e.do("POST", "/token", strings.NewReader(`{}`), "Sec-Fetch-Site", "cross-site"), 201, nil)
}

func TestCaptureStripsSessionCookie(t *testing.T) {
	e := setup(t)
	id := e.newToken(`{}`)["uuid"].(string)
	e.do("POST", "/"+id+"/one", nil, "Cookie", "a=1; wg_session=SECRET; __Host-wg_session=X; b=2")
	e.do("POST", "/"+id+"/only", nil, "Cookie", "__Host-wg_session=X")
	req, _ := http.NewRequest("POST", e.srv.URL+"/"+id+"/multi", nil)
	req.Header.Add("Cookie", "wg_session=SECRET")
	req.Header.Add("Cookie", "c=3")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	byPath := map[string][]string{}
	for _, r := range e.list(id, "").Data {
		byPath[r.Path] = r.Headers["cookie"]
	}
	if got := byPath["/one"]; len(got) != 1 || got[0] != "a=1; b=2" {
		t.Fatalf("mixed cookies: %q", got)
	}
	if got, has := byPath["/only"]; has && got != nil {
		t.Fatalf("a header left empty must be dropped: %q", got)
	}
	if got := byPath["/multi"]; len(got) != 1 || got[0] != "c=3" {
		t.Fatalf("several Cookie headers: %q", got)
	}
	if e.mr.Exists("wh:{" + id + "}:body") {
		for _, r := range e.list(id, "").Data {
			if strings.Contains(fmt.Sprint(r.Headers), "SECRET") {
				t.Fatal("the secret was stored")
			}
		}
	}
}

func TestSetupAndLogin(t *testing.T) {
	e := setupAccounts(t)
	var cfg map[string]any
	e.json(e.do("GET", "/api/config", nil), 200, &cfg)
	if cfg["accounts"] != true {
		t.Fatalf("config: %v", cfg)
	}
	for _, p := range []string{"/login", "/setup", "/invite", "/reset", "/account"} {
		resp := e.do("GET", p, nil)
		if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "script-src 'self'") {
			t.Fatalf("%s: %d %v", p, resp.StatusCode, resp.Header)
		}
	}

	token := e.link(accounts.NewLink{Kind: accounts.LinkSetup})
	var l map[string]string
	e.json(e.post("POST", "/api/links/inspect", map[string]string{"token": token}), 200, &l)
	if l["kind"] != "setup" || l["email"] != "" || l["role"] != "admin" {
		t.Fatalf("inspect: %v", l)
	}
	e.json(e.post("POST", "/api/links/inspect", map[string]string{"token": "nope"}), 404, nil)
	for _, bad := range []map[string]string{
		{"token": token, "email": "root@example.com", "name": "Root", "password": "short"},
		{"token": token, "email": "not an email", "name": "Root", "password": "long enough password"},
		{"token": token, "email": "root@example.com", "name": "", "password": "long enough password"},
	} {
		e.json(e.post("POST", "/api/links/redeem", bad), 422, nil)
	}
	resp := e.post("POST", "/api/links/redeem", map[string]string{"token": token, "email": "root@example.com", "name": "Root", "password": "correct horse battery"})
	var out struct {
		User map[string]any `json:"user"`
	}
	e.json(resp, 200, &out)
	if out.User["email"] != "root@example.com" || out.User["role"] != "admin" || out.User["name"] != "Root" || out.User["id"] == "" || out.User["last_login_at"] == nil {
		t.Fatalf("admin: %v", out.User)
	}
	c := sessionCookie(resp)
	if c == nil || c.Name != "wg_session" || c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.MaxAge != 7200 {
		t.Fatalf("cookie over http: %+v", c)
	}
	if u := e.me(c); u == nil || u["email"] != "root@example.com" {
		t.Fatalf("me: %v", u)
	}
	// The link is spent, and setup is over for good.
	e.json(e.post("POST", "/api/links/inspect", map[string]string{"token": token}), 404, nil)
	again := e.link(accounts.NewLink{Kind: accounts.LinkSetup})
	e.json(e.post("POST", "/api/links/inspect", map[string]string{"token": again}), 404, nil)
	e.json(e.post("POST", "/api/links/redeem", map[string]string{"token": again, "email": "evil@example.com", "name": "x", "password": "long enough password"}), 404, nil)

	// Login: one generic error for a wrong password and an unknown email.
	var fail1, fail2 map[string]any
	e.json(e.login("root@example.com", "correct horse batter"), 401, &fail1)
	e.json(e.login("nobody@example.com", "correct horse battery"), 401, &fail2)
	if fail1["error"] != "Invalid email or password" || fail2["error"] != fail1["error"] {
		t.Fatalf("login errors: %v %v", fail1, fail2)
	}
	e.json(e.login("", ""), 422, nil)
	e.json(e.do("POST", "/api/login", strings.NewReader("{"), "Content-Type", "application/json"), 422, nil)
	e.json(e.do("POST", "/api/login", strings.NewReader(`{"email":"`+strings.Repeat("x", 17<<10)+`"}`), "Content-Type", "application/json"), 413, nil)
	resp = e.login("ROOT@example.com", "correct horse battery")
	e.json(resp, 200, &out)
	c2 := sessionCookie(resp)
	if c2 == nil || c2.Value == c.Value {
		t.Fatal("login must start a new session")
	}
	if u := e.me(c2); u == nil || u["id"] != out.User["id"] {
		t.Fatalf("me after login: %v", u)
	}

	// Logout ends the session and clears both cookie names.
	resp = e.do("POST", "/api/logout", nil, cookieHeader(c2)...)
	if resp.StatusCode != 204 {
		t.Fatalf("logout: %d", resp.StatusCode)
	}
	cleared := map[string]bool{}
	for _, ck := range resp.Cookies() {
		if ck.MaxAge < 0 {
			cleared[ck.Name] = true
		}
	}
	if !cleared["wg_session"] || !cleared["__Host-wg_session"] {
		t.Fatalf("cleared cookies: %v", cleared)
	}
	if u := e.me(c2); u != nil {
		t.Fatalf("me after logout: %v", u)
	}
	if u := e.me(c); u == nil {
		t.Fatal("logout must only end its own session")
	}
	if e.do("POST", "/api/logout", nil).StatusCode != 204 {
		t.Fatal("logout without a session")
	}
	// A stale cookie is cleared on the way through.
	if resp := e.do("GET", "/api/me", nil, cookieHeader(c2)...); len(resp.Cookies()) == 0 {
		t.Fatal("a stale cookie must be cleared")
	}
}

func TestInviteAndReset(t *testing.T) {
	e := setupAccounts(t)
	e.firstAdmin("root@example.com", "correct horse battery")

	invite := e.link(accounts.NewLink{Kind: accounts.LinkInvite, Email: "ana@example.com"})
	var l map[string]string
	e.json(e.post("POST", "/api/links/inspect", map[string]string{"token": invite}), 200, &l)
	if l["kind"] != "invite" || l["email"] != "ana@example.com" || l["role"] != "user" {
		t.Fatalf("inspect invite: %v", l)
	}
	e.json(e.post("POST", "/api/links/redeem", map[string]string{"token": invite, "name": "", "password": "ana's password"}), 422, nil)
	resp := e.post("POST", "/api/links/redeem", map[string]string{"token": invite, "email": "ignored@example.com", "name": "Ana", "password": "ana's password"})
	var out struct {
		User map[string]any `json:"user"`
	}
	e.json(resp, 200, &out)
	if out.User["email"] != "ana@example.com" || out.User["role"] != "user" || out.User["name"] != "Ana" {
		t.Fatalf("invited user: %v", out.User)
	}
	c1 := sessionCookie(resp)
	if c1 == nil {
		t.Fatal("invite must sign the user in")
	}
	dup := e.link(accounts.NewLink{Kind: accounts.LinkInvite, Email: "ANA@example.com"})
	e.json(e.post("POST", "/api/links/redeem", map[string]string{"token": dup, "name": "Ana", "password": "ana's password"}), 409, nil)

	// A reset ends every session of the user.
	c2 := sessionCookie(e.login("ana@example.com", "ana's password"))
	reset := e.link(accounts.NewLink{Kind: accounts.LinkReset, UserID: out.User["id"].(string)})
	e.json(e.post("POST", "/api/links/inspect", map[string]string{"token": reset}), 200, &l)
	if l["kind"] != "reset" || l["email"] != "ana@example.com" {
		t.Fatalf("inspect reset: %v", l)
	}
	resp = e.post("POST", "/api/links/redeem", map[string]string{"token": reset, "password": "a brand new password"})
	e.json(resp, 200, &out)
	c3 := sessionCookie(resp)
	for _, old := range []*http.Cookie{c1, c2} {
		if u := e.me(old); u != nil {
			t.Fatal("old sessions must end on reset")
		}
	}
	if u := e.me(c3); u == nil || u["email"] != "ana@example.com" {
		t.Fatalf("session from reset: %v", u)
	}
	e.json(e.login("ana@example.com", "ana's password"), 401, nil)
	e.json(e.login("ana@example.com", "a brand new password"), 200, nil)
	e.json(e.post("POST", "/api/links/redeem", map[string]string{"token": reset, "password": "a brand new password"}), 404, nil)
}

func TestDisabledAccount(t *testing.T) {
	e := setupAccounts(t)
	_, c := e.firstAdmin("root@example.com", "correct horse battery")
	e.exec("update users set disabled_at = now()")
	var fail map[string]any
	e.json(e.login("root@example.com", "correct horse battery"), 403, &fail)
	if fail["error"] != "This account is disabled" {
		t.Fatalf("disabled: %v", fail)
	}
	e.json(e.login("root@example.com", "wrong password here"), 401, nil) // never before a correct password
	if u := e.me(c); u != nil {
		t.Fatal("a disabled user has no session")
	}
}

func TestSessionSlides(t *testing.T) {
	e := setupAccounts(t)
	_, c := e.firstAdmin("root@example.com", "correct horse battery")
	e.exec("update sessions set last_seen_at = now() - interval '2 minutes', expires_at = now() + interval '1 hour'")
	if e.me(c) == nil {
		t.Fatal("live session")
	}
	var slid bool
	if err := e.db.QueryRow(context.Background(), "select expires_at > now() + interval '90 minutes' from sessions").Scan(&slid); err != nil || !slid {
		t.Fatalf("expiry must move to now + SESSION_TTL: %v %v", slid, err)
	}
	e.exec("update sessions set expires_at = now() - interval '1 second'")
	if u := e.me(c); u != nil {
		t.Fatalf("expired session: %v", u)
	}
}

func TestAccountSettings(t *testing.T) {
	e := setupAccounts(t)
	_, c1 := e.firstAdmin("root@example.com", "correct horse battery")
	c2 := sessionCookie(e.login("root@example.com", "correct horse battery"))
	c3 := sessionCookie(e.login("root@example.com", "correct horse battery"))

	e.json(e.post("PATCH", "/api/account", map[string]string{"name": "Nobody"}), 401, nil)
	e.json(e.post("PATCH", "/api/account", map[string]string{"name": strings.Repeat("x", 101)}, cookieHeader(c1)...), 422, nil)
	var out struct {
		User map[string]any `json:"user"`
	}
	e.json(e.post("PATCH", "/api/account", map[string]string{"name": " Ana Root "}, cookieHeader(c1)...), 200, &out)
	if out.User["name"] != "Ana Root" {
		t.Fatalf("rename: %v", out.User)
	}
	if u := e.me(c2); u["name"] != "Ana Root" {
		t.Fatalf("name after rename: %v", u)
	}

	// Signing out elsewhere keeps the current session only.
	e.json(e.do("POST", "/api/account/logout-others", nil), 401, nil)
	if resp := e.do("POST", "/api/account/logout-others", nil, cookieHeader(c3)...); resp.StatusCode != 204 {
		t.Fatalf("logout-others: %d", resp.StatusCode)
	}
	if e.me(c2) != nil || e.me(c3) == nil {
		t.Fatal("logout-others must keep only the current session")
	}

	// Changing the password: the current one is checked, others sessions end.
	c4 := sessionCookie(e.login("root@example.com", "correct horse battery"))
	e.json(e.post("POST", "/api/account/password", map[string]string{"current_password": "correct horse battery", "new_password": "new password here"}), 401, nil)
	e.json(e.post("POST", "/api/account/password", map[string]string{"current_password": "wrong", "new_password": "new password here"}, cookieHeader(c3)...), 403, nil)
	e.json(e.post("POST", "/api/account/password", map[string]string{"current_password": "correct horse battery", "new_password": "short"}, cookieHeader(c3)...), 422, nil)
	if resp := e.post("POST", "/api/account/password", map[string]string{"current_password": "correct horse battery", "new_password": "new password here"}, cookieHeader(c3)...); resp.StatusCode != 204 {
		t.Fatalf("change password: %d", resp.StatusCode)
	}
	if e.me(c4) != nil || e.me(c3) == nil {
		t.Fatal("a password change must end the other sessions")
	}
	e.json(e.login("root@example.com", "correct horse battery"), 401, nil)
	e.json(e.login("root@example.com", "new password here"), 200, nil)
}

func TestLoginRateLimit(t *testing.T) {
	e := setupAccounts(t, func(c *config.Config) { c.LoginLimit = 2; c.ProxyHops = 1 })
	e.firstAdmin("root@example.com", "correct horse battery")
	from := func(ip string) []string { return []string{"X-Forwarded-For", ip} }
	freshWindow(t, time.Minute, 20*time.Second) // argon2 under -race takes a while
	for i := range 3 {
		resp := e.post("POST", "/api/login", map[string]string{"email": "root@example.com", "password": "wrong password"}, from("10.0.0.1")...)
		want := 401
		if i == 2 {
			want = 429
		}
		if resp.StatusCode != want {
			t.Fatalf("attempt %d: %d, want %d", i, resp.StatusCode, want)
		}
	}
	// Inspect and redeem share the per-IP limit.
	e.json(e.post("POST", "/api/links/inspect", map[string]string{"token": "x"}, from("10.0.0.1")...), 429, nil)
	e.json(e.post("POST", "/api/links/redeem", map[string]string{"token": "x"}, from("10.0.0.1")...), 429, nil)
	e.json(e.post("POST", "/api/links/inspect", map[string]string{"token": "x"}, from("10.0.0.2")...), 404, nil)

	// Per email the limit is three times higher, whatever the IP within the
	// client's network (a /24 here).
	for i := range 7 {
		resp := e.post("POST", "/api/login", map[string]string{"email": "Other@example.com", "password": "wrong password"}, from(fmt.Sprintf("10.1.0.%d", i))...)
		want := 401
		if i >= 6 {
			want = 429
		}
		if resp.StatusCode != want {
			t.Fatalf("email attempt %d: %d, want %d", i, resp.StatusCode, want)
		}
	}
	// Another network has its own count: nobody is locked out of their
	// account from elsewhere.
	e.json(e.post("POST", "/api/login", map[string]string{"email": "other@example.com", "password": "wrong password"}, from("10.1.1.1")...), 401, nil)
}

func TestClientNetwork(t *testing.T) {
	s := &Server{cfg: config.Config{ProxyHops: 1}}
	for ip, want := range map[string]string{
		"10.1.0.7":         "10.1.0.0/24",
		"::ffff:10.1.0.7":  "10.1.0.0/24",
		"2001:db8:0:1::1":  "2001:db8::/56",
		"2001:db8:0:ff::1": "2001:db8::/56",
		"2001:db8:1::1":    "2001:db8:1::/56",
		"fe80::1%en0":      "fe80::/56",
		"not an address":   "not an address",
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("X-Forwarded-For", ip)
		if got := s.clientNetwork(r); got != want {
			t.Errorf("clientNetwork(%s) = %s, want %s", ip, got, want)
		}
	}
}

func TestPlainCookieIgnoredOverHTTPS(t *testing.T) {
	e := setupAccounts(t, func(c *config.Config) { c.PublicURL = "https://hooks.example.com" })
	_, c := e.firstAdmin("owner@example.com", "owner's password")
	// The same secret in a plain wg_session cookie, as one planted over HTTP
	// or by a sibling domain would be, signs nobody in.
	plain := &http.Cookie{Name: "wg_session", Value: c.Value}
	if u := e.me(plain); u != nil {
		t.Fatalf("plain cookie over https: %v", u)
	}
}

func TestCrossOriginProtection(t *testing.T) {
	e := setupAccounts(t)
	e.firstAdmin("root@example.com", "correct horse battery")
	body := map[string]string{"email": "root@example.com", "password": "correct horse battery"}
	// Browsers say where a request comes from; other sites are refused.
	e.json(e.post("POST", "/api/login", body, "Sec-Fetch-Site", "cross-site"), 403, nil)
	e.json(e.post("POST", "/api/login", body, "Sec-Fetch-Site", "same-site"), 403, nil)
	e.json(e.post("POST", "/api/login", body, "Origin", "https://evil.example"), 403, nil)
	e.json(e.do("POST", "/token", strings.NewReader(`{}`), "Sec-Fetch-Site", "cross-site"), 403, nil)
	// Same origin, a trusted origin and non-browser clients pass.
	e.json(e.post("POST", "/api/login", body, "Sec-Fetch-Site", "same-origin"), 200, nil)
	e.json(e.post("POST", "/api/login", body, "Origin", e.srv.URL), 200, nil)
	e.json(e.post("POST", "/api/login", body), 200, nil)
	e.json(e.do("GET", "/api/me", nil, "Sec-Fetch-Site", "cross-site"), 200, nil)
	// Captures are webhooks: never subject to it.
	id := e.newToken(`{}`)["uuid"].(string)
	if resp := e.do("POST", "/"+id, strings.NewReader("hi"), "Sec-Fetch-Site", "cross-site", "Origin", "https://evil.example"); resp.StatusCode != 200 {
		t.Fatalf("capture: %d", resp.StatusCode)
	}

	trusted := setupAccounts(t, func(c *config.Config) { c.PublicURL = "https://hooks.example.com" })
	trusted.firstAdmin("root@example.com", "correct horse battery")
	trusted.json(trusted.post("POST", "/api/login", body, "Origin", "https://hooks.example.com", "Sec-Fetch-Site", "cross-site"), 200, nil)
	trusted.json(trusted.post("POST", "/api/login", body, "Origin", "https://other.example.com", "Sec-Fetch-Site", "cross-site"), 403, nil)
}

func TestSecureCookieOverHTTPS(t *testing.T) {
	e := setupAccounts(t, func(c *config.Config) { c.PublicURL = "https://hooks.example.com" })
	_, c := e.firstAdmin("root@example.com", "correct horse battery")
	if c.Name != "__Host-wg_session" || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Domain != "" {
		t.Fatalf("cookie with PUBLIC_URL https: %+v", c)
	}
	if u := e.me(c); u == nil {
		t.Fatal("the __Host- cookie must be read")
	}

	proxied := setupAccounts(t, func(c *config.Config) { c.ProxyHops = 1 })
	proxied.firstAdmin("root@example.com", "correct horse battery")
	resp := proxied.post("POST", "/api/login", map[string]string{"email": "root@example.com", "password": "correct horse battery"}, "X-Forwarded-Proto", "https")
	if c := sessionCookie(resp); c == nil || c.Name != "__Host-wg_session" || !c.Secure {
		t.Fatalf("cookie behind a trusted https proxy: %+v", c)
	}
	resp = proxied.post("POST", "/api/login", map[string]string{"email": "root@example.com", "password": "correct horse battery"})
	if c := sessionCookie(resp); c == nil || c.Name != "wg_session" || c.Secure {
		t.Fatalf("cookie over http: %+v", c)
	}
}

func TestAPIRateLimitCoversAccounts(t *testing.T) {
	e := setup(t, func(c *config.Config) { c.APILimit = 2 })
	e.json(e.do("GET", "/api/config", nil), 200, nil)
	e.json(e.do("GET", "/api/me", nil), 200, nil)
	e.json(e.do("GET", "/api/me", nil), 429, nil)
}
