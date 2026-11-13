package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dbaio/webhookgo/internal/accounts"
	"github.com/dbaio/webhookgo/internal/accounts/accountstest"
	"github.com/dbaio/webhookgo/internal/config"
)

const testClientID = "test-client.apps.googleusercontent.com"

// fakeGoogle plays Google's token endpoint: it checks the code and the
// PKCE verifier of the last sign-in started, and answers with an ID token
// for the claims a test sets.
type fakeGoogle struct {
	t   *testing.T
	srv *httptest.Server

	mu        sync.Mutex
	challenge string // from the last redirect to Google
	nonce     string
	claims    map[string]any // overrides of the ID token's claims; nil values drop a claim
	status    int            // answer of the token endpoint; 0 = 200
	form      url.Values     // what the token endpoint was sent
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	g := &fakeGoogle{t: t}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		_ = r.ParseForm()
		g.form = r.PostForm
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		switch {
		case r.URL.Path != "/token" || r.Method != "POST":
			http.NotFound(w, r)
			return
		case g.status != 0:
			w.WriteHeader(g.status)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Bad Request"}`))
			return
		case r.PostForm.Get("code") != "good-code" || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		claims := map[string]any{
			"iss": "https://accounts.google.com", "aud": testClientID, "sub": "1234567890",
			"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
			"nonce": g.nonce, "email": "someone@example.com", "email_verified": true,
		}
		for k, v := range g.claims {
			if v == nil {
				delete(claims, k)
			} else {
				claims[k] = v
			}
		}
		// Unless a test says otherwise, an address outside Gmail belongs to a
		// Workspace account of its own domain.
		if email, ok := claims["email"].(string); ok {
			if _, said := g.claims["hd"]; !said {
				if _, domain, _ := strings.Cut(strings.ToLower(email), "@"); domain != "gmail.com" {
					claims["hd"] = domain
				}
			}
		}
		payload, _ := json.Marshal(claims)
		token := "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(payload) + ".c2lnbmF0dXJl"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "ya29.x", "id_token": token, "token_type": "Bearer", "expires_in": 3599})
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeGoogle) set(claims map[string]any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.claims = claims
}

// setupGoogle builds a server with accounts and sign-in with Google on,
// talking to a fake Google.
func setupGoogle(t *testing.T, mutate ...func(*config.Config)) (*env, *fakeGoogle) {
	t.Helper()
	g := newFakeGoogle(t)
	acc, db := accountstest.Open(t)
	on := func(c *config.Config) {
		c.Accounts, c.SessionTTL, c.LoginLimit = true, 2*time.Hour, 0
		c.GoogleClientID, c.GoogleClientSecret = testClientID, "test-secret"
	}
	e := setupServer(t, acc, func(s *Server) {
		s.google.authURL = g.srv.URL + "/auth"
		s.google.tokenURL = g.srv.URL + "/token"
	}, append([]func(*config.Config){on}, mutate...)...)
	e.db = db
	return e, g
}

// onHost sends a test's requests to the server as if addressed to host.
var onHost = []string{"Host", "hooks.example.com"}

// noRedirect is a client that hands back redirects instead of following them.
var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func (e *env) noFollow(path string, headers ...string) *http.Response {
	e.t.Helper()
	req, err := http.NewRequest("GET", e.srv.URL+path, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		if headers[i] == "Host" {
			req.Host = headers[i+1]
		} else {
			req.Header.Set(headers[i], headers[i+1])
		}
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func flowCookie(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == "wg_google" || c.Name == "__Host-wg_google" {
			return c
		}
	}
	return nil
}

// googleStart begins a sign-in and returns the redirect to Google and the
// flow cookie, telling the fake Google what to expect.
func (e *env) googleStart(g *fakeGoogle, next string, headers ...string) (*url.URL, *http.Cookie) {
	e.t.Helper()
	path := "/api/auth/google"
	if next != "" {
		path += "?next=" + url.QueryEscape(next)
	}
	resp := e.noFollow(path, headers...)
	if resp.StatusCode != http.StatusFound {
		e.t.Fatalf("start: status %d", resp.StatusCode)
	}
	to, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		e.t.Fatal(err)
	}
	c := flowCookie(resp)
	if c == nil || c.MaxAge <= 0 || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
		e.t.Fatalf("flow cookie: %+v", c)
	}
	g.mu.Lock()
	g.challenge, g.nonce = to.Query().Get("code_challenge"), to.Query().Get("nonce")
	g.mu.Unlock()
	return to, c
}

