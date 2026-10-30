package accounts

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/dbaio/webhookgo/internal/auth"
)

// CreateLink stores a one-time link and returns its secret, which goes in
// the URL fragment given to the person.
func (s *Store) CreateLink(ctx context.Context, l NewLink) (string, error) {
	role := l.Role
	if role == "" {
		role = RoleUser
	}
	switch l.Kind {
	case LinkSetup:
		role = RoleAdmin
	case LinkInvite:
	case LinkReset:
		if l.UserID == "" {
			return "", errors.New("a reset link needs a user")
		}
	default:
		return "", fmt.Errorf("unknown link kind %q", l.Kind)
	}
	if role != RoleAdmin && role != RoleUser {
		return "", fmt.Errorf("unknown role %q", role)
	}
	secret, hash := auth.NewSecret()
	_, err := s.pool.Exec(ctx, `insert into one_time_links (hash, kind, user_id, email, role, created_by, expires_at)
		values ($1, $2, nullif($3, '')::uuid, nullif($4, ''), $5, nullif($6, '')::uuid, now() + make_interval(secs => $7))`,
		hash, l.Kind, l.UserID, l.Email, role, l.CreatedBy, l.TTL.Seconds())
	if err != nil {
		return "", err
	}
	return secret, nil
}

// DeleteUnusedSetupLinks drops setup links that were never redeemed, so only
// the one logged at the latest start is valid.
func (s *Store) DeleteUnusedSetupLinks(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, "delete from one_time_links where kind = $1 and used_at is null", LinkSetup)
	return err
}

const linkColumns = `l.kind, coalesce(l.email, u.email, ''), l.role, coalesce(l.user_id::text, ''), l.expires_at
	from one_time_links l left join users u on u.id = l.user_id`

func scanLink(row pgx.Row) (*Link, error) {
	var l Link
	err := row.Scan(&l.Kind, &l.Email, &l.Role, &l.UserID, &l.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	l.ExpiresAt = l.ExpiresAt.UTC()
	return &l, nil
}

// InspectLink describes a link that can still be redeemed, or ErrNotFound.
func (s *Store) InspectLink(ctx context.Context, token string) (*Link, error) {
	l, err := scanLink(s.pool.QueryRow(ctx, "select "+linkColumns+" where l.hash = $1 and l.used_at is null and l.expires_at > now()", auth.HashSecret(token)))
	if err != nil {
		return nil, err
	}
	if l.Kind == LinkSetup {
		if n, err := s.ActiveAdmins(ctx); err != nil {
			return nil, err
		} else if n > 0 {
			return nil, ErrNotFound
		}
	}
	return l, nil
}

// Redeem uses a link once: it creates the admin (setup) or the user (invite),
// or sets the password and ends every session (reset), then signs the person
// in. Everything happens in one transaction, with the link row locked, so a
// link cannot be used twice. It returns the user and the session secret.
func (s *Store) Redeem(ctx context.Context, token string, in Redeem) (*User, string, error) {
	hash := auth.HashSecret(token)
	var (
		u      *User
		secret string
	)
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		l, err := scanLink(tx.QueryRow(ctx, "select "+linkColumns+" where l.hash = $1 and l.used_at is null and l.expires_at > now() for update of l", hash))
		if err != nil {
			return err
		}
		switch l.Kind {
		case LinkSetup:
			if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", setupLock); err != nil {
				return err
			}
			if n, err := activeAdmins(ctx, tx); err != nil {
				return err
			} else if n > 0 {
				return ErrNotFound
			}
			u, err = insertUser(ctx, tx, in.Email, in.Name, in.PasswordHash, RoleAdmin)
		case LinkInvite:
			u, err = insertUser(ctx, tx, l.Email, in.Name, in.PasswordHash, l.Role)
		case LinkReset:
			u, err = scanUser(tx.QueryRow(ctx, "update users set password_hash = $2 where id = $1 returning "+userColumns, l.UserID, in.PasswordHash))
			if err == nil {
				_, err = tx.Exec(ctx, "delete from sessions where user_id = $1", l.UserID)
			}
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "update one_time_links set used_at = now() where hash = $1", hash); err != nil {
			return err
		}
		secret, err = createSession(ctx, tx, u, in.IP, in.UserAgent, in.SessionTTL)
		return err
	})
	if err != nil {
		return nil, "", err
	}
	return u, secret, nil
}
