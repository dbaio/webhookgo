package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dbaio/webhookgo/internal/store"
)

var (
	ErrLastAdmin = errors.New("the last active admin cannot be lost")
	ErrSelf      = errors.New("admins cannot change their own account")
)

// adminLock serialises the changes that can lower the number of active
// admins, so two admins cannot each remove the other at the same time.
const adminLock = 0x7767_6164 // "wgad"

// Actors of log entries that are not signed-in users.
const (
	ActorCLI   = "cli"
	ActorSetup = "setup"
)

// What a log entry is about.
const (
	TargetUser = "user"
	TargetURL  = "url"
	TargetLink = "link"
)

// Logged actions.
const (
	ActionSetup         = "setup" // the first admin was created
	ActionUserInvite    = "user.invite"
	ActionUserRole      = "user.role"
	ActionUserDisable   = "user.disable"
	ActionUserEnable    = "user.enable"
	ActionUserReset     = "user.reset_link"
	ActionUserDelete    = "user.delete"
	ActionURLDelete     = "url.delete"
	ActionURLView       = "url.view" // an admin opened someone else's private URL
	ActionCLIInvite     = "cli.invite"
	ActionCLIReset      = "cli.reset"
	ActionReportResolve = "report.resolve" // the abuse reports of a URL were closed
)

// --- Log --------------------------------------------------------------------

// LogEntry is one row of the admin log, which is only ever appended to.
type LogEntry struct {
	ID         string          `json:"id"`
	ActorID    string          `json:"actor_id"` // "" for the CLI and setup, or once the actor is deleted
	ActorEmail string          `json:"actor_email"`
	Action     string          `json:"action"`
	TargetType string          `json:"target_type"`
	TargetID   string          `json:"target_id"`
	Details    json.RawMessage `json:"details"`
	IP         string          `json:"ip"`
	CreatedAt  time.Time       `json:"created_at"`
}

// NewLogEntry describes an action to log.
type NewLogEntry struct {
	ActorID    string // "" for the CLI and setup
	ActorEmail string
	Action     string
	TargetType string
	TargetID   string
	Details    map[string]any
	IP         string
}

// Log records an admin action.
func (s *Store) Log(ctx context.Context, e NewLogEntry) error {
	return logEntry(ctx, s.pool, e)
}

func logEntry(ctx context.Context, q querier, e NewLogEntry) error {
	details := e.Details
	if details == nil {
		details = map[string]any{}
	}
	b, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `insert into admin_log (id, actor_id, actor_email, action, target_type, target_id, details, ip)
		values ($1, nullif($2, '')::uuid, $3, $4, $5, $6, $7::jsonb, nullif($8, ''))`,
		uuid.Must(uuid.NewV7()).String(), e.ActorID, e.ActorEmail, e.Action, e.TargetType, e.TargetID, string(b), e.IP)
	return err
}

// LogFilter narrows a page of the log; empty fields match everything. Actor
// is matched against the actor's email (ignoring case) and id.
type LogFilter struct {
	Action string
	Actor  string
}

// ListLog returns one page of the admin log, newest first, and how many
// entries match.
func (s *Store) ListLog(ctx context.Context, f LogFilter, page, perPage int) ([]*LogEntry, int, error) {
	const where = "where ($1 = '' or action = $1) and ($2 = '' or lower(actor_email) = lower($2) or actor_id::text = lower($2))"
	var total int
	if err := s.pool.QueryRow(ctx, "select count(*) from admin_log "+where, f.Action, f.Actor).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `select id, coalesce(actor_id::text, ''), actor_email, action, target_type, target_id, details::text, coalesce(ip, ''), created_at
		from admin_log `+where+" order by created_at desc, id desc limit $3 offset $4", f.Action, f.Actor, perPage, (page-1)*perPage)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	entries := []*LogEntry{}
	for rows.Next() {
		var (
			e       LogEntry
			details string
		)
		if err := rows.Scan(&e.ID, &e.ActorID, &e.ActorEmail, &e.Action, &e.TargetType, &e.TargetID, &details, &e.IP, &e.CreatedAt); err != nil {
			return nil, 0, err
		}
		e.Details = json.RawMessage(details)
		e.CreatedAt = e.CreatedAt.UTC()
		entries = append(entries, &e)
	}
	return entries, total, rows.Err()
}

// --- Users ------------------------------------------------------------------

// AdminUser is a user as the panel sees it: with the disabled mark and how
// many URLs the account owns.
type AdminUser struct {
	*User
	DisabledAt *time.Time `json:"disabled_at"`
	URLCount   int        `json:"url_count"`
}

const adminUserColumns = "u.id, u.email, u.name, u.password_hash, u.role, u.disabled_at, u.created_at, u.last_login_at, (select count(*) from urls where owner_id = u.id)"

