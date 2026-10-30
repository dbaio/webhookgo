package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dbaio/webhookgo/internal/store"
)

const (
	defaultWait = 30 * time.Second
	// maxWait stays under the read timeouts of the proxies in deploy/ (nginx
	// 90 s, Cloudflare 100 s); callers that need longer call again.
	maxWait    = 60 * time.Second
	maxMatches = 20
	waitChunk  = 25 // full requests read at a time while scanning the history
)

type waitOutcome int

const (
	waitFound waitOutcome = iota
	waitTimeout
	waitGone     // the URL was deleted
	waitPrivate  // the URL was claimed by an account, and the caller may no longer watch it
	waitResync   // events may have been missed: read the history again
	waitLeft     // the client went away
	waitShutdown // the server is stopping
)

// wait answers with the oldest request that arrived at or after ?since and
// matches the filters, waiting up to ?timeout seconds for one. It lets a
// script or an end-to-end test trigger a webhook elsewhere and block until
// it shows up.
func (s *Server) wait(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadToken(w, r)
	if !ok {
		return
	}
	since, timeout, m, err := parseWait(r.URL.Query(), time.Now())
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(timeout + 30*time.Second))

	ctx := r.Context()
	expired := time.NewTimer(timeout)
	defer expired.Stop()
	for {
		// Subscribe before reading the history, so a request stored in
		// between is seen by one or the other.
		events, cancel := s.hub.Subscribe(t.ID)
		req, err := s.firstMatch(ctx, t.ID, since, m)
		out := waitFound
		if err == nil && req == nil {
			out = waitTimeout
			if timeout > 0 {
				req, out, err = s.nextMatch(r, events, t.ID, since, m, expired.C)
			}
		}
		cancel()

		switch {
		case err != nil:
			internalError(w, r, err)
		case req != nil:
			writeJSON(w, http.StatusOK, req)
		case out == waitTimeout:
			writeError(w, http.StatusRequestTimeout, fmt.Sprintf("no matching request within %s", timeout))
		case out == waitGone:
			writeError(w, http.StatusNotFound, "URL was deleted")
		case out == waitPrivate:
			writeError(w, http.StatusForbidden, "URL is private")
		case out == waitShutdown:
			writeError(w, http.StatusServiceUnavailable, "server is restarting, please retry")
		case out == waitResync:
			continue
		}
		return
	}
}

// firstMatch scans the requests stored since since, oldest first.
func (s *Server) firstMatch(ctx context.Context, tokenID string, since time.Time, m *matcher) (*store.Request, error) {
	ids, err := s.store.IDsSince(ctx, tokenID, since)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	select {
	case s.heavy <- struct{}{}:
		defer func() { <-s.heavy }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	for chunk := range slices.Chunk(ids, waitChunk) {
		reqs, err := s.store.LoadRequests(ctx, tokenID, chunk)
		if err != nil {
			return nil, err
		}
		for _, req := range reqs {
			if m.match(req) {
				return req, nil
			}
		}
	}
	return nil, nil
}

// nextMatch follows the URL's live events until a matching request arrives.
func (s *Server) nextMatch(r *http.Request, events <-chan []byte, tokenID string, since time.Time, m *matcher, expired <-chan time.Time) (*store.Request, waitOutcome, error) {
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return nil, waitLeft, nil
		case <-s.shutdown:
			return nil, waitShutdown, nil
		case <-expired:
			return nil, waitTimeout, nil
		case msg, ok := <-events:
			if !ok {
				return nil, waitResync, nil // dropped as a slow client
			}
			var ev event
			if json.Unmarshal(msg, &ev) != nil {
				continue
			}
			switch ev.Type {
			case "token.deleted":
				return nil, waitGone, nil
			case "token.claimed", "token.released":
				// Who may watch the URL just changed: checked again, as the
				// stream does, so an anonymous caller does not keep a URL
				// that is private now. The new owner's own wait goes on.
				if !s.streamAllowed(r, tokenID) {
					return nil, waitPrivate, nil
				}
			case "resync":
				return nil, waitResync, nil
			case "request.created":
				sum := ev.Request
				if sum == nil || sum.CreatedAt.Before(since) || !m.methodOK(sum.Method) {
					continue
				}
				req, err := s.store.GetRequest(ctx, tokenID, sum.ID)
				if errors.Is(err, store.ErrNotFound) {
					continue
				}
				if err != nil {
					return nil, waitFound, err
				}
				if m.match(req) {
					return req, waitFound, nil
				}
			}
		}
	}
}

