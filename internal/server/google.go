package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/dbaio/webhookgo/internal/accounts"
)

// Sign in with Google: the OpenID Connect authorization code flow with PKCE,
// asking for the email address only (scopes openid and email). The address
// is what ties the Google account to an account here.

const (
	googleCallbackPath = "/api/auth/google/callback"
	googleFlowTTL      = 10 * time.Minute

	// The cookie that carries a sign-in through Google and back: its state,
	// PKCE verifier and nonce, and where to go afterwards. Lax, as Google
	// sends the browser back with a top-level GET from its own site.
	googleCookie     = "wg_google"
	hostGoogleCookie = "__Host-wg_google"
)

// Why a sign-in through Google failed, for the login page (?error=).
const (
	googleCancelled  = "google_cancelled"  // the person said no at Google
	googleExpired    = "google_expired"    // no matching flow cookie: too slow, or another tab
	googleFailed     = "google_failed"     // Google or the database did not answer as expected
	googleUnverified = "google_unverified" // Google has not verified the address
	googleNotManaged = "google_address"    // neither Gmail nor Google Workspace: Google can't vouch for it
	googleNotAllowed = "google_domain"     // no account, no invitation, and not an allowed domain
	googleDisabled   = "disabled"          // the account is disabled
	googleRateLimit  = "rate_limited"      // LOGIN_RATE_LIMIT
)

type googleOAuth struct {
	clientID, clientSecret string
	domains                []string // who may join; empty = anyone
	authURL, tokenURL      string   // Google's, or a test server's
	client                 *http.Client
}

func newGoogle(clientID, clientSecret string, domains []string) *googleOAuth {
	return &googleOAuth{
		clientID: clientID, clientSecret: clientSecret, domains: domains,
		authURL:  "https://accounts.google.com/o/oauth2/v2/auth",
		tokenURL: "https://oauth2.googleapis.com/token",
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// mayJoin reports whether an address without an account may create one.
func (g *googleOAuth) mayJoin(email string) bool {
	if len(g.domains) == 0 {
		return true
	}
	_, domain, _ := strings.Cut(strings.ToLower(email), "@")
	return slices.Contains(g.domains, domain)
}

// withGoogle hides a route when sign-in with Google is off.
func (s *Server) withGoogle(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.google == nil {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		h(w, r)
	}
}

func (s *Server) googleRedirectURI(r *http.Request) string {
	return s.baseURL(r) + googleCallbackPath
}

// googleStart is GET /api/auth/google: it sends the browser to Google.
// ?next= is where to land afterwards: a URL's ID or /admin, else home.
func (s *Server) googleStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	// Google comes back to PUBLIC_URL, so the flow cookie must be set there:
	// reached on another address, start over from PUBLIC_URL.
	if pub, err := url.Parse(s.cfg.PublicURL); s.cfg.PublicURL != "" && err == nil && !strings.EqualFold(pub.Host, s.host(r)) {
		http.Redirect(w, r, s.cfg.PublicURL+r.URL.RequestURI(), http.StatusFound)
		return
	}
	state, verifier, nonce := randomToken(), randomToken(), randomToken()
	flow := strings.Join([]string{state, verifier, nonce, nextOf(r.URL.Query().Get("next"))}, ".")
	http.SetCookie(w, s.googleFlowCookie(r, flow, int(googleFlowTTL.Seconds())))

	challenge := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"client_id":             {s.google.clientID},
		"redirect_uri":          {s.googleRedirectURI(r)},
		"response_type":         {"code"},
		"scope":                 {"openid email"},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
		"prompt":                {"select_account"}, // a shared computer may have someone else's account signed in
	}
	http.Redirect(w, r, s.google.authURL+"?"+q.Encode(), http.StatusFound)
}

// googleCallback is GET /api/auth/google/callback, where Google sends the
// browser back. It signs the person in and goes on to where they started,
// or back to the login page with ?error= saying why not.
func (s *Server) googleCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	flow := s.googleFlow(r)
	// The flow cookie is good for one try, whatever happens next.
	http.SetCookie(w, s.googleFlowCookie(r, "", -1))

	parts := strings.Split(flow, ".")
	next := ""
	if len(parts) == 4 {
		next = parts[3]
	}
	fail := func(code string) {
		u := "/login?error=" + code
		if next != "" {
			u += "&next=" + url.QueryEscape(loginNext(next))
		}
		http.Redirect(w, r, u, http.StatusSeeOther)
	}

	q := r.URL.Query()
	if len(parts) != 4 || q.Get("state") == "" || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(parts[0])) != 1 {
		fail(googleExpired)
		return
	}
	if e := q.Get("error"); e != "" {
		if e == "access_denied" {
			fail(googleCancelled)
		} else {
			slog.Warn("google sign-in refused by Google", "error", e[:min(len(e), 64)])
			fail(googleFailed)
		}
		return
	}
	if ok, _, err := s.store.Allow(r.Context(), "login", s.clientIP(r), s.cfg.LoginLimit); err != nil {
		slog.Error("google sign-in", "err", err)
		fail(googleFailed)
		return
	} else if !ok {
		fail(googleRateLimit)
		return
	}

	claims, err := s.google.exchange(r.Context(), q.Get("code"), parts[1], s.googleRedirectURI(r))
	if err == nil {
		err = claims.check(s.google.clientID, parts[2], time.Now())
	}
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			slog.Warn("google sign-in failed", "err", err)
		}
		fail(googleFailed)
		return
	}
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	if !bool(claims.EmailVerified) || validEmail(email) != nil {
		fail(googleUnverified)
		return
	}
	// The address is what ties the Google account to an account here, so it
	// must be one Google answers for: Gmail, or a Workspace domain (hd). For
	// any other, email_verified only means that someone read a code sent
	// there once, maybe long ago (Google's own advice).
	if !claims.managed(email) {
		fail(googleNotManaged)
		return
	}

	u, secret, joined, err := s.accounts.SignInExternal(r.Context(), accounts.ExternalSignIn{
		Email: email, Provider: "google", MayJoin: s.google.mayJoin(email),
		IP: s.clientIP(r), UserAgent: r.UserAgent(), SessionTTL: s.cfg.SessionTTL,
	})
	switch {
	case errors.Is(err, accounts.ErrDisabled):
		fail(googleDisabled)
		return
	case errors.Is(err, accounts.ErrNotInvited):
		fail(googleNotAllowed)
		return
	case err != nil:
		slog.Error("google sign-in", "err", err)
		fail(googleFailed)
		return
	}
	if joined {
		slog.Info("account created through Google", "user", u.ID, "role", u.Role)
	}
	s.signIn(w, r, secret)
	http.Redirect(w, r, nextParam(next), http.StatusSeeOther)
}

