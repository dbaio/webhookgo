package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/dbaio/webhookgo/internal/accounts"
	"github.com/dbaio/webhookgo/internal/signature"
	"github.com/dbaio/webhookgo/internal/store"
)

const (
	maxTokenPayload    = 4 << 20
	maxResponseHeaders = 50
	maxDescription     = 200
)

// event is published on a URL's channel and relayed to live clients.
type event struct {
	Type    string         `json:"type"`
	Request *store.Summary `json:"request,omitempty"`
	ID      string         `json:"id,omitempty"`
	Total   int64          `json:"total"`
	Evicted []string       `json:"evicted,omitempty"`
	Token   *tokenView     `json:"token,omitempty"`
}

func (s *Server) publish(ctx context.Context, tokenID string, e event) {
	b, err := json.Marshal(e)
	if err == nil {
		err = s.store.Publish(context.WithoutCancel(ctx), tokenID, b)
	}
	if err != nil {
		slog.Warn("publish event failed", "token", tokenID, "type", e.Type, "err", err)
	}
}

type tokenView struct {
	*store.Token
	Signature    *signatureView `json:"signature,omitempty"`  // replaces the token's, which holds the secret
	ClaimHash    string         `json:"claim_hash,omitempty"` // always empty: the hash never leaves Redis
	ClaimKey     string         `json:"claim_key,omitempty"`  // only when an anonymous URL is created or released, with accounts on
	URL          string         `json:"url"`
	RequestCount int64          `json:"request_count"`
	ExpiresAt    *time.Time     `json:"expires_at"` // null for a URL that belongs to an account
}

// signatureView is a URL's signature settings without the secret.
type signatureView struct {
	signature.Config
	HasSecret bool `json:"has_secret"`
}

func (s *Server) view(r *http.Request, t *store.Token, count int64) *tokenView {
	v := &tokenView{
		Token:        t,
		URL:          s.baseURL(r) + "/" + t.ID,
		RequestCount: count,
	}
	if !t.Owned() {
		expires := time.Now().UTC().Add(s.store.TTL()).Truncate(time.Second)
		v.ExpiresAt = &expires
	}
	if t.Signature != nil {
		v.Signature = &signatureView{Config: *t.Signature, HasSecret: t.Signature.Secret != ""}
		v.Signature.Secret = ""
	}
	return v
}

// loadToken resolves {token} from the path, answering 404 itself when the URL
// does not exist. It is also where a private URL is kept from everyone but
// its owner and the admins (403), for every /token/{token}… route.
func (s *Server) loadToken(w http.ResponseWriter, r *http.Request) (*store.Token, bool) {
	id := strings.ToLower(r.PathValue("token"))
	if !isUUID(id) {
		writeError(w, http.StatusNotFound, "URL not found")
		return nil, false
	}
	u := userFrom(r)
	t, err := s.tokens.read(r.Context(), id)
	if d, ok := errors.AsType[*store.DeletedError](err); ok && (!d.Owned || isAdmin(u)) {
		writeJSON(w, http.StatusGone, map[string]any{"error": "URL deleted", "status": http.StatusGone, "deleted_at": d.At})
		return nil, false
	}
	if errors.Is(err, store.ErrNotFound) {
		// Including a deleted private URL, which leaves no public trace.
		writeError(w, http.StatusNotFound, "URL not found or expired")
		return nil, false
	}
	if err != nil {
		internalError(w, r, err)
		return nil, false
	}
	if t.Owned() {
		switch {
		case u == nil, u.ID != t.OwnerID && !isAdmin(u):
			writeError(w, http.StatusForbidden, "URL is private")
			return nil, false
		case u.ID != t.OwnerID:
			s.noteAdminAccess(r, u, t)
		}
	} else if isAdmin(u) { // public, but the panel lists them all: opening one is logged too
		s.noteAdminAccess(r, u, t)
	}
	return t, true
}

// --- URLs -------------------------------------------------------------------

// flexInt accepts 200 as well as "200" (HTML forms and the legacy UI send
// strings); an empty string means "unset".
type flexInt struct {
	set bool
	v   int
}

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("%q is not a number", s)
	}
	f.set, f.v = true, n
	return nil
}

type flexBool struct {
	set bool
	v   bool
}