// googleBack is Google sending the browser back with query q.
func (e *env) googleBack(c *http.Cookie, q url.Values) *http.Response {
	e.t.Helper()
	var headers []string
	if c != nil {
		headers = cookieHeader(c)
	}
	resp := e.noFollow(googleCallbackPath+"?"+q.Encode(), headers...)
	if resp.StatusCode != http.StatusSeeOther {
		e.t.Fatalf("callback: status %d", resp.StatusCode)
	}
	if fc := flowCookie(resp); fc == nil || fc.MaxAge >= 0 {
		e.t.Fatalf("the callback must clear the flow cookie: %+v", fc)
	}
	return resp
}

// googleSignIn goes through the whole flow as email and returns the
// callback's answer.
func (e *env) googleSignIn(g *fakeGoogle, email, next string) *http.Response {
	e.t.Helper()
	g.set(map[string]any{"email": email})
	to, c := e.googleStart(g, next)
	return e.googleBack(c, url.Values{"state": {to.Query().Get("state")}, "code": {"good-code"}})
}

// signedIn checks that a callback signed someone in and went to where, and
// returns the session cookie.
func signedIn(t *testing.T, resp *http.Response, where string) *http.Cookie {
	t.Helper()
	if got := resp.Header.Get("Location"); got != where {
		t.Fatalf("went to %q, want %q", got, where)
	}
	c := sessionCookie(resp)
	if c == nil {
		t.Fatal("no session cookie")
	}
	return c
}

func failedWith(t *testing.T, resp *http.Response, code string) {
	t.Helper()
	if got := resp.Header.Get("Location"); !strings.HasPrefix(got, "/login?error="+code) {
		t.Fatalf("went to %q, want the login page with %s", got, code)
	}
	if sessionCookie(resp) != nil {
		t.Fatal("a failed sign-in must not set a session")
	}
}

func TestGoogleOff(t *testing.T) {
	e := setupAccounts(t)
	var cfg map[string]any
	e.json(e.do("GET", "/api/config", nil), 200, &cfg)
	if cfg["google"] != false {
		t.Fatalf("google: %v", cfg["google"])
	}
	e.json(e.noFollow("/api/auth/google"), 404, nil)
	e.json(e.noFollow(googleCallbackPath+"?state=x&code=y"), 404, nil)

	// A client ID with accounts off changes nothing.
	off := setup(t, func(c *config.Config) { c.GoogleClientID, c.GoogleClientSecret = testClientID, "s" })
	e.json(off.noFollow("/api/auth/google"), 404, nil)
}

func TestGoogleStart(t *testing.T) {
	e, g := setupGoogle(t)
	var cfg map[string]any
	e.json(e.do("GET", "/api/config", nil), 200, &cfg)
	if cfg["google"] != true {
		t.Fatalf("google: %v", cfg["google"])
	}
	to, c := e.googleStart(g, "")
	q := to.Query()
	if to.Path != "/auth" || q.Get("client_id") != testClientID || q.Get("redirect_uri") != e.srv.URL+googleCallbackPath ||
		q.Get("response_type") != "code" || q.Get("scope") != "openid email" || q.Get("code_challenge_method") != "S256" ||
		q.Get("prompt") != "select_account" || q.Has("hd") {
		t.Fatalf("redirect to Google: %s", to)
	}
	if len(q.Get("state")) < 40 || len(q.Get("nonce")) < 40 || len(q.Get("code_challenge")) != 43 {
		t.Fatalf("weak flow values: %s", to)
	}
	if strings.Contains(to.String(), "test-secret") || strings.Contains(c.Value, "test-secret") {
		t.Fatal("the client secret must stay on the server")
	}
	if c.Name != "wg_google" || c.Secure || c.Path != "/" {
		t.Fatalf("flow cookie over HTTP: %+v", c)
	}
	// Each sign-in gets its own values.
	to2, _ := e.googleStart(g, "")
	if to2.Query().Get("state") == q.Get("state") || to2.Query().Get("nonce") == q.Get("nonce") {
		t.Fatal("state and nonce must be fresh")
	}

	// Over HTTPS, the cookie takes the __Host- prefix. Google is not asked
	// to favour a domain, even with one allowed: invited people from
	// elsewhere must be offered their own accounts.
	e2, g2 := setupGoogle(t, func(c *config.Config) {
		c.PublicURL, c.GoogleDomains = "https://hooks.example.com", []string{"example.com"}
	})
	to, c = e2.googleStart(g2, "", onHost...)
	if c.Name != "__Host-wg_google" || !c.Secure || c.Path != "/" {
		t.Fatalf("flow cookie over HTTPS: %+v", c)
	}
	if to.Query().Get("redirect_uri") != "https://hooks.example.com"+googleCallbackPath || to.Query().Has("hd") {
		t.Fatalf("redirect to Google: %s", to)
	}

	// Reached on another address, the sign-in starts over on PUBLIC_URL's,
	// where Google will come back and find the flow cookie.
	resp := e2.noFollow("/api/auth/google?next=%2Fadmin", "Host", "www.hooks.example.com")
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "https://hooks.example.com/api/auth/google?next=%2Fadmin" || flowCookie(resp) != nil {
		t.Fatalf("other host: %d %q %v", resp.StatusCode, resp.Header.Get("Location"), resp.Cookies())
	}
}

