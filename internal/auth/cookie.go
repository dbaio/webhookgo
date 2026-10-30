package auth

import (
	"net/http"
	"time"
)

// The session cookie. On HTTPS the __Host- prefix makes the browser refuse
// the cookie unless it is Secure, has Path=/ and no Domain, so a cookie
// planted from elsewhere cannot replace it. Plain HTTP (localhost, a LAN)
// cannot use the prefix.
const (
	SessionCookie     = "wg_session"
	HostSessionCookie = "__Host-wg_session"
)

// SessionCookieFor returns the cookie that carries secret for ttl.
func SessionCookieFor(secret string, secure bool, ttl time.Duration) *http.Cookie {
	c := &http.Cookie{
		Name:     SessionCookie,
		Value:    secret,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
	if secure {
		c.Name, c.Secure = HostSessionCookie, true
	}
	return c
}

// ClearSessionCookies expires both forms of the cookie.
func ClearSessionCookies(w http.ResponseWriter) {
	for _, c := range []*http.Cookie{
		{Name: HostSessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode},
		{Name: SessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode},
	} {
		http.SetCookie(w, c)
	}
}

// SessionSecret returns the secret from the request's session cookie, or "".
// Over HTTPS (secure) only the __Host- cookie counts: a plain one may have
// been planted over HTTP or by a sibling domain, which the prefix exists to
// rule out. Otherwise the __Host- one wins over the plain one.
func SessionSecret(r *http.Request, secure bool) string {
	names := []string{HostSessionCookie, SessionCookie}
	if secure {
		names = names[:1]
	}
	for _, name := range names {
		if c, err := r.Cookie(name); err == nil && c.Value != "" {
			return c.Value
		}
	}
	return ""
}
