package server

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/dbaio/webhookgo/internal/auth"
	"github.com/dbaio/webhookgo/internal/signature"
	"github.com/dbaio/webhookgo/internal/store"
)

const (
	// Bytes past MAX_BODY_SIZE are counted (to report the real size) up to
	// this much more, then the connection is left to close.
	discardLimit  = 32 << 20
	maxFormValue  = 64 << 10
	maxFormFields = 1000

	// Custom responses run in an opaque origin so a URL serving HTML cannot
	// script the app or read its local storage.
	sandboxCSP = "sandbox allow-scripts allow-forms allow-popups allow-modals allow-downloads"
)

// capture records a webhook call and answers with the URL's configured response.
func (s *Server) capture(w http.ResponseWriter, r *http.Request, tokenID, rest string) {
	ctx := r.Context()
	ip := s.clientIP(r)

	if ok, retry, err := s.store.Allow(ctx, "capture", ip, s.cfg.RateLimit); err != nil {
		internalError(w, r, err)
		return
	} else if !ok {
		tooManyRequests(w, retry)
		return
	}

	token, err := s.tokens.read(ctx, tokenID)
	// A deleted private URL leaves no public trace: captures are anonymous,
	// so it is a 404 like a URL that never existed.
	if d, ok := errors.AsType[*store.DeletedError](err); ok && !d.Owned {
		writeError(w, http.StatusGone, "URL deleted")
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "URL not found or expired, create a new one")
		return
	}
	if err != nil {
		internalError(w, r, err)
		return
	}

	status := token.DefaultStatus
	if seg, _, _ := strings.Cut(strings.TrimPrefix(rest, "/"), "/"); len(seg) == 3 {
		if code, err := strconv.Atoi(seg); err == nil && validStatus(code) {
			status = code
		}
	}
	preflight := token.CORS && r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
	if preflight {
		status = http.StatusNoContent
	}

	// Preflights carry no signature: browsers send them on their own.
	var check *signature.Check
	if token.Signature != nil && !preflight {
		check = token.Signature.Start(r.Header, time.Now())
	}
	// The UI itself calls capture URLs (the test request button), and a
	// signed-in browser sends its session cookie along: it must never be
	// recorded.
	stripSessionCookies(r.Header)
	req, body := s.newRequest(r, tokenID, rest, ip, check)
	var rejection string
	if sig := req.Signature; sig != nil && sig.Status != signature.Valid && token.Signature.Reject {
		status = http.StatusUnauthorized
		rejection = "signature " + string(sig.Status) + ": " + sig.Detail
	}
	req.ResponseStatus = status

	total, evicted, err := s.tokens.addRequest(ctx, req, body)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "URL not found or expired, create a new one")
		return
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	sum := req.Summary()
	s.publish(ctx, tokenID, event{Type: "request.created", Request: &sum, Total: total, Evicted: evicted})
	// Only the ID is used from here on, so a delayed response does not keep
	// the body reachable while it sleeps.
	reqID := req.ID

	if rejection != "" {
		h := w.Header()
		h.Set("X-Request-Id", reqID)
		h.Set("X-Token-Id", tokenID)
		if token.CORS {
			setCORS(h, r)
		}
		writeError(w, status, rejection)
		return
	}

	if d := min(token.Timeout, s.cfg.MaxDelay); d > 0 {
		t := time.NewTimer(time.Duration(d) * time.Second)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return
		}
	}

	h := w.Header()
	ctype := token.DefaultContentType
	if ctype == "" {
		ctype = "text/plain; charset=utf-8"
	}
	h.Set("Content-Type", ctype)
	replaced := map[string]bool{}
	for _, hd := range token.ResponseHeaders {
		name := http.CanonicalHeaderKey(hd.Name)
		if !replaced[name] {
			h.Del(name)
			replaced[name] = true
		}
		h.Add(name, hd.Value)
	}
	h.Set("X-Request-Id", reqID)
	h.Set("X-Token-Id", tokenID)
	if token.CORS {
		setCORS(h, r)
	}
	h.Set("X-Content-Type-Options", "nosniff")
	h.Add("Content-Security-Policy", sandboxCSP) // added, so a custom CSP cannot lift it

	if preflight {
		h.Del("Content-Type")
		w.WriteHeader(status)
		return
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead && bodyAllowed(status) {
		_, _ = io.WriteString(w, token.DefaultContent)
	}
}