func TestGoogleJoin(t *testing.T) {
	e, g := setupGoogle(t)
	admin, adminCookie := e.firstAdmin("root@example.com", "correct horse battery")

	c := signedIn(t, e.googleSignIn(g, "New.Person@Gmail.com", ""), "/")
	g.mu.Lock()
	form := g.form
	g.mu.Unlock()
	if form.Get("client_secret") != "test-secret" || form.Get("client_id") != testClientID ||
		form.Get("grant_type") != "authorization_code" || form.Get("redirect_uri") != e.srv.URL+googleCallbackPath {
		t.Fatalf("token request: %v", form)
	}
	me := e.me(c)
	if me["email"] != "new.person@gmail.com" || me["role"] != "user" || me["name"] != "" || me["has_password"] != false {
		t.Fatalf("new account: %v", me)
	}
	if admin["has_password"] != true {
		t.Fatalf("a password account: %v", admin)
	}
	// The new account works like any other: it owns the URLs it creates.
	tok := e.ownedToken(c, `{}`)
	if tok["owner_id"] != me["id"] {
		t.Fatalf("owned URL: %v", tok)
	}
	// The admins see who joined, and how.
	rows := e.logRows(adminCookie, "action=user.join")
	if len(rows) != 1 || rows[0]["actor_id"] != me["id"] || rows[0]["target_id"] != me["id"] ||
		details(rows[0])["via"] != "google" || details(rows[0])["invited"] != false || details(rows[0])["role"] != "user" {
		t.Fatalf("log: %v", rows)
	}

	// Signing in again reaches the same account, and logs nothing more.
	c2 := signedIn(t, e.googleSignIn(g, "new.person@gmail.com", ""), "/")
	if e.me(c2)["id"] != me["id"] || len(e.logRows(adminCookie, "action=user.join")) != 1 {
		t.Fatal("a second sign-in must reach the same account")
	}

	// It has no password: a password login fails like a wrong one, and
	// there is no password to change.
	e.json(e.login("new.person@gmail.com", ""), 422, nil)
	e.json(e.login("new.person@gmail.com", "any password at all"), 401, nil)
	e.json(e.as(c, "POST", "/api/account/password", `{"current_password":"","new_password":"a brand new password"}`), 409, nil)
	// An admin's reset link gives it one.
	link := e.link(accounts.NewLink{Kind: accounts.LinkReset, UserID: me["id"].(string)})
	e.json(e.post("POST", "/api/links/redeem", map[string]string{"token": link, "password": "a brand new password"}), 200, nil)
	e.json(e.login("new.person@gmail.com", "a brand new password"), 200, nil)
	signedIn(t, e.googleSignIn(g, "new.person@gmail.com", ""), "/")
}

