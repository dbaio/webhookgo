package accounts

import (
	"context"
	"maps"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// What an abuse report's status may be.
const (
	ReportOpen      = "open"
	ReportActioned  = "actioned"  // an admin deleted the URL or disabled its owner
	ReportDismissed = "dismissed" // an admin found nothing to do
)

// How an admin resolves the reports of a URL.
const (
	ResolveDeleteURL    = "delete_url"
	ResolveDisableOwner = "disable_owner"
	ResolveDismiss      = "dismiss"
)

// reportDedupe is how long one address's report of a URL stands in for any
// further ones.
const reportDedupe = 24 * time.Hour

// Report is one abuse report of a URL, sent from the public /abuse page.
type Report struct {
	ID             string     `json:"id"`
	ReporterIP     string     `json:"reporter_ip"`
	Contact        string     `json:"contact"`
	Message        string     `json:"message"`
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"created_at"`
	HandledByEmail string     `json:"handled_by_email"` // "" while open, or once the admin is deleted
	HandledAt      *time.Time `json:"handled_at"`
	Note           string     `json:"note"`
}

// NewReport describes a report to store.
type NewReport struct {
	TokenID    string
	ReporterIP string
	Contact    string
	Message    string
}

// AddReport stores a report, unless the same address already reported the
// URL in the last 24 hours, and reports whether it was stored.
func (s *Store) AddReport(ctx context.Context, r NewReport) (bool, error) {
	var stored bool
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var dup bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from abuse_reports where token_id = $1 and reporter_ip = $2 and created_at > $3)",
			r.TokenID, r.ReporterIP, time.Now().Add(-reportDedupe)).Scan(&dup); err != nil {
			return err
		}
		if dup {
			return nil
		}
		_, err := tx.Exec(ctx, "insert into abuse_reports (id, token_id, reporter_ip, contact, message) values ($1, $2, $3, $4, $5)",
			uuid.Must(uuid.NewV7()).String(), r.TokenID, r.ReporterIP, r.Contact, r.Message)
		stored = err == nil
		return err
	})
	return stored, err
}

// ReportGroup is every report of one URL, newest first, as the panel lists
// them.
type ReportGroup struct {
	TokenID string    `json:"token_id"`
	Reports []*Report `json:"reports"`
	Open    int       `json:"open"` // reports still open
	FirstAt time.Time `json:"first_at"`
	LastAt  time.Time `json:"last_at"`
}

// ListReports returns one page of the URLs that have a report with status
// ("" = any), the one with the newest such report first, each with every
// report it got, and how many URLs match.
func (s *Store) ListReports(ctx context.Context, status string, page, perPage int) ([]*ReportGroup, int, error) {
	const where = "where $1 = '' or status = $1"
	var total int
	if err := s.pool.QueryRow(ctx, "select count(distinct token_id) from abuse_reports "+where, status).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, "select token_id, max(created_at) as last_at from abuse_reports "+where+
		" group by token_id order by last_at desc, token_id limit $2 offset $3", status, perPage, (page-1)*perPage)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	groups := []*ReportGroup{}
	byToken := map[string]*ReportGroup{}
	ids := []string{}
	for rows.Next() {
		var (
			g      ReportGroup
			lastAt time.Time
		)
		if err := rows.Scan(&g.TokenID, &lastAt); err != nil {
			return nil, 0, err
		}
		g.Reports = []*Report{}
		groups = append(groups, &g)
		byToken[g.TokenID] = &g
		ids = append(ids, g.TokenID)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if len(ids) == 0 {
		return groups, total, nil
	}
	reports, err := s.pool.Query(ctx, `select r.token_id, r.id, r.reporter_ip, r.contact, r.message, r.status, r.created_at, coalesce(u.email, ''), r.handled_at, r.note
		from abuse_reports r left join users u on u.id = r.handled_by where r.token_id = any($1) order by r.created_at desc, r.id desc`, ids)
	if err != nil {
		return nil, 0, err
	}
	defer reports.Close()
	for reports.Next() {
		var (
			r       Report
			tokenID string
		)
		if err := reports.Scan(&tokenID, &r.ID, &r.ReporterIP, &r.Contact, &r.Message, &r.Status, &r.CreatedAt, &r.HandledByEmail, &r.HandledAt, &r.Note); err != nil {
			return nil, 0, err
		}
		r.CreatedAt = r.CreatedAt.UTC()
		if r.HandledAt != nil {
			at := r.HandledAt.UTC()
			r.HandledAt = &at
		}
		g := byToken[tokenID]
		g.Reports = append(g.Reports, &r)
		if r.Status == ReportOpen {
			g.Open++
		}
		// Newest first: the first row seen is the last report.
		if g.LastAt.IsZero() {
			g.LastAt = r.CreatedAt
		}
		g.FirstAt = r.CreatedAt
	}
	return groups, total, reports.Err()
}

// Reported reports whether a URL has any report, open or not.
func (s *Store) Reported(ctx context.Context, tokenID string) (bool, error) {
	var yes bool
	err := s.pool.QueryRow(ctx, "select exists (select 1 from abuse_reports where token_id = $1)", tokenID).Scan(&yes)
	return yes, err
}

// Resolution is how an admin closed the reports of a URL.
type Resolution struct {
	TokenID string
	Action  string         // ResolveDeleteURL, ResolveDisableOwner or ResolveDismiss
	Note    string         // the admin's note, kept with each report
	Details map[string]any // what the action did, for the log
	IP      string
}

// ResolveReports closes every open report of a URL on behalf of actor, as
// actioned, or dismissed for ResolveDismiss, and logs it. It returns how
// many reports it closed.
func (s *Store) ResolveReports(ctx context.Context, actor *User, res Resolution) (int, error) {
	status := ReportActioned
	if res.Action == ResolveDismiss {
		status = ReportDismissed
	}
	var n int
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "update abuse_reports set status = $2, handled_by = $3, handled_at = now(), note = $4 where token_id = $1 and status = $5",
			res.TokenID, status, actor.ID, res.Note, ReportOpen)
		if err != nil {
			return err
		}
		n = int(tag.RowsAffected())
		details := map[string]any{"action": res.Action, "reports": n}
		if res.Note != "" {
			details["note"] = res.Note
		}
		maps.Copy(details, res.Details)
		return logEntry(ctx, tx, NewLogEntry{
			ActorID: actor.ID, ActorEmail: actor.Email, Action: ActionReportResolve, TargetType: TargetURL, TargetID: res.TokenID,
			Details: details, IP: res.IP,
		})
	})
	return n, err
}
