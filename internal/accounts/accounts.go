// Package accounts persists users, their sessions and one-time links (setup,
// invite, reset) in Postgres. Secrets never reach the database: sessions and
// links are stored under the SHA-256 of their secret, and passwords as
// argon2id hashes.
package accounts

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound   = errors.New("not found")
	ErrEmailTaken = errors.New("email already registered")
)

//go:embed migrations/*.sql
var migrations embed.FS

// Advisory lock keys: one serialises migrations across replicas, the other
// the creation of the first admin.
const (
	migrateLock = 0x7767_6d69 // "wgmi"
	setupLock   = 0x7767_7365 // "wgse"
)

type Store struct {
	pool *pgxpool.Pool
}

// querier is what the pool and a transaction have in common.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Open connects to Postgres at url, or through the PG* variables when url is
// empty, trying up to attempts times a second apart.
func Open(ctx context.Context, url string, attempts int) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("DATABASE_URL: %w", err)
	}
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	for attempt := 1; ; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := pool.Ping(pingCtx)
		cancel()
		if err == nil {
			return &Store{pool: pool}, nil
		}
		if attempt >= attempts {
			pool.Close()
			return nil, fmt.Errorf("ACCOUNTS=on but Postgres is unreachable at %s: %w; did you set COMPOSE_PROFILES=accounts?", cfg.ConnConfig.Host, err)
		}
		slog.Warn("waiting for postgres", "attempt", attempt, "err", err)
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Migrate applies the embedded migrations that are not yet recorded in
// schema_migrations, each in its own transaction, under an advisory lock so
// replicas starting together do not race.
func (s *Store) Migrate(ctx context.Context) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "select pg_advisory_lock($1)", migrateLock); err != nil {
		return err
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), "select pg_advisory_unlock($1)", migrateLock) }()

	if _, err := conn.Exec(ctx, `create table if not exists schema_migrations (
		version int primary key, applied_at timestamptz not null default now())`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrations, "migrations") // sorted by name
	if err != nil {
		return err
	}
	for _, e := range entries {
		version, err := strconv.Atoi(strings.SplitN(e.Name(), "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migration %s: name must start with a number", e.Name())
		}
		var applied bool
		if err := conn.QueryRow(ctx, "select exists (select 1 from schema_migrations where version = $1)", version).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		sql, err := fs.ReadFile(migrations, "migrations/"+e.Name())
		if err != nil {
			return err
		}
		if err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "insert into schema_migrations (version) values ($1)", version)
			return err
		}); err != nil {
			return fmt.Errorf("migration %s: %w", e.Name(), err)
		}
		slog.Info("applied migration", "name", e.Name())
	}
	return nil
}
