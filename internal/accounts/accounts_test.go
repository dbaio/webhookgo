package accounts_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/dbaio/webhookgo/internal/accounts"
	"github.com/dbaio/webhookgo/internal/accounts/accountstest"
)

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.DiscardHandler))
	os.Exit(m.Run())
}

var ctx = context.Background()

func TestMigrateIsIdempotent(t *testing.T) {
	st, db := accountstest.Open(t) // already migrated once
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(ctx, "select count(*) from schema_migrations").Scan(&n); err != nil || n != len(files) {
		t.Fatalf("schema_migrations: %d rows, want %d, %v", n, len(files), err)
	}
	for _, table := range []string{"users", "sessions", "one_time_links", "urls"} {
		if _, err := db.Exec(ctx, "select 1 from "+table+" limit 1"); err != nil {
			t.Errorf("table %s: %v", table, err)
		}
	}
}

func redeem(t *testing.T, st *accounts.Store, token string, in accounts.Redeem) (*accounts.User, string) {
	t.Helper()
	if in.SessionTTL == 0 {
		in.SessionTTL = time.Hour
	}
	if in.PasswordHash == "" {
		in.PasswordHash = "$argon2id$v=19$m=19456,t=2,p=1$x$y" // never verified here
	}
	u, secret, err := st.Redeem(ctx, token, in)
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	return u, secret
}

func TestSetupLinkIsSingleUse(t *testing.T) {
	st, _ := accountstest.Open(t)
	if n, err := st.ActiveAdmins(ctx); err != nil || n != 0 {
		t.Fatalf("admins before setup: %d, %v", n, err)
	}
	token, err := st.CreateLink(ctx, accounts.NewLink{Kind: accounts.LinkSetup, TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if l, err := st.InspectLink(ctx, token); err != nil || l.Kind != "setup" || l.Email != "" || l.Role != "admin" {
		t.Fatalf("inspect: %+v, %v", l, err)
	}
	u, secret := redeem(t, st, token, accounts.Redeem{Email: "Root@Example.com", Name: "Root"})
	if u.Role != "admin" || u.Email != "Root@Example.com" || u.LastLoginAt == nil || u.ID == "" {
		t.Fatalf("admin: %+v", u)
	}
	if got, err := st.SessionUser(ctx, secret, time.Hour); err != nil || got.ID != u.ID {
		t.Fatalf("session after setup: %+v, %v", got, err)
	}
	if n, _ := st.ActiveAdmins(ctx); n != 1 {
		t.Fatalf("admins after setup: %d", n)
	}
	if _, err := st.InspectLink(ctx, token); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("a used link must be gone: %v", err)
	}
	if _, _, err := st.Redeem(ctx, token, accounts.Redeem{Email: "x@example.com", Name: "x", PasswordHash: "h", SessionTTL: time.Hour}); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("second redeem: %v", err)
	}
	// Another setup link is useless once an admin exists.
	again, _ := st.CreateLink(ctx, accounts.NewLink{Kind: accounts.LinkSetup, TTL: time.Hour})
	if _, err := st.InspectLink(ctx, again); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("setup link with an admin: %v", err)
	}
	if _, _, err := st.Redeem(ctx, again, accounts.Redeem{Email: "y@example.com", Name: "y", PasswordHash: "h", SessionTTL: time.Hour}); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("setup redeem with an admin: %v", err)
	}
	if got, err := st.UserByEmail(ctx, "root@example.com"); err != nil || got.ID != u.ID {
		t.Fatalf("lookup by email ignores case: %+v, %v", got, err)
	}
}

func TestLinksExpire(t *testing.T) {
	st, db := accountstest.Open(t)
	token, _ := st.CreateLink(ctx, accounts.NewLink{Kind: accounts.LinkInvite, Email: "a@example.com", TTL: time.Hour})
	if _, err := st.InspectLink(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "update one_time_links set expires_at = now() - interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InspectLink(ctx, token); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("expired inspect: %v", err)
	}
	if _, _, err := st.Redeem(ctx, token, accounts.Redeem{Name: "a", PasswordHash: "h", SessionTTL: time.Hour}); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("expired redeem: %v", err)
	}
	if _, err := st.InspectLink(ctx, "not-a-token"); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("unknown token: %v", err)
	}
	if _, err := st.CreateLink(ctx, accounts.NewLink{Kind: "magic", TTL: time.Hour}); err == nil {
		t.Fatal("unknown kinds must be rejected")
	}
	if _, err := st.CreateLink(ctx, accounts.NewLink{Kind: accounts.LinkInvite, Role: "root", TTL: time.Hour}); err == nil {
		t.Fatal("unknown roles must be rejected")
	}
}

