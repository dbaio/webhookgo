package accounts

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dbaio/webhookgo/internal/auth"
)

// MaxAPIKeys is how many active keys one user may hold.
const MaxAPIKeys = 20

var ErrTooManyKeys = errors.New("too many API keys")

// keysLock serialises the key count check of one user with their inserts.
const keysLock = 0x7767_6b65 // "wgke"

// APIKey is a key as the API lists it. The key itself is given out once, on
// creation; the database keeps its hash and the first characters.
type APIKey struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

const apiKeyColumns = "id, name, prefix, created_at, last_used_at"

func scanAPIKey(row pgx.Row) (*APIKey, error) {
	var k APIKey
	err := row.Scan(&k.ID, &k.Name, &k.Prefix, &k.CreatedAt, &k.LastUsedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	k.CreatedAt = k.CreatedAt.UTC()
	if k.LastUsedAt != nil {
		t := k.LastUsedAt.UTC()
		k.LastUsedAt = &t
	}
	return &k, nil
}

// CreateAPIKey stores a key for a user and returns it along with the key
// itself, this once. A user may hold at most MaxAPIKeys active keys, else
// ErrTooManyKeys.
func (s *Store) CreateAPIKey(ctx context.Context, userID, name string) (*APIKey, string, error) {
	key, hash := auth.NewAPIKey()
	prefix := key[len(auth.APIKeyPrefix):][:8]
	id := uuid.Must(uuid.NewV7()).String()
	var k *APIKey
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1, hashtext($2))", keysLock, userID); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, "select count(*) from api_keys where user_id = $1 and revoked_at is null", userID).Scan(&n); err != nil {
			return err
		}
		if n >= MaxAPIKeys {
			return ErrTooManyKeys
		}
		var err error
		k, err = scanAPIKey(tx.QueryRow(ctx, "insert into api_keys (id, user_id, name, prefix, hash) values ($1, $2, $3, $4, $5) returning "+apiKeyColumns,
			id, userID, name, prefix, hash))
		return err
	})
	if err != nil {
		return nil, "", err
	}
	return k, key, nil
}

// ListAPIKeys returns the user's keys that are not revoked, newest first.
func (s *Store) ListAPIKeys(ctx context.Context, userID string) ([]*APIKey, error) {
	rows, err := s.pool.Query(ctx, "select "+apiKeyColumns+" from api_keys where user_id = $1 and revoked_at is null order by created_at desc, id desc", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []*APIKey{}
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// RevokeAPIKey marks a key revoked: it stops authenticating at once and no
// longer appears in the list. A key that is not the user's, or is already
// revoked, gives ErrNotFound.
func (s *Store) RevokeAPIKey(ctx context.Context, userID, id string) error {
	tag, err := s.pool.Exec(ctx, "update api_keys set revoked_at = now() where id = $1 and user_id = $2 and revoked_at is null", id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// APIKeyUser resolves a key to its user, or ErrNotFound when the key is
// unknown or revoked, or the user is disabled. It notes the use on the key
// at most once a minute, as a best effort: a failure there is logged, not
// returned.
func (s *Store) APIKeyUser(ctx context.Context, key string) (*User, error) {
	hash := auth.HashSecret(key)
	var u User
	var stale bool
	err := s.pool.QueryRow(ctx, `select u.id, u.email, u.name, u.password_hash, u.role, u.disabled_at, u.created_at, u.last_login_at,
			k.last_used_at is null or k.last_used_at < now() - interval '1 minute'
		from api_keys k join users u on u.id = k.user_id
		where k.hash = $1 and k.revoked_at is null`, hash).
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
		if _, err := s.pool.Exec(ctx, "update api_keys set last_used_at = now() where hash = $1", hash); err != nil {
			slog.Warn("could not record the API key's use", "err", err)
		}
	}
	u.CreatedAt, u.HasPassword = u.CreatedAt.UTC(), u.PasswordHash != ""
	if u.LastLoginAt != nil {
		t := u.LastLoginAt.UTC()
		u.LastLoginAt = &t
	}
	return &u, nil
}