func TestGoogleNext(t *testing.T) {
	e, g := setupGoogle(t)
	id := "0193b2c4-5d6e-7f80-9a1b-2c3d4e5f6a7b"
	for next, where := range map[string]string{
		"":                       "/",
		"/admin":                 "/admin",
		id:                       "/#/" + id,
		strings.ToUpper(id):      "/#/" + id,
		"https://evil.example/":  "/",
		"//evil.example":         "/",
		"/admin/../evil":         "/",
		"/#/" + id:               "/",
		"javascript:alert(1)":    "/",
		"/account?next=//x.test": "/",
	} {
		signedIn(t, e.googleSignIn(g, "someone@example.com", next), where)
	}
	// A failure keeps where the person was going, in the form the login
	// page takes, for the next try.
	g.set(map[string]any{"email_verified": false})
	for next, where := range map[string]string{
		"/admin": "/login?error=google_unverified&next=%2Fadmin",
		id:       "/login?error=google_unverified&next=" + id,
		"":       "/login?error=google_unverified",
		"//x":    "/login?error=google_unverified",
	} {
		to, c := e.googleStart(g, next)
		resp := e.googleBack(c, url.Values{"state": {to.Query().Get("state")}, "code": {"good-code"}})
		if got := resp.Header.Get("Location"); got != where {
			t.Errorf("next %q: went to %q, want %q", next, got, where)
		}
	}
}

func TestGoogleLinksExistingAccount(t *testing.T) {
	e, g := setupGoogle(t, func(c *config.Config) { c.GoogleDomains = []string{"company.test"} })
	admin, adminCookie := e.firstAdmin("Root@Elsewhere.test", "correct horse battery")

	// An account that exists signs in whatever its domain, keeping its
	// role and its password.
	c := signedIn(t, e.googleSignIn(g, "root@elsewhere.test", "/admin"), "/admin")
	me := e.me(c)
	if me["id"] != admin["id"] || me["role"] != "admin" || me["has_password"] != true {
		t.Fatalf("linked account: %v", me)
	}
	e.json(e.login("root@elsewhere.test", "correct horse battery"), 200, nil)
	if rows := e.logRows(adminCookie, "action=user.join"); len(rows) != 0 {
		t.Fatalf("nobody joined: %v", rows)
	}

	// A personal Google account made with that address (no Workspace
	// behind it) only shows that someone once read a code sent there: it
	// gets nowhere near the account, nor anywhere else.
	g.set(map[string]any{"email": "root@elsewhere.test", "hd": nil})
	to, fc := e.googleStart(g, "")
	failedWith(t, e.googleBack(fc, url.Values{"state": {to.Query().Get("state")}, "code": {"good-code"}}), "google_address")
	g.set(map[string]any{"email": "stranger@elsewhere.test", "hd": nil})
	to, fc = e.googleStart(g, "")
	failedWith(t, e.googleBack(fc, url.Values{"state": {to.Query().Get("state")}, "code": {"good-code"}}), "google_address")

	// A disabled account does not.
	user, _ := e.invite("contractor@elsewhere.test", "correct horse battery")
	e.json(e.as(adminCookie, "PATCH", "/api/admin/users/"+user["id"].(string), `{"disabled":true}`), 200, nil)
	failedWith(t, e.googleSignIn(g, "contractor@elsewhere.test", ""), "disabled")
	e.json(e.as(adminCookie, "PATCH", "/api/admin/users/"+user["id"].(string), `{"disabled":false}`), 200, nil)
	signedIn(t, e.googleSignIn(g, "contractor@elsewhere.test", ""), "/")
}

func TestGoogleDomains(t *testing.T) {
	e, g := setupGoogle(t, func(c *config.Config) { c.GoogleDomains = []string{"company.test", "corp.test"} })
	_, adminCookie := e.firstAdmin("root@company.test", "correct horse battery")

	// Addresses in the allowed domains join; others don't.
	signedIn(t, e.googleSignIn(g, "ana@company.test", ""), "/")
	signedIn(t, e.googleSignIn(g, "bo@CORP.test", ""), "/")
	failedWith(t, e.googleSignIn(g, "eve@gmail.com", ""), "google_domain")
	failedWith(t, e.googleSignIn(g, "eve@sub.company.test", ""), "google_domain")
	failedWith(t, e.googleSignIn(g, "eve@company.test.evil", ""), "google_domain")
	// In a listed domain, but a personal Google account: not the company's.
	g.set(map[string]any{"email": "eve@company.test", "hd": nil})
	to, fc := e.googleStart(g, "")
	failedWith(t, e.googleBack(fc, url.Values{"state": {to.Query().Get("state")}, "code": {"good-code"}}), "google_address")
	var users struct {
		Total int `json:"total"`
	}
	e.json(e.as(adminCookie, "GET", "/api/admin/users", ""), 200, &users)
	if users.Total != 3 {
		t.Fatalf("users: %d", users.Total)
	}

	// An invitation lets its address in, with the invitation's role, and
	// is used up by it.
	token := e.link(accounts.NewLink{Kind: accounts.LinkInvite, Email: "Guest@Gmail.com", Role: accounts.RoleAdmin})
	c := signedIn(t, e.googleSignIn(g, "guest@gmail.com", ""), "/")
	if me := e.me(c); me["role"] != "admin" || me["has_password"] != false {
		t.Fatalf("invited account: %v", me)
	}
	e.json(e.post("POST", "/api/links/redeem", map[string]string{"token": token, "name": "Guest", "password": "correct horse battery"}), 404, nil)
	rows := e.logRows(adminCookie, "action=user.join")
	if len(rows) != 3 || details(rows[0])["invited"] != true || details(rows[0])["role"] != "admin" {
		t.Fatalf("log: %v", rows)
	}
	// An expired invitation doesn't.
	e.link(accounts.NewLink{Kind: accounts.LinkInvite, Email: "late@gmail.com", TTL: time.Millisecond})
	time.Sleep(5 * time.Millisecond)
	failedWith(t, e.googleSignIn(g, "late@gmail.com", ""), "google_domain")
}