func TestInviteAndReset(t *testing.T) {
	st, _ := accountstest.Open(t)
	invite, _ := st.CreateLink(ctx, accounts.NewLink{Kind: accounts.LinkInvite, Email: "Ana@example.com", TTL: time.Hour})
	if l, err := st.InspectLink(ctx, invite); err != nil || l.Kind != "invite" || l.Email != "Ana@example.com" || l.Role != "user" {
		t.Fatalf("inspect invite: %+v, %v", l, err)
	}
	u, s1 := redeem(t, st, invite, accounts.Redeem{Name: "Ana"})
	if u.Role != "user" || u.Email != "Ana@example.com" || u.Name != "Ana" {
		t.Fatalf("invited user: %+v", u)
	}
	dup, _ := st.CreateLink(ctx, accounts.NewLink{Kind: accounts.LinkInvite, Email: "ANA@example.com", TTL: time.Hour})
	if _, _, err := st.Redeem(ctx, dup, accounts.Redeem{Name: "Ana 2", PasswordHash: "h", SessionTTL: time.Hour}); !errors.Is(err, accounts.ErrEmailTaken) {
		t.Fatalf("duplicate email: %v", err)
	}
	if _, err := st.InspectLink(ctx, dup); err != nil {
		t.Fatalf("a failed redeem must not consume the link: %v", err)
	}

	s2, _ := st.CreateSession(ctx, u, "127.0.0.1", "test", time.Hour)
	reset, err := st.CreateLink(ctx, accounts.NewLink{Kind: accounts.LinkReset, UserID: u.ID, CreatedBy: u.ID, TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if l, err := st.InspectLink(ctx, reset); err != nil || l.Kind != "reset" || l.Email != "Ana@example.com" || l.UserID != u.ID {
		t.Fatalf("inspect reset: %+v, %v", l, err)
	}
	after, s3 := redeem(t, st, reset, accounts.Redeem{PasswordHash: "new-hash"})
	if after.ID != u.ID || after.PasswordHash != "new-hash" {
		t.Fatalf("reset user: %+v", after)
	}
	for _, old := range []string{s1, s2} {
		if _, err := st.SessionUser(ctx, old, time.Hour); !errors.Is(err, accounts.ErrNotFound) {
			t.Fatalf("session must end on reset: %v", err)
		}
	}
	if got, err := st.SessionUser(ctx, s3, time.Hour); err != nil || got.ID != u.ID {
		t.Fatalf("session from reset: %v", err)
	}
	users, err := st.ListUsers(ctx)
	if err != nil || len(users) != 1 || users[0].Email != "Ana@example.com" {
		t.Fatalf("list: %+v, %v", users, err)
	}
}

func TestSessions(t *testing.T) {
	st, db := accountstest.Open(t)
	token, _ := st.CreateLink(ctx, accounts.NewLink{Kind: accounts.LinkSetup, TTL: time.Hour})
	u, s1 := redeem(t, st, token, accounts.Redeem{Email: "r@example.com", Name: "R"})
	s2, err := st.CreateSession(ctx, u, "127.0.0.1", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SessionUser(ctx, "nope", time.Hour); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("unknown session: %v", err)
	}

	// Sliding expiry, at most once a minute.
	if _, err := db.Exec(ctx, "update sessions set last_seen_at = now() - interval '2 minutes', expires_at = now() + interval '30 minutes'"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SessionUser(ctx, s1, time.Hour); err != nil {
		t.Fatal(err)
	}
	var slid, seen bool
	if err := db.QueryRow(ctx, "select expires_at > now() + interval '50 minutes', last_seen_at > now() - interval '10 seconds' from sessions where id_hash = sha256($1)", []byte(s1)).Scan(&slid, &seen); err != nil || !slid || !seen {
		t.Fatalf("session must slide: slid=%v seen=%v %v", slid, seen, err)
	}
	if _, err := db.Exec(ctx, "update sessions set last_seen_at = now() - interval '10 seconds', expires_at = now() + interval '30 minutes'"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SessionUser(ctx, s1, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, "select expires_at > now() + interval '50 minutes' from sessions where id_hash = sha256($1)", []byte(s1)).Scan(&slid); err != nil || slid {
		t.Fatalf("a session seen in the last minute must not be touched: %v %v", slid, err)
	}

	// Expiry.
	if _, err := db.Exec(ctx, "update sessions set expires_at = now() - interval '1 second' where id_hash = sha256($1)", []byte(s2)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SessionUser(ctx, s2, time.Hour); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("expired session: %v", err)
	}

	// A disabled user has no session.
	if _, err := db.Exec(ctx, "update users set disabled_at = now()"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SessionUser(ctx, s1, time.Hour); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("disabled user: %v", err)
	}
	if n, _ := st.ActiveAdmins(ctx); n != 0 {
		t.Fatalf("a disabled admin is not active: %d", n)
	}
	if _, err := db.Exec(ctx, "update users set disabled_at = null"); err != nil {
		t.Fatal(err)
	}

	// Password change keeps the current session only.
	s3, _ := st.CreateSession(ctx, u, "127.0.0.1", "test", time.Hour)
	if err := st.SetPassword(ctx, u.ID, "h2", s3); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SessionUser(ctx, s1, time.Hour); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("other session after password change: %v", err)
	}
	if _, err := st.SessionUser(ctx, s3, time.Hour); err != nil {
		t.Fatalf("own session after password change: %v", err)
	}
	if err := st.DeleteSession(ctx, s3); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SessionUser(ctx, s3, time.Hour); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("deleted session: %v", err)
	}
}