func scanAdminUser(row pgx.Row) (*AdminUser, error) {
	var (
		u User
		n int
	)
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.DisabledAt, &u.CreatedAt, &u.LastLoginAt, &n)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.CreatedAt, u.HasPassword = u.CreatedAt.UTC(), u.PasswordHash != ""
	for _, t := range []**time.Time{&u.LastLoginAt, &u.DisabledAt} {
		if *t != nil {
			utc := (*t).UTC()
			*t = &utc
		}
	}
	return &AdminUser{User: &u, DisabledAt: u.DisabledAt, URLCount: n}, nil
}

func (s *Store) AdminUserByID(ctx context.Context, id string) (*AdminUser, error) {
	return scanAdminUser(s.pool.QueryRow(ctx, "select "+adminUserColumns+" from users u where u.id = $1", id))
}

// ListAdminUsers returns one page of the accounts, newest first, and how
// many match. A non-empty search keeps those whose email or name contains
// it, ignoring case.
func (s *Store) ListAdminUsers(ctx context.Context, search string, page, perPage int) ([]*AdminUser, int, error) {
	const where = "where $1 = '' or strpos(lower(u.email), lower($1)) > 0 or strpos(lower(u.name), lower($1)) > 0"
	var total int
	if err := s.pool.QueryRow(ctx, "select count(*) from users u "+where, search).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, "select "+adminUserColumns+" from users u "+where+" order by u.created_at desc, u.email limit $2 offset $3",
		search, perPage, (page-1)*perPage)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	users := []*AdminUser{}
	for rows.Next() {
		u, err := scanAdminUser(rows)
		if err != nil {
			return nil, 0, err
		}
		users = append(users, u)
	}
	return users, total, rows.Err()
}

// UserUpdate is what an admin may change on an account; a nil field is left
// as it is.
type UserUpdate struct {
	Role     *string
	Disabled *bool
}

// UpdateUser applies in to the account id on behalf of actor and logs each
// change. Disabling ends the user's sessions. An admin cannot change their
// own account (ErrSelf), and the last active admin can be neither demoted
// nor disabled (ErrLastAdmin).
func (s *Store) UpdateUser(ctx context.Context, actor *User, id string, in UserUpdate, ip string) (*AdminUser, error) {
	if actor.ID == id {
		return nil, ErrSelf
	}
	var out *AdminUser
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		u, err := lockAdminUser(ctx, tx, id)
		if err != nil {
			return err
		}
		role, disabled := u.Role, u.Disabled()
		if in.Role != nil {
			role = *in.Role
		}
		if in.Disabled != nil {
			disabled = *in.Disabled
		}
		if u.Role == RoleAdmin && !u.Disabled() && (role != RoleAdmin || disabled) {
			if err := keepOneAdmin(ctx, tx); err != nil {
				return err
			}
		}
		entry := NewLogEntry{ActorID: actor.ID, ActorEmail: actor.Email, TargetType: TargetUser, TargetID: u.ID, IP: ip}
		if role != u.Role {
			if _, err := tx.Exec(ctx, "update users set role = $2 where id = $1", id, role); err != nil {
				return err
			}
			entry.Action, entry.Details = ActionUserRole, map[string]any{"email": u.Email, "from": u.Role, "to": role}
			if err := logEntry(ctx, tx, entry); err != nil {
				return err
			}
		}
		if disabled != u.Disabled() {
			if disabled {
				_, err = tx.Exec(ctx, "update users set disabled_at = now() where id = $1", id)
				if err == nil {
					_, err = tx.Exec(ctx, "delete from sessions where user_id = $1", id)
				}
				entry.Action = ActionUserDisable
			} else {
				_, err = tx.Exec(ctx, "update users set disabled_at = null where id = $1", id)
				entry.Action = ActionUserEnable
			}
			if err != nil {
				return err
			}
			entry.Details = map[string]any{"email": u.Email}
			if err := logEntry(ctx, tx, entry); err != nil {
				return err
			}
		}
		out, err = scanAdminUser(tx.QueryRow(ctx, "select "+adminUserColumns+" from users u where u.id = $1", id))
		return err
	})
	return out, err
}

// DeleteUser removes an account on behalf of actor and logs it, with how
// many URLs went with it. The caller deletes the user's URLs first, through
// the token layer, so their Redis keys go too; sessions, links and any URL
// row left cascade. An admin cannot delete their own account (ErrSelf) or
// the last active admin (ErrLastAdmin).
func (s *Store) DeleteUser(ctx context.Context, actor *User, id string, urls int, ip string) error {
	if actor.ID == id {
		return ErrSelf
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		u, err := lockAdminUser(ctx, tx, id)
		if err != nil {
			return err
		}
		if u.Role == RoleAdmin && !u.Disabled() {
			if err := keepOneAdmin(ctx, tx); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, "delete from users where id = $1", id); err != nil {
			return err
		}
		return logEntry(ctx, tx, NewLogEntry{
			ActorID: actor.ID, ActorEmail: actor.Email, Action: ActionUserDelete, TargetType: TargetUser, TargetID: id,
			Details: map[string]any{"email": u.Email, "role": u.Role, "urls": urls}, IP: ip,
		})
	})
}

