package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/dbaio/webhookgo/internal/accounts"
)

const (
	keepAlive = 20 * time.Second

	// streamRecheckEvery is how often an open stream checks, with accounts
	// on, that its viewer may still watch the URL: being signed out or
	// disabled, or losing the URL, takes effect within this long.
	streamRecheckEvery = time.Minute
)

// stream pushes a URL's events to the browser with Server-Sent Events.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadToken(w, r)
	if !ok {
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("X-Accel-Buffering", "no") // disable proxy buffering (nginx)
	w.WriteHeader(http.StatusOK)

	events, cancel := s.hub.Subscribe(t.ID)
	defer cancel()

	n, _ := s.store.CountRequests(r.Context(), t.ID)
	hello, _ := json.Marshal(event{Type: "connected", Total: n})
	fmt.Fprintf(w, "retry: 3000\ndata: %s\n\n", hello)
	if rc.Flush() != nil {
		return
	}

	var recheck <-chan time.Time
	if s.streamRecheck > 0 {
		ticker := time.NewTicker(s.streamRecheck)
		defer ticker.Stop()
		recheck = ticker.C
	}
	ping := time.NewTicker(keepAlive)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.shutdown:
			return
		case <-recheck:
			if !s.streamAllowed(r, t.ID) {
				return
			}
		case msg, ok := <-events:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			if reauthorizes(msg) {
				// Who may watch the URL just changed: every viewer
				// reconnects and is checked again.
				_ = rc.Flush()
				return
			}
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
		}
		if rc.Flush() != nil {
			return
		}
	}
}

// streamAllowed re-checks, while a stream is open, that its viewer may still
// watch the URL: their session is still valid and they are not disabled, and
// the URL still exists and is public, theirs, or they are an admin. It costs
// one session lookup and one token read. An error counts as a failure: the
// browser reconnects and goes through loadToken, which answers properly.
func (s *Server) streamAllowed(r *http.Request, id string) bool {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var u *accounts.User
	if secret := s.sessionSecret(r); secret != "" {
		var err error
		if u, err = s.accounts.SessionUser(ctx, secret, s.cfg.SessionTTL); err != nil && !errors.Is(err, accounts.ErrNotFound) {
			return false
		}
	}
	t, err := s.tokens.read(ctx, id)
	if err != nil {
		return false
	}
	return !t.Owned() || u != nil && (u.ID == t.OwnerID || isAdmin(u))
}

// reauthorizes reports whether an event changes who may see the URL.
func reauthorizes(msg []byte) bool {
	var ev struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(msg, &ev) != nil {
		return false
	}
	return ev.Type == "token.claimed" || ev.Type == "token.released"
}
