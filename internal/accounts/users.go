package accounts

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/dbaio/webhookgo/internal/auth"
)

const userColumns = "id, email, name, password_hash, role, disabled_at, created_at, last_login_at"

func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.DisabledAt, &u.CreatedAt, &u.LastLoginAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.CreatedAt, u.HasPassword = u.CreatedAt.UTC(), u.PasswordHash != ""
	if u.LastLoginAt != nil {
		t := u.LastLoginAt.UTC()
		u.LastLoginAt = &t
	}
	return &u, nil
}

// UserByEmail finds a user by email, ignoring case.
func (s *Store) UserByEmail(ctx context.Context, email string) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx, "select "+userColumns+" from users where lower(email) = lower($1)", email))
}

func (s *Store) UserByID(ctx context.Context, id string) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx, "select "+userColumns+" from users where id = $1", id))
}

// ListUsers returns every account, oldest first.
func (s *Store) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := s.pool.Query(ctx, "select "+userColumns+" from users order by created_at, email")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// ActiveAdmins counts the admins who can sign in. The instance must always
// keep at least one.
func (s *Store) ActiveAdmins(ctx context.Context) (int, error) {
	return activeAdmins(ctx, s.pool)
}

func activeAdmins(ctx context.Context, q querier) (int, error) {
	var n int
	err := q.QueryRow(ctx, "select count(*) from users where role = $1 and disabled_at is null", RoleAdmin).Scan(&n)
	return n, err
}

// insertUser creates an account, or fails with ErrEmailTaken.
func insertUser(ctx context.Context, q querier, email, name, passwordHash, role string) (*User, error) {
	id := uuid.Must(uuid.NewV7()).String()
	u, err := scanUser(q.QueryRow(ctx, "insert into users (id, email, name, password_hash, role) values ($1, $2, $3, $4, $5) returning "+userColumns,
		id, email, name, passwordHash, role))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
		return nil, ErrEmailTaken
	}
	return u, err
}

func (s *Store) UpdateName(ctx context.Context, id, name string) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx, "update users set name = $2 where id = $1 returning "+userColumns, id, name))
}

// SetPassword stores a new password hash and ends the user's other sessions,
// keeping the one identified by keepSecret.
func (s *Store) SetPassword(ctx context.Context, id, passwordHash, keepSecret string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "update users set password_hash = $2 where id = $1", id, passwordHash)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		_, err = tx.Exec(ctx, "delete from sessions where user_id = $1 and id_hash <> $2", id, auth.HashSecret(keepSecret))
		return err
	})
}