func TestGoogleRefusals(t *testing.T) {
	e, g := setupGoogle(t)
	back := func(c *http.Cookie, q url.Values) *http.Response { return e.googleBack(c, q) }
	ok := func(to *url.URL) url.Values {
		return url.Values{"state": {to.Query().Get("state")}, "code": {"good-code"}}
	}

	// The state must match the browser's own flow: no cookie, another
	// sign-in's cookie, or a forged state all fail.
	to, c := e.googleStart(g, "")
	failedWith(t, back(nil, ok(to)), "google_expired")
	failedWith(t, back(c, url.Values{"state": {"forged"}, "code": {"good-code"}}), "google_expired")
	failedWith(t, back(c, url.Values{"code": {"good-code"}}), "google_expired")
	to2, _ := e.googleStart(g, "")
	failedWith(t, back(c, ok(to2)), "google_expired")
	failedWith(t, back(&http.Cookie{Name: "wg_google", Value: "garbage"}, ok(to)), "google_expired")

	// Saying no at Google, or a Google error.
	to, c = e.googleStart(g, "")
	failedWith(t, back(c, url.Values{"state": {to.Query().Get("state")}, "error": {"access_denied"}}), "google_cancelled")
	to, c = e.googleStart(g, "")
	failedWith(t, back(c, url.Values{"state": {to.Query().Get("state")}, "error": {"server_error"}}), "google_failed")

	// A code Google refuses, or one sent with another sign-in's verifier.
	to, c = e.googleStart(g, "")
	failedWith(t, back(c, url.Values{"state": {to.Query().Get("state")}, "code": {"bad-code"}}), "google_failed")
	to, c = e.googleStart(g, "")
	e.googleStart(g, "") // the fake Google now expects the other sign-in's verifier
	failedWith(t, back(c, ok(to)), "google_failed")
	g.mu.Lock()
	g.status = http.StatusBadRequest
	g.mu.Unlock()
	to, c = e.googleStart(g, "")
	failedWith(t, back(c, ok(to)), "google_failed")
	g.mu.Lock()
	g.status = 0
	g.mu.Unlock()

	// ID tokens that are not for this sign-in, or say too little.
	for name, tc := range map[string]struct {
		claims map[string]any
		want   string
	}{
		"another client":      {map[string]any{"aud": "other.apps.googleusercontent.com"}, "google_failed"},
		"no audience":         {map[string]any{"aud": nil}, "google_failed"},
		"another issuer":      {map[string]any{"iss": "https://evil.example"}, "google_failed"},
		"expired":             {map[string]any{"exp": time.Now().Add(-time.Minute).Unix()}, "google_failed"},
		"another sign-in":     {map[string]any{"nonce": "other"}, "google_failed"},
		"no nonce":            {map[string]any{"nonce": nil}, "google_failed"},
		"unverified email":    {map[string]any{"email_verified": false}, "google_unverified"},
		"no verification":     {map[string]any{"email_verified": nil}, "google_unverified"},
		"no email":            {map[string]any{"email": nil}, "google_unverified"},
		"not an email":        {map[string]any{"email": "not an email"}, "google_unverified"},
		"personal, elsewhere": {map[string]any{"hd": nil}, "google_address"},
		"personal, empty hd":  {map[string]any{"hd": ""}, "google_address"},
	} {
		tc.claims["email"] = firstNonEmpty(tc.claims, "email", "someone@example.com")
		g.set(tc.claims)
		to, c := e.googleStart(g, "")
		resp := back(c, ok(to))
		if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/login?error="+tc.want) || sessionCookie(resp) != nil {
			t.Errorf("%s: went to %q, want %s", name, loc, tc.want)
		}
	}
	// Gmail needs no Workspace domain: Google answers for it.
	g.set(map[string]any{"email": "someone@googlemail.com", "hd": nil})
	to, c = e.googleStart(g, "")
	signedIn(t, back(c, ok(to)), "/")
	// What Google may also send: several audiences, the bare issuer, and
	// email_verified as a string.
	g.set(map[string]any{"aud": []string{"other", testClientID}, "iss": "accounts.google.com", "email_verified": "true"})
	to, c = e.googleStart(g, "")
	signedIn(t, back(c, ok(to)), "/")

	// The flow cookie of an HTTPS instance must carry the __Host- prefix.
	e2, g2 := setupGoogle(t, func(c *config.Config) { c.PublicURL = "https://hooks.example.com" })
	to, c = e2.googleStart(g2, "", onHost...)
	failedWith(t, e2.googleBack(&http.Cookie{Name: "wg_google", Value: c.Value}, ok(to)), "google_expired")
	signedIn(t, e2.googleBack(c, ok(to)), "/")
}