func (f *flexBool) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	v, err := strconv.ParseBool(s)
	if err != nil {
		return fmt.Errorf("%q is not a boolean", s)
	}
	f.set, f.v = true, v
	return nil
}

type tokenInput struct {
	DefaultStatus      flexInt         `json:"default_status"`
	DefaultContent     *string         `json:"default_content"`
	DefaultContentType *string         `json:"default_content_type"`
	Timeout            flexInt         `json:"timeout"`
	CORS               flexBool        `json:"cors"`
	Description        *string         `json:"description"`
	ResponseHeaders    *[]store.Header `json:"response_headers"`
	Signature          signatureField  `json:"signature"`
}

// signatureField is the signature settings of a URL update: null turns
// verification off, and fields left out keep their current value.
type signatureField struct {
	set bool
	v   *signatureInput
}

type signatureInput struct {
	Scheme    *string  `json:"scheme"` // "" turns verification off
	Secret    *string  `json:"secret"` // "" keeps the current one
	Header    *string  `json:"header"`
	Algorithm *string  `json:"algorithm"`
	Encoding  *string  `json:"encoding"`
	Prefix    *string  `json:"prefix"`
	Tolerance flexInt  `json:"tolerance"`
	Reject    flexBool `json:"reject"`
}

func (f *signatureField) UnmarshalJSON(b []byte) error {
	f.set = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return nil
	}
	f.v = new(signatureInput)
	return json.Unmarshal(b, f.v)
}

// decodeTokenInput reads JSON or urlencoded/multipart form bodies.
func decodeTokenInput(w http.ResponseWriter, r *http.Request) (tokenInput, error) {
	var in tokenInput
	r.Body = http.MaxBytesReader(w, r.Body, maxTokenPayload)
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch mt {
	case "application/x-www-form-urlencoded", "multipart/form-data":
		if err := r.ParseMultipartForm(maxTokenPayload); err != nil && !errors.Is(err, http.ErrNotMultipart) {
			return in, err
		}
		m := map[string]string{}
		for k, v := range r.PostForm {
			if len(v) > 0 {
				m[k] = v[0]
			}
		}
		b, _ := json.Marshal(m)
		return in, json.Unmarshal(b, &in)
	default:
		b, err := io.ReadAll(r.Body)
		if err != nil {
			return in, err
		}
		if len(strings.TrimSpace(string(b))) == 0 {
			return in, nil
		}
		if err := json.Unmarshal(b, &in); err != nil {
			return in, fmt.Errorf("invalid JSON: %w", err)
		}
		return in, nil
	}
}

func (s *Server) apply(t *store.Token, in tokenInput) error {
	if in.DefaultStatus.set {
		if !validStatus(in.DefaultStatus.v) {
			return errors.New("default_status must be between 200 and 599")
		}
		t.DefaultStatus = in.DefaultStatus.v
	}
	if in.DefaultContent != nil {
		if int64(len(*in.DefaultContent)) > s.cfg.MaxBodySize {
			return fmt.Errorf("default_content must be at most %d bytes", s.cfg.MaxBodySize)
		}
		t.DefaultContent = *in.DefaultContent
	}
	if in.DefaultContentType != nil {
		ct := strings.TrimSpace(*in.DefaultContentType)
		if len(ct) > 255 || !validHeaderValue(ct) {
			return errors.New("default_content_type is not a valid header value")
		}
		if ct == "" {
			ct = "text/plain"
		}
		t.DefaultContentType = ct
	}
	if in.Timeout.set {
		if in.Timeout.v < 0 || in.Timeout.v > s.cfg.MaxDelay {
			return fmt.Errorf("timeout must be between 0 and %d seconds", s.cfg.MaxDelay)
		}
		t.Timeout = in.Timeout.v
	}
	if in.CORS.set {
		t.CORS = in.CORS.v
	}
	if in.Description != nil {
		d := strings.TrimSpace(*in.Description)
		if len([]rune(d)) > maxDescription {
			return fmt.Errorf("description must be at most %d characters", maxDescription)
		}
		t.Description = d
	}
	if in.ResponseHeaders != nil {
		hs := make([]store.Header, 0, len(*in.ResponseHeaders))
		for _, h := range *in.ResponseHeaders {
			h.Name, h.Value = strings.TrimSpace(h.Name), strings.TrimSpace(h.Value)
			if h.Name == "" {
				continue
			}
			if !validHeaderName(h.Name) || !validHeaderValue(h.Value) || len(h.Value) > 8192 {
				return fmt.Errorf("invalid response header %q", h.Name)
			}
			if forbiddenHeaders[http.CanonicalHeaderKey(h.Name)] {
				return fmt.Errorf("response header %q cannot be customised", h.Name)
			}
			hs = append(hs, h)
		}
		if len(hs) > maxResponseHeaders {
			return fmt.Errorf("at most %d response headers", maxResponseHeaders)
		}
		t.ResponseHeaders = hs
	}
	if in.Signature.set {
		return applySignature(t, in.Signature.v)
	}
	return nil
}

