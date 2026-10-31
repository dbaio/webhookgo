package accounts

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dbaio/webhookgo/internal/auth"
)

// maxUserAgent caps the user agent kept with a session.
const maxUserAgent = 512

// CreateSession starts a session for a user who just signed in and returns
// the secret for the cookie. It also records the login on the user.
func (s *Store) CreateSession(ctx context.Context, u *User, ip, userAgent string, ttl time.Duration) (string, error) {
	var secret string
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		secret, err = createSession(ctx, tx, u, ip, userAgent, ttl)
		return err
	})
	return secret, err
}

func createSession(ctx context.Context, q querier, u *User, ip, userAgent string, ttl time.Duration) (string, error) {
	secret, hash := auth.NewSecret()
	if len(userAgent) > maxUserAgent {
		userAgent = userAgent[:maxUserAgent]
	}
	if _, err := q.Exec(ctx, `insert into sessions (id_hash, user_id, created_at, last_seen_at, expires_at, ip, user_agent)
		values ($1, $2, now(), now(), now() + make_interval(secs => $3), $4, $5)`,
		hash, u.ID, ttl.Seconds(), ip, userAgent); err != nil {
		return "", err
	}
	var last time.Time
	if err := q.QueryRow(ctx, "update users set last_login_at = now() where id = $1 returning last_login_at", u.ID).Scan(&last); err != nil {
		return "", err
	}
	last = last.UTC()
	u.LastLoginAt = &last
	return secret, nil
}

// SessionUser resolves a session secret to its user, or ErrNotFound when the
// session is unknown or expired, or the user is disabled. A live session
// slides: its expiry moves to now + ttl, at most once a minute.
func (s *Store) SessionUser(ctx context.Context, secret string, ttl time.Duration) (*User, error) {
	hash := auth.HashSecret(secret)
	var u User
	var stale bool
	err := s.pool.QueryRow(ctx, `select u.id, u.email, u.name, u.password_hash, u.role, u.disabled_at, u.created_at, u.last_login_at,
			s.last_seen_at < now() - interval '1 minute'
		from sessions s join users u on u.id = s.user_id
		where s.id_hash = $1 and s.expires_at > now()`, hash).
		Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.DisabledAt, &u.CreatedAt, &u.LastLoginAt, &stale)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if u.Disabled() {
		return nil, ErrNotFound
	}
	if stale {
		if _, err := s.pool.Exec(ctx, "update sessions set last_seen_at = now(), expires_at = now() + make_interval(secs => $2) where id_hash = $1",
			hash, ttl.Seconds()); err != nil {
			return nil, err
		}
	}
	u.CreatedAt = u.CreatedAt.UTC()
	if u.LastLoginAt != nil {
		t := u.LastLoginAt.UTC()
		u.LastLoginAt = &t
	}
	return &u, nil
}

// DeleteSession ends one session; an unknown secret is not an error.
func (s *Store) DeleteSession(ctx context.Context, secret string) error {
	_, err := s.pool.Exec(ctx, "delete from sessions where id_hash = $1", auth.HashSecret(secret))
	return err
}

// DeleteOtherSessions ends every session of the user but the one
// identified by keepSecret.
func (s *Store) DeleteOtherSessions(ctx context.Context, userID, keepSecret string) error {
	_, err := s.pool.Exec(ctx, "delete from sessions where user_id = $1 and id_hash <> $2", userID, auth.HashSecret(keepSecret))
	return err
}

// DeleteExpired removes the sessions that expired, with their IP address
// and user agent, and the one-time links nobody used in time, with the
// address they were meant for. It reports how many rows went.
func (s *Store) DeleteExpired(ctx context.Context) (int64, error) {
	var n int64
	for _, q := range []string{
		"delete from sessions where expires_at <= now()",
		"delete from one_time_links where used_at is null and expires_at <= now()",
	} {
		tag, err := s.pool.Exec(ctx, q)
		if err != nil {
			return n, err
		}
		n += tag.RowsAffected()
	}
	return n, nil
}