// newRequest reads the call into a Request and returns the raw body with it.
// The body streams through check, when given, so its signature covers every
// byte even past MAX_BODY_SIZE.
func (s *Server) newRequest(r *http.Request, tokenID, rest, ip string, check *signature.Check) (*store.Request, []byte) {
	var src io.Reader = r.Body
	if check != nil {
		src = io.TeeReader(r.Body, check)
	}
	body, size, truncated, complete := readBody(src, s.cfg.MaxBodySize)

	headers := make(map[string][]string, len(r.Header)+1)
	headers["host"] = []string{r.Host}
	for name, values := range r.Header {
		headers[strings.ToLower(name)] = values
	}
	if len(r.TransferEncoding) > 0 {
		headers["transfer-encoding"] = r.TransferEncoding
	}

	req := &store.Request{
		ID:          uuid.Must(uuid.NewV7()).String(),
		TokenID:     tokenID,
		Method:      r.Method,
		URL:         s.scheme(r) + "://" + s.host(r) + r.URL.RequestURI(),
		Path:        rest,
		Hostname:    s.host(r),
		IP:          ip,
		UserAgent:   r.UserAgent(),
		Protocol:    r.Proto,
		Headers:     headers,
		Query:       parsePairs(r.URL.RawQuery),
		Form:        []store.KV{},
		Files:       []store.File{},
		ContentType: r.Header.Get("Content-Type"),
		Size:        size,
		Truncated:   truncated,
		CreatedAt:   time.Now().UTC(),
	}
	req.SetBody(body)
	if check != nil {
		res := check.Finish(body, truncated, complete)
		req.Signature = &res
	}

	mediaType, params, _ := mime.ParseMediaType(req.ContentType)
	switch mediaType {
	case "application/x-www-form-urlencoded":
		if req.Encoding == "" {
			req.Form = parsePairs(req.Content)
		}
	case "multipart/form-data":
		req.Form, req.Files = parseMultipart(body, params["boundary"])
	}
	return req, body
}

// stripSessionCookies removes the app's session cookie, and the one of a
// sign-in through Google, from every Cookie header, keeping the other
// cookies and dropping headers left empty.
func stripSessionCookies(h http.Header) {
	lines := h.Values("Cookie")
	if len(lines) == 0 {
		return
	}
	var kept []string
	for _, line := range lines {
		var parts []string
		for part := range strings.SplitSeq(line, ";") {
			part = strings.TrimSpace(part)
			name, _, _ := strings.Cut(part, "=")
			if part == "" || name == auth.SessionCookie || name == auth.HostSessionCookie || name == googleCookie || name == hostGoogleCookie {
				continue
			}
			parts = append(parts, part)
		}
		if len(parts) > 0 {
			kept = append(kept, strings.Join(parts, "; "))
		}
	}
	h.Del("Cookie")
	for _, line := range kept {
		h.Add("Cookie", line)
	}
}

// readBody keeps the first limit bytes and counts the rest. complete reports
// whether the whole body was read.
func readBody(rc io.Reader, limit int64) (body []byte, size int64, truncated, complete bool) {
	var buf bytes.Buffer
	n, _ := io.CopyN(&buf, rc, limit)
	extra, _ := io.Copy(io.Discard, io.LimitReader(rc, discardLimit))
	return buf.Bytes(), n + extra, extra > 0, extra < discardLimit
}

// parsePairs decodes a query string or urlencoded form, keeping order and
// repeated names. Values are capped: the full text stays in the body.
func parsePairs(s string) []store.KV {
	out := []store.KV{}
	for part := range strings.SplitSeq(s, "&") {
		if part == "" {
			continue
		}
		if len(out) == maxFormFields {
			break
		}
		name, value, _ := strings.Cut(part, "=")
		out = append(out, store.KV{Name: clip(unescape(name)), Value: clip(unescape(value))})
	}
	return out
}

func clip(s string) string {
	if len(s) <= maxFormValue {
		return s
	}
	n := maxFormValue
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "… (truncated)"
}

func unescape(s string) string {
	if u, err := url.QueryUnescape(s); err == nil {
		return u
	}
	return s
}

func parseMultipart(body []byte, boundary string) ([]store.KV, []store.File) {
	fields, files := []store.KV{}, []store.File{}
	if boundary == "" {
		return fields, files
	}
	mr := multipart.NewReader(bytes.NewReader(body), boundary)
	for len(fields)+len(files) < maxFormFields {
		part, err := mr.NextPart()
		if err != nil {
			break // io.EOF, or a body cut by MAX_BODY_SIZE
		}
		if name := part.FileName(); name != "" {
			n, _ := io.Copy(io.Discard, part)
			files = append(files, store.File{
				Field:       part.FormName(),
				Filename:    name,
				ContentType: part.Header.Get("Content-Type"),
				Size:        n,
			})
			continue
		}
		v, _ := io.ReadAll(io.LimitReader(part, maxFormValue))
		value := string(v)
		if !utf8.Valid(v) {
			value = "(binary, " + strconv.Itoa(len(v)) + " bytes)"
		}
		fields = append(fields, store.KV{Name: part.FormName(), Value: value})
	}
	return fields, files
}

func setCORS(h http.Header, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = "*"
	} else {
		h.Set("Access-Control-Allow-Credentials", "true")
		h.Add("Vary", "Origin")
	}
	h.Set("Access-Control-Allow-Origin", origin)
	h.Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS")
	if req := r.Header.Get("Access-Control-Request-Headers"); req != "" {
		h.Set("Access-Control-Allow-Headers", req)
	} else {
		h.Set("Access-Control-Allow-Headers", "*")
	}
	h.Set("Access-Control-Expose-Headers", "*")
	h.Set("Access-Control-Max-Age", "600")
}

func validStatus(code int) bool { return code >= 200 && code <= 599 }

func bodyAllowed(status int) bool {
	return status != http.StatusNoContent && status != http.StatusNotModified
}

func tooManyRequests(w http.ResponseWriter, retry time.Duration) {
	secs := max(1, int(retry.Round(time.Second).Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeError(w, http.StatusTooManyRequests, "rate limit exceeded, retry in "+strconv.Itoa(secs)+"s")
}
