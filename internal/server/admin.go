package server

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dbaio/webhookgo/internal/accounts"
	"github.com/dbaio/webhookgo/internal/store"
)

// adminLinkTTL is how long the invite and reset links made in the panel
// stay valid.
const adminLinkTTL = 72 * time.Hour

const (
	errAdminsOnly   = "Admins only"
	errLastAdmin    = "Keep at least one active admin"
	errOwnAccount   = "You can't do that to your own account"
	errUserNotFound = "User not found"
)

// admin guards a panel route: accounts must be on (404 otherwise) and the
// caller must be an admin signed in with a session, never an API key (403
// otherwise).
func (s *Server) admin(h http.HandlerFunc) http.HandlerFunc {
	return s.withAccounts(func(w http.ResponseWriter, r *http.Request) {
		if sess := sessionFrom(r); sess == nil || sess.secret == "" || !isAdmin(sess.user) {
			writeError(w, http.StatusForbidden, errAdminsOnly)
			return
		}
		h(w, r)
	})
}

// logAdmin records what the signed-in admin just did.
func (s *Server) logAdmin(r *http.Request, action, targetType, targetID string, details map[string]any) error {
	u := userFrom(r)
	return s.accounts.Log(r.Context(), accounts.NewLogEntry{
		ActorID: u.ID, ActorEmail: u.Email, Action: action, TargetType: targetType, TargetID: targetID,
		Details: details, IP: s.clientIP(r),
	})
}

// paging reads ?page and ?per_page.
func paging(r *http.Request) (page, perPage int) {
	q := r.URL.Query()
	return min(max(atoiDefault(q.Get("page"), 1), 1), 1_000_000), min(max(atoiDefault(q.Get("per_page"), 20), 1), 100)
}

// pathID reads the {id} path value, answering 404 with msg unless it is a
// UUID.
func pathID(w http.ResponseWriter, r *http.Request, msg string) (string, bool) {
	id := strings.ToLower(r.PathValue("id"))
	if !isUUID(id) {
		writeError(w, http.StatusNotFound, msg)
		return "", false
	}
	return id, true
}

func validRole(role string) bool { return role == accounts.RoleAdmin || role == accounts.RoleUser }

// userError answers a failed change to a user and reports whether it did.
func (s *Server) userError(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, accounts.ErrNotFound):
		writeError(w, http.StatusNotFound, errUserNotFound)
	case errors.Is(err, accounts.ErrSelf):
		writeError(w, http.StatusConflict, errOwnAccount)
	case errors.Is(err, accounts.ErrLastAdmin):
		writeError(w, http.StatusConflict, errLastAdmin)
	default:
		internalError(w, r, err)
	}
	return true
}

// oneTimeLink answers a link just created, with the page that redeems it.
func (s *Server) oneTimeLink(w http.ResponseWriter, r *http.Request, page, token string, expires time.Time) {
	writeJSON(w, http.StatusCreated, map[string]any{"link": s.baseURL(r) + page + "#" + token, "expires_at": expires})
}

func (s *Server) adminStats(w http.ResponseWriter, r *http.Request) {
	st, err := s.accounts.Stats(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	rs, err := s.store.RedisStats(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"users":    map[string]int{"total": st.Users, "admins": st.Admins, "disabled": st.Disabled},
		"urls":     map[string]int{"owned": st.URLs},
		"reports":  map[string]int{"open": st.OpenReports},
		"redis":    rs,
		"database": map[string]int64{"size_bytes": st.DatabaseBytes},
		"version":  s.version,
	})
}

// --- Users ------------------------------------------------------------------

func (s *Server) adminListUsers(w http.ResponseWriter, r *http.Request) {
	page, perPage := paging(r)
	users, total, err := s.accounts.ListAdminUsers(r.Context(), strings.TrimSpace(r.URL.Query().Get("q")), page, perPage)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pageOf(users, len(users), total, page, perPage))
}

