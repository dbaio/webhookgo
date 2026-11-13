package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dbaio/webhookgo/internal/accounts"
	"github.com/dbaio/webhookgo/internal/auth"
	"github.com/dbaio/webhookgo/internal/store"
)

const (
	maxJSONBody = 16 << 10
	maxEmail    = 254
	maxName     = 100

	errBadLogin    = "Invalid email or password"
	errBadLink     = "This link is invalid, used or expired"
	errNotSignedIn = "Not signed in"
)

var emailPattern = regexp.MustCompile(`^[^\s@]+@[^\s@]+$`)

type ctxKey int

const sessionKey ctxKey = iota

// session is the signed-in user and the secret of the cookie that carries it.
type session struct {
	user   *accounts.User
	secret string
}

func sessionFrom(r *http.Request) *session {
	s, _ := r.Context().Value(sessionKey).(*session)
	return s
}

// userFrom returns the signed-in user, or nil.
func userFrom(r *http.Request) *accounts.User {
	if s := sessionFrom(r); s != nil {
		return s.user
	}
	return nil
}

// sessionSecret returns the secret of the request's session cookie, or "".
// Only the cookie signIn sets on this instance counts: on HTTPS that is the
// __Host- one, so a plain wg_session planted over HTTP or by a sibling
// domain cannot sign anyone in.
func (s *Server) sessionSecret(r *http.Request) string {
	return auth.SessionSecret(r, s.https(r))
}

// authenticate resolves the session cookie into the user, for the API
// routes. A cookie that no longer matches a session is cleared.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret := s.sessionSecret(r)
		if secret == "" {
			next.ServeHTTP(w, r)
			return
		}
		u, err := s.accounts.SessionUser(r.Context(), secret, s.cfg.SessionTTL)
		if errors.Is(err, accounts.ErrNotFound) {
			auth.ClearSessionCookies(w)
			next.ServeHTTP(w, r)
			return
		}
		if err != nil {
			internalError(w, r, err)
			return
		}
		ctx := context.WithValue(r.Context(), sessionKey, &session{user: u, secret: secret})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// crossOrigin rejects state-changing requests from other origins, based on
// the Sec-Fetch-Site and Origin headers browsers send. Requests without
// them (curl, scripts) pass.
func (s *Server) crossOrigin() *http.CrossOriginProtection {
	c := http.NewCrossOriginProtection()
	if s.cfg.PublicURL != "" {
		u, err := url.Parse(s.cfg.PublicURL)
		if err == nil {
			err = c.AddTrustedOrigin(u.Scheme + "://" + u.Host)
		}
		if err != nil {
			slog.Warn("PUBLIC_URL is not trusted for cross-origin checks", "err", err)
		}
	}
	c.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusForbidden, "cross-origin request rejected")
	}))
	return c
}

// withAccounts hides a route when accounts are off.
func (s *Server) withAccounts(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.accounts == nil {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		h(w, r)
	}
}

// requireUser answers 401 and returns nil when nobody is signed in.
func requireUser(w http.ResponseWriter, r *http.Request) *session {
	sess := sessionFrom(r)
	if sess == nil {
		writeError(w, http.StatusUnauthorized, errNotSignedIn)
	}
	return sess
}

// readJSON reads a small JSON body into v, answering the error itself.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	err := json.NewDecoder(r.Body).Decode(v)
	var tooBig *http.MaxBytesError
	switch {
	case err == nil:
		return true
	case errors.As(err, &tooBig):
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
	default:
		writeError(w, http.StatusUnprocessableEntity, "invalid JSON: "+err.Error())
	}
	return false
}

func validEmail(email string) error {
	switch {
	case email == "":
		return errors.New("email is required")
	case len(email) > maxEmail:
		return errors.New("email must be at most 254 characters")
	case !emailPattern.MatchString(email):
		return errors.New("email is not valid")
	}
	return nil
}

func validName(name string) error {
	switch {
	case name == "":
		return errors.New("name is required")
	case utf8.RuneCountInString(name) > maxName:
		return errors.New("name must be at most 100 characters")
	}
	return nil
}

// https reports whether the instance is served over HTTPS, which decides
// the session cookie's name and Secure flag.
func (s *Server) https(r *http.Request) bool {
	return strings.HasPrefix(s.cfg.PublicURL, "https://") || s.scheme(r) == "https"
}

// signIn sends the session cookie.
func (s *Server) signIn(w http.ResponseWriter, r *http.Request, secret string) {
	if r.Header.Get("X-Forwarded-Proto") == "https" && s.cfg.ProxyHops == 0 && !strings.HasPrefix(s.cfg.PublicURL, "https://") {
		s.proxyWarning.Do(func() {
			slog.Warn("the request came over HTTPS through a proxy but TRUST_PROXY=false; session cookies are sent without Secure. Set TRUST_PROXY or PUBLIC_URL.")
		})
	}
	http.SetCookie(w, auth.SessionCookieFor(secret, s.https(r), s.cfg.SessionTTL))
}