// firstNonEmpty keeps a test's own value for key, which may be nil (drop
// the claim), else def.
func firstNonEmpty(claims map[string]any, key string, def any) any {
	if v, has := claims[key]; has {
		return v
	}
	return def
}

func TestGoogleRateLimit(t *testing.T) {
	e, g := setupGoogle(t, func(c *config.Config) { c.LoginLimit = 2 })
	freshWindow(t, time.Minute, 5*time.Second)
	for range 2 {
		signedIn(t, e.googleSignIn(g, "someone@example.com", ""), "/")
	}
	failedWith(t, e.googleSignIn(g, "someone@example.com", ""), "rate_limited")
}

func TestCaptureStripsGoogleCookie(t *testing.T) {
	e := setup(t)
	id := e.newToken(`{}`)["uuid"].(string)
	e.do("POST", "/"+id+"/x", nil, "Cookie", "a=1; wg_google=FLOW; __Host-wg_google=FLOW; b=2")
	for _, r := range e.list(id, "").Data {
		if got := r.Headers["cookie"]; len(got) != 1 || got[0] != "a=1; b=2" {
			t.Fatalf("cookies: %q", got)
		}
	}
}

func TestGoogleAccountInAdminList(t *testing.T) {
	e, g := setupGoogle(t)
	_, adminCookie := e.firstAdmin("root@example.com", "correct horse battery")
	signedIn(t, e.googleSignIn(g, "joiner@example.com", ""), "/")
	var page struct {
		Data []map[string]any `json:"data"`
	}
	e.json(e.as(adminCookie, "GET", "/api/admin/users", ""), 200, &page)
	got := map[string]any{}
	for _, u := range page.Data {
		got[u["email"].(string)] = u["has_password"]
	}
	if got["root@example.com"] != true || got["joiner@example.com"] != false {
		t.Fatalf("has_password: %v", got)
	}
}

// Someone who signed in with Google before the setup, likely the one doing
// it, has that account made the admin by the setup link.
func TestGoogleBeforeSetup(t *testing.T) {
	e, g := setupGoogle(t)
	early := signedIn(t, e.googleSignIn(g, "owner@company.test", ""), "/")
	id := e.me(early)["id"]

	_, c := e.firstAdmin("Owner@company.test", "correct horse battery")
	me := e.me(c)
	if me["id"] != id || me["role"] != "admin" || me["name"] != "Root" || me["has_password"] != true {
		t.Fatalf("after setup: %v", me)
	}
	if e.me(early) != nil {
		t.Fatal("the setup must end the account's earlier sessions")
	}
	signedIn(t, e.googleSignIn(g, "owner@company.test", "/admin"), "/admin")
	// Once there is an admin, setup links are gone, as before.
	token := e.link(accounts.NewLink{Kind: accounts.LinkSetup})
	e.json(e.post("POST", "/api/links/redeem", map[string]string{"token": token, "email": "x@company.test", "name": "X", "password": "correct horse battery"}), 404, nil)
}
