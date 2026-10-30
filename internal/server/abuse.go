package server

import (
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dbaio/webhookgo/internal/accounts"
)

// Limits of an abuse report from the /abuse page.
const (
	maxReportIDs     = 50
	maxReportMessage = 2000 // characters, message and note alike
	reportLimit      = 10   // submissions per hour per client IP
	reportWindow     = time.Hour
)

// reportAbuse is POST /api/abuse/reports: the /abuse page reports URLs to
// the admins, who see them in the panel. It always answers 202 to valid
// input, whatever the IDs are: only URLs that exist are stored, and an
// address's report of a URL is kept once a day, so the page cannot be used
// to probe for URLs or to fill the queue.
func (s *Server) reportAbuse(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if ok, retry, err := s.store.AllowPer(r.Context(), "report", ip, reportLimit, reportWindow); err != nil {
		internalError(w, r, err)
		return
	} else if !ok {
		tooManyRequests(w, retry)
		return
	}
	var in struct {
		IDs     []string `json:"ids"`
		Message string   `json:"message"`
		Contact string   `json:"contact"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	message, contact := strings.TrimSpace(in.Message), strings.TrimSpace(in.Contact)
	var ids []string
	for _, id := range in.IDs {
		id = strings.ToLower(strings.TrimSpace(id))
		if !isUUID(id) {
			writeError(w, http.StatusUnprocessableEntity, "ids must be URL IDs")
			return
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	switch {
	case len(in.IDs) == 0:
		writeError(w, http.StatusUnprocessableEntity, "ids is required")
		return
	case len(in.IDs) > maxReportIDs:
		writeError(w, http.StatusUnprocessableEntity, "at most 50 ids per report")
		return
	case utf8.RuneCountInString(message) > maxReportMessage:
		writeError(w, http.StatusUnprocessableEntity, "message must be at most 2000 characters")
		return
	case len(contact) > maxEmail:
		writeError(w, http.StatusUnprocessableEntity, "contact must be at most 254 characters")
		return
	}
	for _, id := range ids {
		if ok, err := s.tokens.exists(r.Context(), id); err != nil {
			internalError(w, r, err)
			return
		} else if !ok {
			continue
		}
		if _, err := s.accounts.AddReport(r.Context(), accounts.NewReport{TokenID: id, ReporterIP: ip, Contact: contact, Message: message}); err != nil {
			internalError(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "received"})
}