// allowLogin applies LOGIN_RATE_LIMIT per client IP.
func (s *Server) allowLogin(w http.ResponseWriter, r *http.Request) bool {
	ok, retry, err := s.store.Allow(r.Context(), "login", s.clientIP(r), s.cfg.LoginLimit)
	if err != nil {
		internalError(w, r, err)
		return false
	}
	if !ok {
		tooManyRequests(w, retry)
	}
	return ok
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"user": userFrom(r)})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.allowLogin(w, r) {
		return
	}
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	email := strings.TrimSpace(in.Email)
	if email == "" || in.Password == "" {
		writeError(w, http.StatusUnprocessableEntity, "email and password are required")
		return
	}
	// A higher limit per account and client network: one network cannot
	// brute-force an account faster than this, and attempts from elsewhere
	// cannot lock its owner out of it from their own.
	key := strings.ToLower(email) + "|" + s.clientNetwork(r)
	if ok, retry, err := s.store.Allow(r.Context(), "login-email", key, 3*s.cfg.LoginLimit); err != nil {
		internalError(w, r, err)
		return
	} else if !ok {
		tooManyRequests(w, retry)
		return
	}

	u, err := s.accounts.UserByEmail(r.Context(), email)
	if errors.Is(err, accounts.ErrNotFound) || err == nil && !u.HasPassword {
		auth.VerifyDummy(in.Password) // same cost as a real check
		writeError(w, http.StatusUnauthorized, errBadLogin)
		return
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	ok, err := auth.VerifyPassword(u.PasswordHash, in.Password)
	if err != nil {
		internalError(w, r, err)
		return
	}
	if !ok {
		writeError(w, http.StatusUnauthorized, errBadLogin)
		return
	}
	if u.Disabled() {
		writeError(w, http.StatusForbidden, "This account is disabled")
		return
	}
	secret, err := s.accounts.CreateSession(r.Context(), u, s.clientIP(r), r.UserAgent(), s.cfg.SessionTTL)
	if err != nil {
		internalError(w, r, err)
		return
	}
	s.signIn(w, r, secret)
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if secret := s.sessionSecret(r); secret != "" {
		if err := s.accounts.DeleteSession(r.Context(), secret); err != nil {
			internalError(w, r, err)
			return
		}
	}
	auth.ClearSessionCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) inspectLink(w http.ResponseWriter, r *http.Request) {
	if !s.allowLogin(w, r) {
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	l, err := s.accounts.InspectLink(r.Context(), strings.TrimSpace(in.Token))
	if errors.Is(err, accounts.ErrNotFound) {
		writeError(w, http.StatusNotFound, errBadLink)
		return
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"kind": l.Kind, "email": l.Email, "role": l.Role})
}