func applySignature(t *store.Token, in *signatureInput) error {
	if in == nil || in.Scheme != nil && strings.TrimSpace(*in.Scheme) == "" {
		t.Signature = nil
		return nil
	}
	c := signature.Config{Tolerance: signature.DefaultTolerance}
	if t.Signature != nil {
		c = *t.Signature
	}
	if in.Scheme != nil {
		scheme := strings.ToLower(strings.TrimSpace(*in.Scheme))
		if scheme != c.Scheme {
			c.Secret = "" // another provider: the old secret does not apply
		}
		c.Scheme = scheme
	}
	if in.Secret != nil && strings.TrimSpace(*in.Secret) != "" {
		c.Secret = strings.TrimSpace(*in.Secret)
	}
	if in.Header != nil {
		c.Header = strings.TrimSpace(*in.Header)
	}
	if in.Algorithm != nil {
		c.Algorithm = strings.ToLower(strings.TrimSpace(*in.Algorithm))
	}
	if in.Encoding != nil {
		c.Encoding = strings.ToLower(strings.TrimSpace(*in.Encoding))
	}
	if in.Prefix != nil {
		c.Prefix = *in.Prefix
	}
	if in.Tolerance.set {
		c.Tolerance = in.Tolerance.v
	}
	if in.Reject.set {
		c.Reject = in.Reject.v
	}
	if c.Tolerance == 0 && t.Signature != nil && !signature.Timestamped(t.Signature.Scheme) && !in.Tolerance.set {
		c.Tolerance = signature.DefaultTolerance // switching to a timestamped scheme
	}
	if err := c.Validate(); err != nil {
		return err
	}
	t.Signature = &c
	return nil
}

// forbiddenHeaders cannot be set on custom responses: hop-by-hop headers, the
// IDs the app adds, and headers that would act on the app's own origin (the
// UI shares it) such as wiping its storage or planting cookies.
var forbiddenHeaders = map[string]bool{
	"Content-Length": true, "Transfer-Encoding": true, "Connection": true,
	"Keep-Alive": true, "Upgrade": true, "Trailer": true, "Te": true,
	"X-Request-Id": true, "X-Token-Id": true,
	"Set-Cookie": true, "Clear-Site-Data": true, "Service-Worker-Allowed": true,
	"Strict-Transport-Security": true,
}

func validHeaderName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0 {
			continue
		}
		return false
	}
	return true
}

