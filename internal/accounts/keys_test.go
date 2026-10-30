package accounts_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dbaio/webhookgo/internal/accounts"
	"github.com/dbaio/webhookgo/internal/accounts/accountstest"
)

func TestAPIKeys(t *testing.T) {
	st, db := accountstest.Open(t)
	token, _ := st.CreateLink(ctx, accounts.NewLink{Kind: accounts.LinkSetup, TTL: time.Hour})
	u, _ := redeem(t, st, token, accounts.Redeem{Email: "r@example.com", Name: "R"})
	invite, _ := st.CreateLink(ctx, accounts.NewLink{Kind: accounts.LinkInvite, Email: "o@example.com", TTL: time.Hour})
	other, _ := redeem(t, st, invite, accounts.Redeem{Name: "O"})

	first, key, err := st.CreateAPIKey(ctx, u.ID, "CI")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, "wgo_") || len(key) != 47 || first.Prefix != key[4:12] || first.Name != "CI" || first.LastUsedAt != nil || first.ID == "" {
		t.Fatalf("new key: %+v %q", first, key)
	}
	// Only the hash is stored.
	var stored bool
	if err := db.QueryRow(ctx, "select hash = sha256($1) from api_keys where id = $2", []byte(key), first.ID).Scan(&stored); err != nil || !stored {
		t.Fatalf("stored hash: %v %v", stored, err)
	}
	var leaked int
	if err := db.QueryRow(ctx, "select count(*) from api_keys where name like '%' || $1 || '%' or prefix = $1", key).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("the key must not be stored: %d %v", leaked, err)
	}

	// Authentication, and the use noted at most once a minute.
	got, err := st.APIKeyUser(ctx, key)
	if err != nil || got.ID != u.ID || got.Role != accounts.RoleAdmin {
		t.Fatalf("key user: %+v, %v", got, err)
	}
	keys, err := st.ListAPIKeys(ctx, u.ID)
	if err != nil || len(keys) != 1 || keys[0].LastUsedAt == nil {
		t.Fatalf("last_used_at after use: %+v, %v", keys, err)
	}
	if _, err := db.Exec(ctx, "update api_keys set last_used_at = now() - interval '10 seconds'"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.APIKeyUser(ctx, key); err != nil {
		t.Fatal(err)
	}
	var touched bool
	if err := db.QueryRow(ctx, "select last_used_at > now() - interval '5 seconds' from api_keys where id = $1", first.ID).Scan(&touched); err != nil || touched {
		t.Fatalf("a key used in the last minute must not be touched: %v %v", touched, err)
	}
	if _, err := db.Exec(ctx, "update api_keys set last_used_at = now() - interval '2 minutes'"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.APIKeyUser(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, "select last_used_at > now() - interval '5 seconds' from api_keys where id = $1", first.ID).Scan(&touched); err != nil || !touched {
		t.Fatalf("last_used_at must move after a minute: %v %v", touched, err)
	}
	for _, bad := range []string{"", "nope", "wgo_" + strings.Repeat("x", 43), key[:46]} {
		if _, err := st.APIKeyUser(ctx, bad); !errors.Is(err, accounts.ErrNotFound) {
			t.Fatalf("unknown key %q: %v", bad, err)
		}
	}

	// Listing: newest first, each user their own.
	second, _, err := st.CreateAPIKey(ctx, u.ID, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	theirs, theirKey, _ := st.CreateAPIKey(ctx, other.ID, "theirs")
	keys, _ = st.ListAPIKeys(ctx, u.ID)
	if len(keys) != 2 || keys[0].ID != second.ID || keys[1].ID != first.ID {
		t.Fatalf("list: %+v", keys)
	}
	if keys, _ := st.ListAPIKeys(ctx, other.ID); len(keys) != 1 || keys[0].ID != theirs.ID {
		t.Fatalf("other's list: %+v", keys)
	}

	// Revoking: only one's own keys, once.
	if err := st.RevokeAPIKey(ctx, u.ID, theirs.ID); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("revoking someone else's key: %v", err)
	}
	if err := st.RevokeAPIKey(ctx, u.ID, "11111111-2222-7333-8444-555555555555"); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("revoking an unknown key: %v", err)
	}
	if err := st.RevokeAPIKey(ctx, u.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.RevokeAPIKey(ctx, u.ID, first.ID); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("revoking twice: %v", err)
	}
	if _, err := st.APIKeyUser(ctx, key); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("revoked key: %v", err)
	}
	if keys, _ := st.ListAPIKeys(ctx, u.ID); len(keys) != 1 || keys[0].ID != second.ID {
		t.Fatalf("list after revoke: %+v", keys)
	}
	var revoked int
	if err := db.QueryRow(ctx, "select count(*) from api_keys where revoked_at is not null").Scan(&revoked); err != nil || revoked != 1 {
		t.Fatalf("revoked rows are kept: %d %v", revoked, err)
	}

	// A disabled user's keys stop working; a deleted user's go with them.
	if _, err := db.Exec(ctx, "update users set disabled_at = now() where id = $1", other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.APIKeyUser(ctx, theirKey); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("disabled user's key: %v", err)
	}
	if _, err := db.Exec(ctx, "delete from users where id = $1", other.ID); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := db.QueryRow(ctx, "select count(*) from api_keys where user_id = $1", other.ID).Scan(&left); err != nil || left != 0 {
		t.Fatalf("keys of a deleted user: %d %v", left, err)
	}
}

func TestAPIKeyLimit(t *testing.T) {
	st, _ := accountstest.Open(t)
	token, _ := st.CreateLink(ctx, accounts.NewLink{Kind: accounts.LinkSetup, TTL: time.Hour})
	u, _ := redeem(t, st, token, accounts.Redeem{Email: "r@example.com", Name: "R"})
	var last *accounts.APIKey
	for i := range accounts.MaxAPIKeys {
		k, _, err := st.CreateAPIKey(ctx, u.ID, "key")
		if err != nil {
			t.Fatalf("key %d: %v", i, err)
		}
		last = k
	}
	if _, _, err := st.CreateAPIKey(ctx, u.ID, "one too many"); !errors.Is(err, accounts.ErrTooManyKeys) {
		t.Fatalf("over the limit: %v", err)
	}
	// Revoked keys do not count.
	if err := st.RevokeAPIKey(ctx, u.ID, last.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateAPIKey(ctx, u.ID, "room again"); err != nil {
		t.Fatalf("after a revoke: %v", err)
	}
}