// googleFlowCookie sets (maxAge > 0) or clears the flow cookie. Like the
// session cookie, it takes the __Host- prefix over HTTPS.
func (s *Server) googleFlowCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	c := &http.Cookie{Name: googleCookie, Value: value, Path: "/", MaxAge: maxAge, HttpOnly: true, SameSite: http.SameSiteLaxMode}
	if s.https(r) {
		c.Name, c.Secure = hostGoogleCookie, true
	}
	return c
}

func (s *Server) googleFlow(r *http.Request) string {
	names := []string{hostGoogleCookie, googleCookie}
	if s.https(r) {
		names = names[:1] // as for the session: a plain one may have been planted
	}
	for _, name := range names {
		if c, err := r.Cookie(name); err == nil && c.Value != "" {
			return c.Value
		}
	}
	return ""
}

// nextOf keeps from ?next= only the places a sign-in may lead back to, in
// the short form the flow cookie carries: a URL's ID, "admin" or "".
func nextOf(next string) string {
	switch {
	case isUUID(next):
		return strings.ToLower(next)
	case next == "/admin":
		return "admin"
	}
	return ""
}

// loginNext turns a short form from nextOf back into the login page's
// ?next=, for another try: a URL's ID or /admin.
func loginNext(next string) string {
	if next == "admin" {
		return "/admin"
	}
	return next
}

// nextParam is where a short form from nextOf leads: the page to land on
// after signing in.
func nextParam(next string) string {
	switch {
	case next == "admin":
		return "/admin"
	case isUUID(next):
		return "/#/" + next
	}
	return "/"
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// idClaims are the parts of Google's ID token that matter here.
type idClaims struct {
	Issuer        string    `json:"iss"`
	Audience      audience  `json:"aud"`
	Expires       int64     `json:"exp"`
	Nonce         string    `json:"nonce"`
	Email         string    `json:"email"`
	EmailVerified looseBool `json:"email_verified"`
	HostedDomain  string    `json:"hd"` // the Google Workspace domain; "" for a personal account
}

// managed reports whether Google is the authority for the address: a Gmail
// one, or one of a Google Workspace account.
func (c *idClaims) managed(email string) bool {
	_, domain, _ := strings.Cut(email, "@")
	return c.HostedDomain != "" || domain == "gmail.com" || domain == "googlemail.com"
}

// exchange trades the code for the ID token. The token comes straight from
// Google over TLS, in answer to a request authenticated with the client
// secret, so, as Google's documentation says, its signature need not be
// checked; its claims still are (check).
func (g *googleOAuth) exchange(ctx context.Context, code, verifier, redirectURI string) (*idClaims, error) {
	if code == "" {
		return nil, errors.New("no code in the callback")
	}
	form := url.Values{
		"code":          {code},
		"client_id":     {g.clientID},
		"client_secret": {g.clientSecret},
		"redirect_uri":  {redirectURI},
		"grant_type":    {"authorization_code"},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	var out struct {
		IDToken     string `json:"id_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("token endpoint: %s: %w", resp.Status, err)
	}
	if resp.StatusCode != http.StatusOK || out.IDToken == "" {
		return nil, fmt.Errorf("token endpoint: %s: %s %s", resp.Status, out.Error, out.Description)
	}
	parts := strings.Split(out.IDToken, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed ID token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil, fmt.Errorf("malformed ID token: %w", err)
	}
	var c idClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, fmt.Errorf("malformed ID token: %w", err)
	}
	return &c, nil
}

// check validates the claims of an ID token meant for clientID, issued for
// the sign-in that sent nonce.
func (c *idClaims) check(clientID, nonce string, now time.Time) error {
	switch {
	case c.Issuer != "https://accounts.google.com" && c.Issuer != "accounts.google.com":
		return fmt.Errorf("ID token from %q", c.Issuer)
	case !slices.Contains(c.Audience, clientID):
		return errors.New("ID token for another client")
	case now.Unix() >= c.Expires:
		return errors.New("ID token expired")
	case subtle.ConstantTimeCompare([]byte(c.Nonce), []byte(nonce)) != 1:
		return errors.New("ID token for another sign-in")
	}
	return nil
}

// audience is the aud claim: one client ID, or a list of them.
type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*a = audience{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

// looseBool reads true and "true" alike.
type looseBool bool

func (l *looseBool) UnmarshalJSON(b []byte) error {
	switch string(b) {
	case "true", `"true"`:
		*l = true
	default:
		*l = false
	}
	return nil
}