func validHeaderValue(s string) bool {
	return !strings.ContainsAny(s, "\r\n\x00")
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if s.accounts != nil && u == nil && !s.cfg.AnonymousURLs {
		writeError(w, http.StatusUnauthorized, "Sign in to create URLs")
		return
	}
	ip := s.clientIP(r)
	if ok, retry, err := s.store.Allow(r.Context(), "token", ip, s.cfg.TokenLimit); err != nil {
		internalError(w, r, err)
		return
	} else if !ok {
		tooManyRequests(w, retry)
		return
	}
	in, err := decodeTokenInput(w, r)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	now := time.Now().UTC()
	t := &store.Token{
		ID:                 uuid.NewString(),
		DefaultStatus:      http.StatusOK,
		DefaultContentType: "text/plain",
		ResponseHeaders:    []store.Header{},
		IP:                 ip,
		UserAgent:          r.UserAgent(),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := s.apply(t, in); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	// Signed in, the URL is private. Anonymous with accounts on, it comes
	// with the key that lets an account claim it later, given out this once.
	var claimKey string
	switch {
	case u != nil:
		t.OwnerID = u.ID
	case s.accounts != nil:
		claimKey, t.ClaimHash = newClaimKey()
	}
	if err := s.tokens.create(r.Context(), t); errors.Is(err, accounts.ErrTooManyURLs) {
		s.tooManyURLs(w)
		return
	} else if err != nil {
		internalError(w, r, err)
		return
	}
	v := s.view(r, t, 0)
	v.ClaimKey = claimKey
	writeJSON(w, http.StatusCreated, v)
}

func (s *Server) getToken(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadToken(w, r)
	if !ok {
		return
	}
	n, err := s.store.CountRequests(r.Context(), t.ID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.view(r, t, n))
}

func (s *Server) updateToken(w http.ResponseWriter, r *http.Request) {
	// The body first: a claim that lands while a slow body arrives would
	// otherwise be missed, and the anonymous copy loaded before it saved.
	in, err := decodeTokenInput(w, r)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	t, ok := s.loadToken(w, r)
	if !ok {
		return
	}
	if err := s.apply(t, in); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	v, err := s.saveAndAnnounce(r, t)
	if err != nil {
		s.saveError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) toggleCORS(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadToken(w, r)
	if !ok {
		return
	}
	t.CORS = !t.CORS
	if _, err := s.saveAndAnnounce(r, t); err != nil {
		s.saveError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": t.CORS})
}

// saveAndAnnounce stores t and tells live clients about the change.
func (s *Server) saveAndAnnounce(r *http.Request, t *store.Token) (*tokenView, error) {
	t.UpdatedAt = time.Now().UTC()
	if err := s.tokens.update(r.Context(), t); err != nil {
		return nil, err
	}
	n, err := s.store.CountRequests(r.Context(), t.ID)
	if err != nil {
		return nil, err
	}
	v := s.view(r, t, n)
	s.publish(r.Context(), t.ID, event{Type: "token.updated", Token: v, Total: n})
	return v, nil
}

// saveError answers a failed write: an anonymous URL claimed since loadToken
// read it is private now, as loadToken would answer; a private URL deleted
// or released from under the update is gone; anything else is a storage
// problem.
func (s *Server) saveError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrOwned):
		writeError(w, http.StatusForbidden, "URL is private")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "URL not found or expired")
	default:
		internalError(w, r, err)
	}
}

func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadToken(w, r)
	if !ok {
		return
	}
	if err := s.tokens.delete(r.Context(), t, s.clientIP(r), r.UserAgent()); err != nil {
		s.saveError(w, r, err)
		return
	}
	s.publish(r.Context(), t.ID, event{Type: "token.deleted"})
	w.WriteHeader(http.StatusNoContent)
}

// --- Requests ---------------------------------------------------------------

