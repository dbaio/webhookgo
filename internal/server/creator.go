package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/dbaio/webhookgo/internal/store"
)

// abuseCreator is GET /api/abuse/urls/{token}: who created a live anonymous
// URL (ip, user_agent, created_at), for the /abuse page. The rest of the API
// shows them to the URL's owner and the admins only; the abuse page shows
// them to anyone who looks the URL up there, so that abuse can be reported
// to the network it came from, with or without accounts. A private URL keeps
// them (403). Reading does not extend the URL's life.
func (s *Server) abuseCreator(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(r.PathValue("token"))
	if !isUUID(id) {
		writeError(w, http.StatusNotFound, "URL not found")
		return
	}
	t, err := s.store.PeekToken(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "URL not found or expired")
	case err != nil:
		internalError(w, r, err)
	case t.Owned():
		writeError(w, http.StatusForbidden, "URL is private")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"ip": t.IP, "user_agent": t.UserAgent, "created_at": t.CreatedAt})
	}
}