func (s *Server) redeemLink(w http.ResponseWriter, r *http.Request) {
	if !s.allowLogin(w, r) {
		return
	}
	var in struct {
		Token    string `json:"token"`
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	token := strings.TrimSpace(in.Token)
	l, err := s.accounts.InspectLink(r.Context(), token)
	if errors.Is(err, accounts.ErrNotFound) {
		writeError(w, http.StatusNotFound, errBadLink)
		return
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	email, name := strings.TrimSpace(in.Email), strings.TrimSpace(in.Name)
	switch l.Kind {
	case accounts.LinkSetup:
		err = validEmail(email)
		if err == nil {
			err = validName(name)
		}
	case accounts.LinkInvite:
		err = validName(name)
	}
	if err == nil {
		err = auth.CheckPassword(in.Password)
	}
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		internalError(w, r, err)
		return
	}
	u, secret, err := s.accounts.Redeem(r.Context(), token, accounts.Redeem{
		Email: email, Name: name, PasswordHash: hash,
		IP: s.clientIP(r), UserAgent: r.UserAgent(), SessionTTL: s.cfg.SessionTTL,
	})
	switch {
	case errors.Is(err, accounts.ErrEmailTaken):
		writeError(w, http.StatusConflict, "That email is already registered")
		return
	case errors.Is(err, accounts.ErrNotFound):
		writeError(w, http.StatusNotFound, errBadLink)
		return
	case err != nil:
		internalError(w, r, err)
		return
	}
	s.signIn(w, r, secret)
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}

func (s *Server) updateAccount(w http.ResponseWriter, r *http.Request) {
	sess := requireUser(w, r)
	if sess == nil {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	name := strings.TrimSpace(in.Name)
	if err := validName(name); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	u, err := s.accounts.UpdateName(r.Context(), sess.user.ID, name)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	sess := requireUser(w, r)
	if sess == nil {
		return
	}
	var in struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if !sess.user.HasPassword {
		writeError(w, http.StatusConflict, "This account has no password: it signs in with Google")
		return
	}
	if err := auth.CheckPassword(in.New); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	ok, err := auth.VerifyPassword(sess.user.PasswordHash, in.Current)
	if err != nil {
		internalError(w, r, err)
		return
	}
	if !ok {
		writeError(w, http.StatusForbidden, "Current password is incorrect")
		return
	}
	hash, err := auth.HashPassword(in.New)
	if err != nil {
		internalError(w, r, err)
		return
	}
	if err := s.accounts.SetPassword(r.Context(), sess.user.ID, hash, sess.secret); err != nil {
		internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) logoutOthers(w http.ResponseWriter, r *http.Request) {
	sess := requireUser(w, r)
	if sess == nil {
		return
	}
	if err := s.accounts.DeleteOtherSessions(r.Context(), sess.user.ID, sess.secret); err != nil {
		internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Private URLs -----------------------------------------------------------

// tokenItem is a URL in the signed-in user's list.
type tokenItem struct {
	*tokenView
	LatestRequestAt *time.Time `json:"latest_request_at"`
}

// listTokens is GET /token: the user's URLs, newest first, with
// ?page, ?per_page and ?q (a search on the description).
func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if u == nil {
		writeError(w, http.StatusUnauthorized, "Sign in to list your URLs")
		return
	}
	q := r.URL.Query()
	page := min(max(atoiDefault(q.Get("page"), 1), 1), 1_000_000)
	perPage := min(max(atoiDefault(q.Get("per_page"), 20), 1), 100)
	tokens, total, err := s.accounts.ListURLs(r.Context(), u.ID, strings.TrimSpace(q.Get("q")), page, perPage)
	if err != nil {
		internalError(w, r, err)
		return
	}
	ids := make([]string, len(tokens))
	for i, t := range tokens {
		ids[i] = t.ID
	}
	activity, err := s.store.Activities(r.Context(), ids)
	if err != nil {
		internalError(w, r, err)
		return
	}
	items := make([]tokenItem, len(tokens))
	for i, t := range tokens {
		items[i] = tokenItem{tokenView: s.view(r, t, activity[i].Requests), LatestRequestAt: activity[i].LastRequestAt}
	}
	writeJSON(w, http.StatusOK, pageOf(items, len(items), total, page, perPage))
}

// claimToken is POST /token/{token}/claim: with the key given out when an
// anonymous URL was created, the signed-in user makes it theirs.
func (s *Server) claimToken(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if u == nil {
		writeError(w, http.StatusUnauthorized, errNotSignedIn)
		return
	}
	id := strings.ToLower(r.PathValue("token"))
	if !isUUID(id) {
		writeError(w, http.StatusNotFound, "URL not found")
		return
	}
	var in struct {
		ClaimKey string `json:"claim_key"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	t, err := s.tokens.read(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) { // deleted ones included: there is nothing to claim
		writeError(w, http.StatusNotFound, "URL not found or expired")
		return
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	if t.Owned() {
		writeError(w, http.StatusConflict, "This URL already belongs to an account")
		return
	}
	hash := claimHash(strings.TrimSpace(in.ClaimKey))
	if t.ClaimHash == "" || subtle.ConstantTimeCompare([]byte(hash), []byte(t.ClaimHash)) != 1 {
		writeError(w, http.StatusForbidden, "Wrong claim key")
		return
	}
	t.OwnerID, t.ClaimHash, t.UpdatedAt = u.ID, "", time.Now().UTC()
	switch err := s.tokens.claim(r.Context(), t); {
	case errors.Is(err, accounts.ErrTooManyURLs):
		s.tooManyURLs(w)
		return
	case errors.Is(err, accounts.ErrURLOwned): // claimed twice at once
		writeError(w, http.StatusConflict, "This URL already belongs to an account")
		return
	case err != nil:
		internalError(w, r, err)
		return
	}
	// No token data: anonymous viewers may still be listening.
	s.publish(r.Context(), t.ID, event{Type: "token.claimed"})
	n, err := s.store.CountRequests(r.Context(), t.ID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.view(r, t, n))
}

// releaseToken is POST /token/{token}/release: the owner (or an admin) makes
// a private URL anonymous again. It expires like any other from here on, and
// the answer carries a new claim key, given out this once.
func (s *Server) releaseToken(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadToken(w, r)
	if !ok {
		return
	}
	if !t.Owned() {
		writeError(w, http.StatusConflict, "This URL is already anonymous")
		return
	}
	key, hash := newClaimKey()
	t.OwnerID, t.ClaimHash, t.UpdatedAt = "", hash, time.Now().UTC()
	if err := s.tokens.release(r.Context(), t); err != nil {
		internalError(w, r, err)
		return
	}
	s.publish(r.Context(), t.ID, event{Type: "token.released"})
	n, err := s.store.CountRequests(r.Context(), t.ID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	v := s.view(r, t, n)
	v.ClaimKey = key
	writeJSON(w, http.StatusOK, v)
}