// lockAdminUser takes the admin lock and the user's row for the rest of the
// transaction, so the count of active admins cannot change underneath.
func lockAdminUser(ctx context.Context, tx pgx.Tx, id string) (*User, error) {
	if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", adminLock); err != nil {
		return nil, err
	}
	return scanUser(tx.QueryRow(ctx, "select "+userColumns+" from users where id = $1 for update", id))
}

// keepOneAdmin fails with ErrLastAdmin unless another active admin would
// remain after the current one is lost.
func keepOneAdmin(ctx context.Context, tx pgx.Tx) error {
	n, err := activeAdmins(ctx, tx)
	if err != nil {
		return err
	}
	if n <= 1 {
		return ErrLastAdmin
	}
	return nil
}

// --- URLs -------------------------------------------------------------------

// Owner identifies the account a URL belongs to.
type Owner struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// OwnedURL is a URL together with its owner, as the panel lists them.
type OwnedURL struct {
	Token *store.Token
	Owner Owner
}

// OwnerURLs returns every URL an account owns, oldest first.
func (s *Store) OwnerURLs(ctx context.Context, ownerID string) ([]*store.Token, error) {
	rows, err := s.pool.Query(ctx, "select token_id, config from urls where owner_id = $1 order by created_at, token_id", ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tokens := []*store.Token{}
	for rows.Next() {
		var (
			id     string
			config []byte
		)
		if err := rows.Scan(&id, &config); err != nil {
			return nil, err
		}
		t, err := decodeToken(id, config)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

const ownedURLColumns = "l.token_id, l.config, u.id, u.email from urls l join users u on u.id = l.owner_id"

// AdminURL loads a URL that belongs to an account, with its owner.
func (s *Store) AdminURL(ctx context.Context, id string) (*OwnedURL, error) {
	var (
		o      OwnedURL
		tid    string
		config []byte
	)
	err := s.pool.QueryRow(ctx, "select "+ownedURLColumns+" where l.token_id = $1", id).Scan(&tid, &config, &o.Owner.ID, &o.Owner.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if o.Token, err = decodeToken(tid, config); err != nil {
		return nil, err
	}
	return &o, nil
}

// ListAllURLs returns one page of every URL that belongs to an account,
// newest first, and how many match. A non-empty search keeps those whose
// description contains it, ignoring case, or whose ID or creator IP is
// exactly it; a non-empty owner keeps that account's only.
func (s *Store) ListAllURLs(ctx context.Context, search, ownerID string, page, perPage int) ([]*OwnedURL, int, error) {
	const where = `where ($1 = '' or strpos(lower(l.config->>'description'), lower($1)) > 0 or l.token_id::text = lower($1) or l.config->>'ip' = $1)
		and ($2 = '' or l.owner_id::text = $2)`
	var total int
	if err := s.pool.QueryRow(ctx, "select count(*) from urls l "+where, search, ownerID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, "select "+ownedURLColumns+" "+where+" order by l.created_at desc, l.token_id limit $3 offset $4",
		search, ownerID, perPage, (page-1)*perPage)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	urls := []*OwnedURL{}
	for rows.Next() {
		var (
			o      OwnedURL
			id     string
			config []byte
		)
		if err := rows.Scan(&id, &config, &o.Owner.ID, &o.Owner.Email); err != nil {
			return nil, 0, err
		}
		if o.Token, err = decodeToken(id, config); err != nil {
			return nil, 0, err
		}
		urls = append(urls, &o)
	}
	return urls, total, rows.Err()
}

// --- Stats ------------------------------------------------------------------

// Stats is what the panel's overview shows about Postgres.
type Stats struct {
	Users, Admins, Disabled int   // accounts, active admins among them, disabled accounts
	URLs                    int   // URLs that belong to accounts
	OpenReports             int   // abuse reports nobody has handled yet
	DatabaseBytes           int64 // pg_database_size(current_database())
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	err := s.pool.QueryRow(ctx, `select count(*), count(*) filter (where role = $1 and disabled_at is null), count(*) filter (where disabled_at is not null),
		(select count(*) from urls), (select count(*) from abuse_reports where status = $2), pg_database_size(current_database()) from users`, RoleAdmin, ReportOpen).
		Scan(&st.Users, &st.Admins, &st.Disabled, &st.URLs, &st.OpenReports, &st.DatabaseBytes)
	return st, err
}