func parseWait(q url.Values, now time.Time) (since time.Time, timeout time.Duration, m *matcher, err error) {
	since = now
	if v := strings.TrimSpace(q.Get("since")); v != "" {
		if since, err = parseSince(v); err != nil {
			return
		}
	}
	since = time.UnixMilli(since.UnixMilli()) // stored arrival times are kept in milliseconds

	timeout = defaultWait
	if v := strings.TrimSpace(q.Get("timeout")); v != "" {
		n, convErr := strconv.Atoi(v)
		if convErr != nil || n < 0 || time.Duration(n)*time.Second > maxWait {
			err = fmt.Errorf("timeout must be between 0 and %d seconds", int(maxWait.Seconds()))
			return
		}
		timeout = time.Duration(n) * time.Second
	}

	m = &matcher{text: strings.TrimSpace(q.Get("q"))}
	for v := range strings.SplitSeq(q.Get("method"), ",") {
		if v = strings.ToUpper(strings.TrimSpace(v)); v != "" {
			m.methods = append(m.methods, v)
		}
	}
	if len(q["match"]) > maxMatches {
		err = fmt.Errorf("at most %d match conditions", maxMatches)
		return
	}
	for _, v := range q["match"] {
		c, cErr := parseCondition(v)
		if cErr != nil {
			err = cErr
			return
		}
		m.conds = append(m.conds, c)
	}
	return
}

// parseSince reads a Unix time in seconds (fractions allowed) or
// milliseconds, or an RFC 3339 date.
func parseSince(v string) (time.Time, error) {
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		switch {
		case math.IsNaN(f) || math.IsInf(f, 0) || f < 0:
		case f >= 1e12:
			return time.UnixMilli(int64(f)), nil
		default:
			sec, frac := math.Modf(f)
			return time.Unix(int64(sec), int64(frac*1e9)), nil
		}
	} else if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return t, nil
	}
	return time.Time{}, errors.New("since must be a Unix time (seconds or milliseconds) or an RFC 3339 date")
}

// matcher holds the filters of a wait: the same text search and methods as
// the request list, plus exact-value conditions.
type matcher struct {
	text    string
	methods []string
	conds   []condition
}

// condition is one ?match=<field>=<value>, where field is body.<json.path>,
// header.<name>, query.<name>, form.<name> or path.
type condition struct {
	kind  string
	name  string
	path  []string // body: keys and array indexes
	value string
}

func parseCondition(v string) (condition, error) {
	field, value, ok := strings.Cut(v, "=")
	kind, name, _ := strings.Cut(field, ".")
	c := condition{kind: kind, name: name, value: value}
	switch {
	case !ok:
		return c, fmt.Errorf("match %q: expected <field>=<value>", v)
	case kind == "path" && name == "":
	case kind == "header" && name != "":
		c.name = strings.ToLower(name)
	case (kind == "query" || kind == "form") && name != "":
	case kind == "body" && name != "":
		c.path = strings.Split(name, ".")
	default:
		return c, fmt.Errorf("match %q: the field must be body.<path>, header.<name>, query.<name>, form.<name> or path", v)
	}
	return c, nil
}

func (m *matcher) methodOK(method string) bool {
	return len(m.methods) == 0 || slices.Contains(m.methods, method)
}

func (m *matcher) match(r *store.Request) bool {
	if !m.methodOK(r.Method) || m.text != "" && !r.Contains(m.text) {
		return false
	}
	var (
		doc    any
		parsed bool
		isJSON bool
	)
	for _, c := range m.conds {
		var ok bool
		switch c.kind {
		case "path":
			ok = r.Path == c.value
		case "header":
			ok = slices.Contains(r.Headers[c.name], c.value)
		case "query":
			ok = hasPair(r.Query, c.name, c.value)
		case "form":
			ok = hasPair(r.Form, c.name, c.value)
		case "body":
			if !parsed {
				doc, isJSON = decodeJSON(r)
				parsed = true
			}
			if isJSON {
				v, found := lookup(doc, c.path)
				ok = found && scalarEquals(v, c.value)
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func hasPair(kvs []store.KV, name, value string) bool {
	return slices.ContainsFunc(kvs, func(kv store.KV) bool { return kv.Name == name && kv.Value == value })
}

func decodeJSON(r *store.Request) (any, bool) {
	if r.Encoding != "" {
		return nil, false
	}
	dec := json.NewDecoder(strings.NewReader(r.Content))
	dec.UseNumber() // keep 64-bit IDs exact
	var v any
	if dec.Decode(&v) != nil {
		return nil, false
	}
	return v, true
}

func lookup(v any, path []string) (any, bool) {
	for _, key := range path {
		switch node := v.(type) {
		case map[string]any:
			next, ok := node[key]
			if !ok {
				return nil, false
			}
			v = next
		case []any:
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false
			}
			v = node[i]
		default:
			return nil, false
		}
	}
	return v, true
}

// scalarEquals compares a JSON value with its text form: strings as they
// are, numbers as written in the body, true, false and null.
func scalarEquals(v any, want string) bool {
	switch x := v.(type) {
	case string:
		return x == want
	case json.Number:
		return x.String() == want
	case bool:
		return strconv.FormatBool(x) == want
	case nil:
		return want == "null"
	}
	return false
}
