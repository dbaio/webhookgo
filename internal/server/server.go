// Package server implements the HTTP interface: webhook capture, the JSON
// API, the live event stream and the embedded web UI.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/dbaio/webhookgo/internal/accounts"
	"github.com/dbaio/webhookgo/internal/config"
	"github.com/dbaio/webhookgo/internal/hub"
	"github.com/dbaio/webhookgo/internal/store"
)

type Server struct {
	cfg      config.Config
	store    *store.Store
	accounts *accounts.Store // nil when ACCOUNTS is off
	tokens   *tokens         // every read and write of a URL goes through it
	hub      *hub.Hub
	ui       *staticFiles
	version  string
	shutdown chan struct{}
	heavy    chan struct{} // bounds concurrent searches and full-body lists

	streamRecheck time.Duration // how often a live stream re-checks its viewer's access (0 = never)
	proxyWarning  sync.Once     // TRUST_PROXY=false behind an HTTPS proxy, logged once
}

const maxHeavy = 4

// New builds the server. acc is nil when accounts are off: every account
// feature then stays out of the way.
func New(cfg config.Config, st *store.Store, acc *accounts.Store, h *hub.Hub, ui fs.FS, version string) (*Server, error) {
	files, err := loadStatic(ui)
	if err != nil {
		return nil, err
	}
	files.pages = map[string]bool{"/": true, "/abuse": true, "/privacy": true}
	s := &Server{
		cfg: cfg, store: st, accounts: acc, hub: h, ui: files, version: version,
		tokens:   &tokens{store: st, accounts: acc, maxURLs: cfg.MaxURLs},
		shutdown: make(chan struct{}),
		heavy:    make(chan struct{}, maxHeavy),
	}
	if acc != nil {
		for _, p := range []string{"/login", "/setup", "/invite", "/reset", "/account", "/admin"} {
			files.pages[p] = true
		}
		s.streamRecheck = streamRecheckEvery
	}
	return s, nil
}

// Close ends long-lived live streams so a graceful shutdown does not wait
// for browsers to disconnect.
func (s *Server) Close() { close(s.shutdown) }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", s.createToken)
	mux.HandleFunc("GET /token", s.withAccounts(s.listTokens))
	mux.HandleFunc("POST /token/{token}/claim", s.withAccounts(s.claimToken))
	mux.HandleFunc("POST /token/{token}/release", s.withAccounts(s.releaseToken))
	mux.HandleFunc("GET /token/{token}", s.getToken)
	mux.HandleFunc("PUT /token/{token}", s.updateToken)
	mux.HandleFunc("PATCH /token/{token}", s.updateToken)
	mux.HandleFunc("DELETE /token/{token}", s.deleteToken)
	mux.HandleFunc("PUT /token/{token}/cors/toggle", s.toggleCORS)
	mux.HandleFunc("GET /token/{token}/stream", s.stream)
	mux.HandleFunc("GET /token/{token}/requests", s.listRequests)
	mux.HandleFunc("GET /token/{token}/wait", s.wait)
	mux.HandleFunc("DELETE /token/{token}/request", s.deleteRequests)
	mux.HandleFunc("GET /token/{token}/request/latest", s.latestRequest)
	mux.HandleFunc("GET /token/{token}/request/{request}", s.getRequest)
	mux.HandleFunc("GET /token/{token}/request/{request}/raw", s.rawRequest)
	mux.HandleFunc("DELETE /token/{token}/request/{request}", s.deleteRequest)
	mux.HandleFunc("GET /api/config", s.appConfig)
	mux.HandleFunc("GET /api/abuse/urls/{token}", s.abuseCreator)
	mux.HandleFunc("GET /api/me", s.me)
	mux.HandleFunc("POST /api/login", s.withAccounts(s.login))
	mux.HandleFunc("POST /api/logout", s.withAccounts(s.logout))
	mux.HandleFunc("POST /api/links/inspect", s.withAccounts(s.inspectLink))
	mux.HandleFunc("POST /api/links/redeem", s.withAccounts(s.redeemLink))
	mux.HandleFunc("PATCH /api/account", s.withAccounts(s.updateAccount))
	mux.HandleFunc("POST /api/account/password", s.withAccounts(s.changePassword))
	mux.HandleFunc("POST /api/account/logout-others", s.withAccounts(s.logoutOthers))
	mux.HandleFunc("GET /api/keys", s.withAccounts(s.listKeys))
	mux.HandleFunc("POST /api/keys", s.withAccounts(s.createKey))
	mux.HandleFunc("DELETE /api/keys/{key}", s.withAccounts(s.revokeKey))
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/admin/stats", s.admin(s.adminStats))
	mux.HandleFunc("GET /api/admin/users", s.admin(s.adminListUsers))
	mux.HandleFunc("POST /api/admin/users", s.admin(s.adminInvite))
	mux.HandleFunc("PATCH /api/admin/users/{id}", s.admin(s.adminUpdateUser))
	mux.HandleFunc("POST /api/admin/users/{id}/reset", s.admin(s.adminResetLink))
	mux.HandleFunc("DELETE /api/admin/users/{id}", s.admin(s.adminDeleteUser))
	mux.HandleFunc("GET /api/admin/urls", s.admin(s.adminListURLs))
	mux.HandleFunc("GET /api/admin/ips", s.admin(s.adminTopIPs))
	mux.HandleFunc("GET /api/admin/urls/{id}", s.admin(s.adminLookupURL))
	mux.HandleFunc("DELETE /api/admin/urls/{id}", s.admin(s.adminDeleteURL))
	mux.HandleFunc("GET /api/admin/reports", s.admin(s.adminListReports))
	mux.HandleFunc("POST /api/admin/reports/resolve", s.admin(s.adminResolveReports))
	mux.HandleFunc("GET /api/admin/log", s.admin(s.adminLog))
	mux.HandleFunc("POST /api/abuse/reports", s.withAccounts(s.reportAbuse))
	mux.Handle("GET /", s.ui)

	// With accounts on, the API also refuses cross-origin writes and knows
	// who is signed in, by session or (on /token routes) by API key.
	// Captures never go through any of it.
	var api http.Handler = mux
	if s.accounts != nil {
		api = s.crossOrigin().Handler(s.authenticate(s.authenticateKey(mux)))
	}

	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Anything under /<uuid> is a webhook call, whatever the method.
		if id, rest, ok := splitTokenPath(r.URL.Path); ok {
			s.capture(w, r, id, rest)
			return
		}
		if p := r.URL.Path; strings.HasPrefix(p, "/token") || strings.HasPrefix(p, "/api/") {
			if ok, retry, err := s.store.Allow(r.Context(), "api", s.clientIP(r), s.cfg.APILimit); err != nil {
				internalError(w, r, err)
				return
			} else if !ok {
				tooManyRequests(w, retry)
				return
			}
			api.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
	return s.recoverer(s.logger(root))
}