// adminInvite is POST /api/admin/users: an invite link for a new account.
func (s *Server) adminInvite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	email, role := strings.TrimSpace(in.Email), strings.ToLower(strings.TrimSpace(in.Role))
	if role == "" {
		role = accounts.RoleUser
	}
	if err := validEmail(email); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if !validRole(role) {
		writeError(w, http.StatusUnprocessableEntity, "role must be admin or user")
		return
	}
	if _, err := s.accounts.UserByEmail(r.Context(), email); err == nil {
		writeError(w, http.StatusConflict, "That email is already registered")
		return
	} else if !errors.Is(err, accounts.ErrNotFound) {
		internalError(w, r, err)
		return
	}
	token, err := s.accounts.CreateLink(r.Context(), accounts.NewLink{
		Kind: accounts.LinkInvite, Email: email, Role: role, CreatedBy: userFrom(r).ID, TTL: adminLinkTTL,
	})
	if err != nil {
		internalError(w, r, err)
		return
	}
	expires := time.Now().UTC().Add(adminLinkTTL).Truncate(time.Second)
	if err := s.logAdmin(r, accounts.ActionUserInvite, accounts.TargetLink, email, map[string]any{"email": email, "role": role, "expires_at": expires}); err != nil {
		internalError(w, r, err)
		return
	}
	s.oneTimeLink(w, r, "/invite", token, expires)
}

