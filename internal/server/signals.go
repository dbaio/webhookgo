package server

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/dbaio/webhookgo/internal/store"
)

// The admin panel looks for abuse among the anonymous URLs with a few
// signals: a URL that answers with its own page (phishing, the usual abuse
// of a webhook inspector), the URLs holding the most requests, and the
// addresses that created the most URLs.

// servesPage reports whether a URL answers with its own HTML, XML or SVG
// page, as the /abuse page flags it.
func servesPage(t *store.Token) bool {
	ct := strings.ToLower(t.DefaultContentType)
	return (strings.Contains(ct, "html") || strings.Contains(ct, "xml") || strings.Contains(ct, "svg")) &&
		strings.TrimSpace(t.DefaultContent) != ""
}

// anonymousQuery narrows or orders the list of anonymous URLs.
type anonymousQuery struct {
	search     string // in the name, ignoring case, or exactly the ID or creator IP
	pagesOnly  bool   // only those that serve a page
	byRequests bool   // the most requests first, rather than the newest
}

func (q anonymousQuery) scans() bool { return q.search != "" || q.pagesOnly || q.byRequests }

func (q anonymousQuery) match(t *store.Token) bool {
	if q.pagesOnly && !servesPage(t) {
		return false
	}
	if q.search == "" {
		return true
	}
	lower := strings.ToLower(q.search)
	return t.ID == lower || t.IP == q.search || strings.Contains(strings.ToLower(t.Description), lower)
}

// adminListAnonymous answers GET /api/admin/urls?kind=anonymous, with ?q,
// ?pages=1 (only URLs that serve a page) and ?sort=requests (the most
// requests first). Any of those reads every anonymous URL, so it waits its
// turn like other heavy reads.
func (s *Server) adminListAnonymous(w http.ResponseWriter, r *http.Request, page, perPage int) {
	params := r.URL.Query()
	q := anonymousQuery{search: strings.TrimSpace(params.Get("q")), pagesOnly: params.Get("pages") == "1"}
	switch params.Get("sort") {
	case "", "newest":
	case "requests":
		q.byRequests = true
	default:
		writeError(w, http.StatusUnprocessableEntity, "sort must be newest or requests")
		return
	}
	var (
		urls     []store.AnonymousURL
		activity []store.Activity
		total    int
		err      error
	)
	if q.scans() {
		if !s.waitHeavy(r.Context()) {
			return
		}
		defer func() { <-s.heavy }()
		urls, activity, total, err = s.scanAnonymous(r.Context(), q, page, perPage)
	} else if urls, total, err = s.store.ListAnonymous(r.Context(), page, perPage); err == nil {
		activity, err = s.store.Activities(r.Context(), anonymousIDs(urls))
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	items := make([]adminURL, len(urls))
	for i, u := range urls {
		v := s.view(r, u.Token, activity[i].Requests)
		v.ExpiresAt = &u.ExpiresAt
		items[i] = adminURL{tokenView: v, LatestRequestAt: activity[i].LastRequestAt, ServesPage: servesPage(u.Token)}
	}
	writeJSON(w, http.StatusOK, pageOf(items, len(items), total, page, perPage))
}

// waitHeavy takes a turn among the heavy reads, or reports that the request
// was abandoned while it waited. The caller gives the turn back.
func (s *Server) waitHeavy(ctx context.Context) bool {
	select {
	case s.heavy <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func anonymousIDs(urls []store.AnonymousURL) []string {
	ids := make([]string, len(urls))
	for i, u := range urls {
		ids[i] = u.Token.ID
	}
	return ids
}

// scanAnonymous reads every anonymous URL for a list that searches, filters
// or orders by requests, and returns one page of it with its activity.
func (s *Server) scanAnonymous(ctx context.Context, q anonymousQuery, page, perPage int) ([]store.AnonymousURL, []store.Activity, int, error) {
	var (
		urls     []store.AnonymousURL
		activity []store.Activity // of every URL in urls, when ordering by requests
	)
	err := s.store.EachAnonymous(ctx, func(chunk []store.AnonymousURL) error {
		var keep []store.AnonymousURL
		for _, u := range chunk {
			if q.match(u.Token) {
				keep = append(keep, u)
			}
		}
		if q.byRequests && len(keep) > 0 {
			a, err := s.store.Activities(ctx, anonymousIDs(keep))
			if err != nil {
				return err
			}
			activity = append(activity, a...)
		}
		urls = append(urls, keep...)
		return nil
	})
	if err != nil {
		return nil, nil, 0, err
	}
	total := len(urls)
	if q.byRequests {
		order := make([]int, total)
		for i := range order {
			order[i] = i
		}
		// Stable: among equals, the newest first, as read.
		slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(activity[b].Requests, activity[a].Requests) })
		sorted, acts := make([]store.AnonymousURL, total), make([]store.Activity, total)
		for i, j := range order {
			sorted[i], acts[i] = urls[j], activity[j]
		}
		urls, activity = sorted, acts
	}
	lo, hi := min((page-1)*perPage, total), min(page*perPage, total)
	urls = urls[lo:hi]
	if q.byRequests {
		return urls, activity[lo:hi], total, nil
	}
	a, err := s.store.Activities(ctx, anonymousIDs(urls))
	return urls, a, total, err
}

// creatorStats is what the anonymous URLs created from one address add up to.
type creatorStats struct {
	IP       string    `json:"ip"`
	URLs     int       `json:"urls"`
	Pages    int       `json:"pages"`    // of those, the ones that serve a page
	Requests int64     `json:"requests"` // stored in them now
	LatestAt time.Time `json:"latest_created_at"`
}

// topCreators is how many addresses GET /api/admin/ips lists.
const topCreators = 20

// adminTopIPs is GET /api/admin/ips: the addresses that created the most
// live anonymous URLs, and how many URLs it read. It reads every anonymous
// URL, so it waits its turn like other heavy reads.
func (s *Server) adminTopIPs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !s.waitHeavy(ctx) {
		return
	}
	defer func() { <-s.heavy }()
	byIP := map[string]*creatorStats{}
	read := 0
	err := s.store.EachAnonymous(ctx, func(chunk []store.AnonymousURL) error {
		activity, err := s.store.Activities(ctx, anonymousIDs(chunk))
		if err != nil {
			return err
		}
		for i, u := range chunk {
			read++
			c := byIP[u.Token.IP]
			if c == nil {
				c = &creatorStats{IP: u.Token.IP}
				byIP[u.Token.IP] = c
			}
			c.URLs++
			c.Requests += activity[i].Requests
			if servesPage(u.Token) {
				c.Pages++
			}
			if u.Token.CreatedAt.After(c.LatestAt) {
				c.LatestAt = u.Token.CreatedAt
			}
		}
		return nil
	})
	if err != nil {
		internalError(w, r, err)
		return
	}
	top := make([]creatorStats, 0, len(byIP))
	for _, c := range byIP {
		top = append(top, *c)
	}
	slices.SortFunc(top, func(a, b creatorStats) int {
		return cmp.Or(cmp.Compare(b.URLs, a.URLs), cmp.Compare(b.Requests, a.Requests), strings.Compare(a.IP, b.IP))
	})
	writeJSON(w, http.StatusOK, map[string]any{"data": top[:min(len(top), topCreators)], "urls": read})
}