// splitTokenPath matches /<uuid> and /<uuid>/<anything>.
func splitTokenPath(p string) (id, rest string, ok bool) {
	p = strings.TrimPrefix(p, "/")
	id, rest, _ = strings.Cut(p, "/")
	if !isUUID(id) {
		return "", "", false
	}
	return strings.ToLower(id), "/" + rest, true
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
	}
	return true
}

// clientIP returns the caller address. Behind TRUST_PROXY=n proxies it takes
// the n-th X-Forwarded-For entry from the right: proxies append to the header,
// so entries further left are whatever the client chose to send.
func (s *Server) clientIP(r *http.Request) string {
	if n := s.cfg.ProxyHops; n > 0 {
		var hops []string
		for _, line := range r.Header.Values("X-Forwarded-For") {
			for part := range strings.SplitSeq(line, ",") {
				if ip := strings.TrimSpace(part); ip != "" {
					hops = append(hops, ip)
				}
			}
		}
		if len(hops) > 0 {
			return hops[max(0, len(hops)-n)]
		}
		if ip := strings.TrimSpace(r.Header.Get("X-Real-Ip")); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// clientNetwork is the network the caller's address is in, /24 for IPv4 and
// /56 for IPv6 (the usual size of one customer's allocation), for limits
// that one address must not dodge by moving to the next.
func (s *Server) clientNetwork(r *http.Request) string {
	ip := s.clientIP(r)
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.Unmap()
	bits := 24
	if addr.Is6() {
		bits = 56
	}
	return netip.PrefixFrom(addr, bits).Masked().String()
}

func (s *Server) scheme(r *http.Request) string {
	if s.cfg.ProxyHops > 0 {
		if p := r.Header.Get("X-Forwarded-Proto"); p == "https" || p == "http" {
			return p
		}
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func (s *Server) host(r *http.Request) string {
	if s.cfg.ProxyHops > 0 {
		if h := r.Header.Get("X-Forwarded-Host"); h != "" {
			return h
		}
	}
	return r.Host
}

// baseURL is the public origin webhook URLs are built from.
func (s *Server) baseURL(r *http.Request) string {
	if s.cfg.PublicURL != "" {
		return s.cfg.PublicURL
	}
	return s.scheme(r) + "://" + s.host(r)
}

func (s *Server) appConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"app_name":       s.cfg.AppName,
		"version":        s.version,
		"public_url":     s.cfg.PublicURL,
		"max_requests":   s.cfg.MaxRequests,
		"max_body_size":  s.cfg.MaxBodySize,
		"max_delay":      s.cfg.MaxDelay,
		"token_ttl":      int(s.cfg.TokenTTL.Seconds()),
		"retention":      int(s.cfg.Retention.Seconds()),
		"abuse_contact":  s.cfg.AbuseContact,
		"accounts":       s.accounts != nil,
		"anonymous_urls": s.accounts == nil || s.cfg.AnonymousURLs,
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "error", "redis": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": s.version})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg, "status": status})
}

// internalError logs err and answers 500 (or 503 when Redis is unreachable).
func internalError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusServiceUnavailable, "storage unavailable, please retry")
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController reach Flush and deadlines.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (s *Server) logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		p := r.URL.Path
		if p == "/healthz" || strings.HasPrefix(p, "/assets/") {
			return
		}
		slog.Info("http",
			"method", r.Method,
			"path", p,
			"status", rec.status,
			"bytes", rec.bytes,
			"duration", time.Since(start).Round(time.Microsecond).String(),
			"ip", s.clientIP(r),
		)
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				slog.Error("panic", "err", v, "stack", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