// adminUpdateUser is PATCH /api/admin/users/{id}: the role, or whether the
// account is disabled.
func (s *Server) adminUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, errUserNotFound)
	if !ok {
		return
	}
	var in struct {
		Role     *string `json:"role"`
		Disabled *bool   `json:"disabled"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Role == nil && in.Disabled == nil {
		writeError(w, http.StatusUnprocessableEntity, "nothing to change: send role or disabled")
		return
	}
	if in.Role != nil {
		role := strings.ToLower(strings.TrimSpace(*in.Role))
		if !validRole(role) {
			writeError(w, http.StatusUnprocessableEntity, "role must be admin or user")
			return
		}
		in.Role = &role
	}
	u, err := s.accounts.UpdateUser(r.Context(), userFrom(r), id, accounts.UserUpdate{Role: in.Role, Disabled: in.Disabled}, s.clientIP(r))
	if s.userError(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// adminResetLink is POST /api/admin/users/{id}/reset: a password reset link.
func (s *Server) adminResetLink(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, errUserNotFound)
	if !ok {
		return
	}
	u, err := s.accounts.UserByID(r.Context(), id)
	if s.userError(w, r, err) {
		return
	}
	token, err := s.accounts.CreateLink(r.Context(), accounts.NewLink{
		Kind: accounts.LinkReset, UserID: u.ID, CreatedBy: userFrom(r).ID, TTL: adminLinkTTL,
	})
	if err != nil {
		internalError(w, r, err)
		return
	}
	expires := time.Now().UTC().Add(adminLinkTTL).Truncate(time.Second)
	if err := s.logAdmin(r, accounts.ActionUserReset, accounts.TargetUser, u.ID, map[string]any{"email": u.Email, "expires_at": expires}); err != nil {
		internalError(w, r, err)
		return
	}
	s.oneTimeLink(w, r, "/reset", token, expires)
}

// adminDeleteUser is DELETE /api/admin/users/{id}. The user's URLs go
// first, one by one through the token layer, so their Redis keys are
// removed and their audit records written; then the account, with its
// sessions and links.
func (s *Server) adminDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, errUserNotFound)
	if !ok {
		return
	}
	ctx, actor := r.Context(), userFrom(r)
	if actor.ID == id {
		writeError(w, http.StatusConflict, errOwnAccount)
		return
	}
	u, err := s.accounts.UserByID(ctx, id)
	if s.userError(w, r, err) {
		return
	}
	// Refused here before any URL goes; the transaction checks again.
	if u.Role == accounts.RoleAdmin && !u.Disabled() {
		if n, err := s.accounts.ActiveAdmins(ctx); err != nil {
			internalError(w, r, err)
			return
		} else if n <= 1 {
			writeError(w, http.StatusConflict, errLastAdmin)
			return
		}
	}
	tokens, err := s.accounts.OwnerURLs(ctx, id)
	if err != nil {
		internalError(w, r, err)
		return
	}
	ip, userAgent := s.clientIP(r), r.UserAgent()
	for _, t := range tokens {
		if err := s.tokens.delete(ctx, t, ip, userAgent); err != nil {
			internalError(w, r, err)
			return
		}
		s.publish(ctx, t.ID, event{Type: "token.deleted"})
	}
	if err := s.accounts.DeleteUser(ctx, actor, id, len(tokens), ip); s.userError(w, r, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- URLs -------------------------------------------------------------------

// adminURL is a URL in the panel's list.
type adminURL struct {
	*tokenView
	Owner           *accounts.Owner `json:"owner"` // null for an anonymous URL
	LatestRequestAt *time.Time      `json:"latest_request_at"`
}

// adminListURLs is GET /api/admin/urls: every URL that belongs to an
// account, newest first, with ?q (a description, or a URL's ID or creator
// IP), ?owner (a user ID), ?page and ?per_page. With ?kind=anonymous, the
// anonymous URLs instead.
func (s *Server) adminListURLs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, perPage := paging(r)
	owner := strings.ToLower(strings.TrimSpace(q.Get("owner")))
	switch q.Get("kind") {
	case "", "owned":
	case "anonymous":
		if owner != "" {
			writeError(w, http.StatusUnprocessableEntity, "owner only applies to private URLs")
			return
		}
		s.adminListAnonymous(w, r, strings.TrimSpace(q.Get("q")), page, perPage)
		return
	default:
		writeError(w, http.StatusUnprocessableEntity, "kind must be owned or anonymous")
		return
	}
	if owner != "" && !isUUID(owner) {
		writeError(w, http.StatusUnprocessableEntity, "owner must be a user id")
		return
	}
	urls, total, err := s.accounts.ListAllURLs(r.Context(), strings.TrimSpace(q.Get("q")), owner, page, perPage)
	if err != nil {
		internalError(w, r, err)
		return
	}
	ids := make([]string, len(urls))
	for i, u := range urls {
		ids[i] = u.Token.ID
	}
	activity, err := s.store.Activities(r.Context(), ids)
	if err != nil {
		internalError(w, r, err)
		return
	}
	items := make([]adminURL, len(urls))
	for i, u := range urls {
		items[i] = adminURL{tokenView: s.view(r, u.Token, activity[i].Requests), Owner: &u.Owner, LatestRequestAt: activity[i].LastRequestAt}
	}
	writeJSON(w, http.StatusOK, pageOf(items, len(items), total, page, perPage))
}

// adminListAnonymous answers ?kind=anonymous. A search reads every
// anonymous URL, so it waits its turn like other heavy reads.
func (s *Server) adminListAnonymous(w http.ResponseWriter, r *http.Request, search string, page, perPage int) {
	if search != "" {
		select {
		case s.heavy <- struct{}{}:
			defer func() { <-s.heavy }()
		case <-r.Context().Done():
			return
		}
	}
	urls, total, err := s.store.ListAnonymous(r.Context(), search, page, perPage)
	if err != nil {
		internalError(w, r, err)
		return
	}
	ids := make([]string, len(urls))
	for i, u := range urls {
		ids[i] = u.Token.ID
	}
	activity, err := s.store.Activities(r.Context(), ids)
	if err != nil {
		internalError(w, r, err)
		return
	}
	items := make([]adminURL, len(urls))
	for i, u := range urls {
		v := s.view(r, u.Token, activity[i].Requests)
		v.ExpiresAt = &u.ExpiresAt
		items[i] = adminURL{tokenView: v, LatestRequestAt: activity[i].LastRequestAt}
	}
	writeJSON(w, http.StatusOK, pageOf(items, len(items), total, page, perPage))
}

// urlLookup is what the panel knows about any UUID: a live URL and its
// owner, the record of a gone one, both or neither.
type urlLookup struct {
	Token  *tokenView      `json:"token"`
	Owner  *accounts.Owner `json:"owner"`
	Record *store.Record   `json:"record"`
}

// findURL loads a URL, anonymous or owned, and its owner, without extending
// its life. Both are nil when it does not exist.
func (s *Server) findURL(ctx context.Context, id string) (*store.Token, *accounts.Owner, error) {
	switch o, err := s.accounts.AdminURL(ctx, id); {
	case err == nil:
		return o.Token, &o.Owner, nil
	case !errors.Is(err, accounts.ErrNotFound):
		return nil, nil, err
	}
	t, err := s.store.PeekToken(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if !t.Owned() {
		return t, nil, nil
	}
	// A cached copy whose row is gone: name the owner if they still exist.
	switch u, err := s.accounts.UserByID(ctx, t.OwnerID); {
	case err == nil:
		return t, &accounts.Owner{ID: u.ID, Email: u.Email}, nil
	case !errors.Is(err, accounts.ErrNotFound):
		return nil, nil, err
	}
	return t, nil, nil
}

// lookupURL is findURL together with the URL's audit record.
func (s *Server) lookupURL(r *http.Request, id string) (urlLookup, error) {
	ctx := r.Context()
	t, owner, err := s.findURL(ctx, id)
	if err != nil {
		return urlLookup{}, err
	}
	rec, err := s.store.GetRecord(ctx, id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return urlLookup{}, err
	}
	l := urlLookup{Owner: owner, Record: rec}
	if t != nil {
		n, err := s.store.CountRequests(ctx, t.ID)
		if err != nil {
			return urlLookup{}, err
		}
		l.Token = s.view(r, t, n)
		if rec != nil && rec.Status == "expired" { // an owned URL missing from the cache is still live
			rec.Status = "live"
		}
		if !t.Owned() { // the view takes this read for a use, which a lookup is not
			at, err := s.store.ExpiresAt(ctx, t.ID)
			if err != nil {
				return urlLookup{}, err
			}
			l.Token.ExpiresAt = &at
		}
	}
	return l, nil
}

// adminLookupURL is GET /api/admin/urls/{id}: what is known about any UUID,
// anonymous, owned, deleted or never seen, without extending its life.
func (s *Server) adminLookupURL(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "URL not found")
	if !ok {
		return
	}
	l, err := s.lookupURL(r, id)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

// deleteURL removes a URL on the admin's behalf, through the token layer
// like any other delete, and logs it. It returns what the log row holds.
func (s *Server) deleteURL(r *http.Request, t *store.Token) (map[string]any, error) {
	ctx := r.Context()
	details := map[string]any{"description": t.Description}
	if t.Owned() {
		details["owner_id"] = t.OwnerID
		switch u, err := s.accounts.UserByID(ctx, t.OwnerID); {
		case err == nil:
			details["owner_email"] = u.Email
		case !errors.Is(err, accounts.ErrNotFound):
			return nil, err
		}
	}
	if err := s.tokens.delete(ctx, t, s.clientIP(r), r.UserAgent()); err != nil {
		return nil, err
	}
	s.publish(ctx, t.ID, event{Type: "token.deleted"})
	return details, s.logAdmin(r, accounts.ActionURLDelete, accounts.TargetURL, t.ID, details)
}

// adminDeleteURL is DELETE /api/admin/urls/{id}: an anonymous or owned URL.
func (s *Server) adminDeleteURL(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "URL not found")
	if !ok {
		return
	}
	t, err := s.tokens.read(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "URL not found or expired")
		return
	}
	if err == nil {
		_, err = s.deleteURL(r, t)
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Reports ----------------------------------------------------------------

// reportGroup is the reports of one URL together with what the panel knows
// about the URL now.
type reportGroup struct {
	*accounts.ReportGroup
	urlLookup
}

// adminListReports is GET /api/admin/reports: the URLs reported from the
// /abuse page, grouped by URL, the one with the newest matching report
// first, with ?status (open, the default; actioned; dismissed; all), ?page
// and ?per_page.
func (s *Server) adminListReports(w http.ResponseWriter, r *http.Request) {
	page, perPage := paging(r)
	status := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	switch status {
	case "":
		status = accounts.ReportOpen
	case "all":
		status = ""
	case accounts.ReportOpen, accounts.ReportActioned, accounts.ReportDismissed:
	default:
		writeError(w, http.StatusUnprocessableEntity, "status must be open, actioned, dismissed or all")
		return
	}
	groups, total, err := s.accounts.ListReports(r.Context(), status, page, perPage)
	if err != nil {
		internalError(w, r, err)
		return
	}
	items := make([]reportGroup, len(groups))
	for i, g := range groups {
		l, err := s.lookupURL(r, g.TokenID)
		if err != nil {
			internalError(w, r, err)
			return
		}
		items[i] = reportGroup{ReportGroup: g, urlLookup: l}
	}
	writeJSON(w, http.StatusOK, pageOf(items, len(items), total, page, perPage))
}

// adminResolveReports is POST /api/admin/reports/resolve: the admin acts on
// a reported URL (deletes it, disables its owner, or dismisses the reports)
// and every open report of it is closed, with a note.
func (s *Server) adminResolveReports(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TokenID string `json:"token_id"`
		Action  string `json:"action"`
		Note    string `json:"note"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	id, action, note := strings.ToLower(strings.TrimSpace(in.TokenID)), strings.ToLower(strings.TrimSpace(in.Action)), strings.TrimSpace(in.Note)
	switch {
	case !isUUID(id):
		writeError(w, http.StatusUnprocessableEntity, "token_id must be a URL ID")
		return
	case action != accounts.ResolveDeleteURL && action != accounts.ResolveDisableOwner && action != accounts.ResolveDismiss:
		writeError(w, http.StatusUnprocessableEntity, "action must be delete_url, disable_owner or dismiss")
		return
	case utf8.RuneCountInString(note) > maxReportMessage:
		writeError(w, http.StatusUnprocessableEntity, "note must be at most 2000 characters")
		return
	}
	ctx := r.Context()
	if reported, err := s.accounts.Reported(ctx, id); err != nil {
		internalError(w, r, err)
		return
	} else if !reported {
		writeError(w, http.StatusNotFound, "No reports for this URL")
		return
	}
	details := map[string]any{}
	switch action {
	case accounts.ResolveDeleteURL:
		t, err := s.tokens.read(ctx, id)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			internalError(w, r, err)
			return
		}
		details["deleted"] = err == nil // false: it was already gone
		if t != nil {
			d, err := s.deleteURL(r, t)
			if err != nil {
				internalError(w, r, err)
				return
			}
			maps.Copy(details, d)
		}
	case accounts.ResolveDisableOwner:
		_, owner, err := s.findURL(ctx, id)
		if err != nil {
			internalError(w, r, err)
			return
		}
		if owner == nil {
			writeError(w, http.StatusConflict, "This URL has no owner")
			return
		}
		disabled := true
		if _, err := s.accounts.UpdateUser(ctx, userFrom(r), owner.ID, accounts.UserUpdate{Disabled: &disabled}, s.clientIP(r)); s.userError(w, r, err) {
			return
		}
		details["owner_id"], details["owner_email"] = owner.ID, owner.Email
	}
	n, err := s.accounts.ResolveReports(ctx, userFrom(r), accounts.Resolution{TokenID: id, Action: action, Note: note, Details: details, IP: s.clientIP(r)})
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated": n})
}

// --- Log --------------------------------------------------------------------

// adminLog is GET /api/admin/log: the log, newest first, with ?action,
// ?actor (an email or user ID), ?page and ?per_page.
func (s *Server) adminLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, perPage := paging(r)
	f := accounts.LogFilter{Action: strings.TrimSpace(q.Get("action")), Actor: strings.TrimSpace(q.Get("actor"))}
	entries, total, err := s.accounts.ListLog(r.Context(), f, page, perPage)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pageOf(entries, len(entries), total, page, perPage))
}
