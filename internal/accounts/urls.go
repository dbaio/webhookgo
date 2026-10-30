package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/dbaio/webhookgo/internal/store"
)

// urlsLock serialises the URL count check of one owner with their inserts.
const urlsLock = 0x7767_7572 // "wgur"

// InsertURL stores a URL that belongs to an account: the whole token, as
// JSON, is the source of truth. The owner may hold at most max URLs (0 =
// unlimited), else ErrTooManyURLs; a token already stored gives ErrURLOwned.
func (s *Store) InsertURL(ctx context.Context, t *store.Token, max int) error {
	config, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if max > 0 {
			if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1, hashtext($2))", urlsLock, t.OwnerID); err != nil {
				return err
			}
			var n int
			if err := tx.QueryRow(ctx, "select count(*) from urls where owner_id = $1", t.OwnerID).Scan(&n); err != nil {
				return err
			}
			if n >= max {
				return ErrTooManyURLs
			}
		}
		_, err := tx.Exec(ctx, `insert into urls (token_id, owner_id, config, created_at, updated_at) values ($1, $2, $3, $4, $5)`,
			t.ID, t.OwnerID, string(config), t.CreatedAt, t.UpdatedAt)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return ErrURLOwned
		}
		return err
	})
}

// UpdateURL replaces the stored token, or fails with ErrNotFound when the
// URL no longer belongs to an account.
func (s *Store) UpdateURL(ctx context.Context, t *store.Token) error {
	config, err := json.Marshal(t)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, "update urls set config = $2, updated_at = $3 where token_id = $1", t.ID, string(config), t.UpdatedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteURL removes a URL; one that is not stored is not an error.
func (s *Store) DeleteURL(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, "delete from urls where token_id = $1", id)
	return err
}

// URL loads the token of a URL that belongs to an account, or ErrNotFound.
func (s *Store) URL(ctx context.Context, id string) (*store.Token, error) {
	var config []byte
	err := s.pool.QueryRow(ctx, "select config from urls where token_id = $1", id).Scan(&config)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return decodeToken(id, config)
}

func decodeToken(id string, config []byte) (*store.Token, error) {
	var t store.Token
	if err := json.Unmarshal(config, &t); err != nil {
		return nil, fmt.Errorf("decode url %s: %w", id, err)
	}
	return &t, nil
}

// ListURLs returns one page of the URLs an account owns, newest first, and
// how many match. A non-empty search keeps those whose description contains
// it, ignoring case.
func (s *Store) ListURLs(ctx context.Context, ownerID, search string, page, perPage int) ([]*store.Token, int, error) {
	const where = "where owner_id = $1 and ($2 = '' or strpos(lower(config->>'description'), lower($2)) > 0)"
	var total int
	if err := s.pool.QueryRow(ctx, "select count(*) from urls "+where, ownerID, search).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, "select token_id, config from urls "+where+" order by created_at desc, token_id limit $3 offset $4",
		ownerID, search, perPage, (page-1)*perPage)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	tokens := []*store.Token{}
	for rows.Next() {
		var (
			id     string
			config []byte
		)
		if err := rows.Scan(&id, &config); err != nil {
			return nil, 0, err
		}
		t, err := decodeToken(id, config)
		if err != nil {
			return nil, 0, err
		}
		tokens = append(tokens, t)
	}
	return tokens, total, rows.Err()
}