func (s *Server) listRequests(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadToken(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	summary, _ := strconv.ParseBool(q.Get("summary"))
	maxPer := max(s.cfg.MaxRequests, 50)
	if !summary {
		maxPer = 100 // full requests carry their bodies
	}
	o := store.ListOptions{
		Page:    min(max(atoiDefault(q.Get("page"), 1), 1), 1_000_000),
		PerPage: min(max(atoiDefault(q.Get("per_page"), 50), 1), maxPer),
		Newest:  q.Get("sorting") == "newest",
		Search:  strings.TrimSpace(q.Get("q")),
	}
	for m := range strings.SplitSeq(q.Get("method"), ",") {
		if m = strings.ToUpper(strings.TrimSpace(m)); m != "" {
			o.Methods = append(o.Methods, m)
		}
	}

	// Searches and full-body pages read a lot from Redis: run a few at a time.
	if !summary || o.Search != "" {
		select {
		case s.heavy <- struct{}{}:
			defer func() { <-s.heavy }()
		case <-r.Context().Done():
			return
		}
	}

	var (
		data  any
		n     int
		total int
		err   error
	)
	if summary {
		var sums []store.Summary
		sums, total, err = s.store.ListSummaries(r.Context(), t.ID, o)
		data, n = sums, len(sums)
	} else {
		var reqs []*store.Request
		reqs, total, err = s.store.ListRequests(r.Context(), t.ID, o)
		data, n = reqs, len(reqs)
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pageOf(data, n, total, o.Page, o.PerPage))
}

// pageOf is the shape of every paged list: data holds n of total items.
func pageOf(data any, n, total, page, perPage int) map[string]any {
	offset := (page - 1) * perPage
	from := 0
	if n > 0 {
		from = offset + 1
	}
	return map[string]any{
		"data":         data,
		"total":        total,
		"per_page":     perPage,
		"current_page": page,
		"is_last_page": offset+n >= total,
		"from":         from,
		"to":           offset + n,
	}
}

func (s *Server) latestRequest(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadToken(w, r)
	if !ok {
		return
	}
	reqs, _, err := s.store.ListRequests(r.Context(), t.ID, store.ListOptions{Page: 1, PerPage: 1, Newest: true})
	if err != nil {
		internalError(w, r, err)
		return
	}
	if len(reqs) == 0 {
		writeError(w, http.StatusNotFound, "no requests yet")
		return
	}
	writeJSON(w, http.StatusOK, reqs[0])
}

func (s *Server) findRequest(w http.ResponseWriter, r *http.Request) (*store.Token, *store.Request, bool) {
	t, ok := s.loadToken(w, r)
	if !ok {
		return nil, nil, false
	}
	id := strings.ToLower(r.PathValue("request"))
	if !isUUID(id) {
		writeError(w, http.StatusNotFound, "request not found")
		return nil, nil, false
	}
	req, err := s.store.GetRequest(r.Context(), t.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "request not found")
		return nil, nil, false
	}
	if err != nil {
		internalError(w, r, err)
		return nil, nil, false
	}
	return t, req, true
}

func (s *Server) getRequest(w http.ResponseWriter, r *http.Request) {
	if _, req, ok := s.findRequest(w, r); ok {
		writeJSON(w, http.StatusOK, req)
	}
}

// rawRequest serves the body exactly as received. Inline it is only ever
// served as JSON or plain text; ?download=1 keeps the original type.
func (s *Server) rawRequest(w http.ResponseWriter, r *http.Request) {
	_, req, ok := s.findRequest(w, r)
	if !ok {
		return
	}
	body := []byte(req.Content)
	if req.Encoding == "base64" {
		b, err := base64.StdEncoding.DecodeString(req.Content)
		if err != nil {
			internalError(w, r, err)
			return
		}
		body = b
	}
	mt, _, _ := mime.ParseMediaType(req.ContentType)
	download, _ := strconv.ParseBool(r.URL.Query().Get("download"))

	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", sandboxCSP)
	h.Set("Cache-Control", "no-store")
	switch {
	case download:
		ct := req.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		h.Set("Content-Type", ct)
		h.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="request-%s%s"`, req.ID[:8], extension(mt)))
	case req.Encoding == "base64":
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="request-%s%s"`, req.ID[:8], extension(mt)))
	case mt == "application/json" || strings.HasSuffix(mt, "+json"):
		h.Set("Content-Type", "application/json; charset=utf-8")
	default:
		h.Set("Content-Type", "text/plain; charset=utf-8")
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func extension(mediaType string) string {
	switch mediaType {
	case "application/json":
		return ".json"
	case "application/xml", "text/xml":
		return ".xml"
	case "text/html":
		return ".html"
	case "text/plain", "":
		return ".txt"
	}
	if exts, _ := mime.ExtensionsByType(mediaType); len(exts) > 0 {
		return exts[0]
	}
	return ".bin"
}

func (s *Server) deleteRequest(w http.ResponseWriter, r *http.Request) {
	t, req, ok := s.findRequest(w, r)
	if !ok {
		return
	}
	removed, err := s.store.DeleteRequest(r.Context(), t, req.ID)
	if err != nil {
		s.saveError(w, r, err)
		return
	}
	n, _ := s.store.CountRequests(r.Context(), t.ID)
	s.publish(r.Context(), t.ID, event{Type: "request.deleted", ID: req.ID, Total: n})
	writeJSON(w, http.StatusOK, map[string]bool{"status": removed})
}

func (s *Server) deleteRequests(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadToken(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteRequests(r.Context(), t); err != nil {
		s.saveError(w, r, err)
		return
	}
	s.publish(r.Context(), t.ID, event{Type: "requests.cleared"})
	writeJSON(w, http.StatusOK, map[string]bool{"status": true})
}

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}
