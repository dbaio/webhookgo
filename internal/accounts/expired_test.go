package accounts_test

import (
	"testing"
	"time"

	"github.com/dbaio/webhookgo/internal/accounts"
	"github.com/dbaio/webhookgo/internal/accounts/accountstest"
)

func TestDeleteExpired(t *testing.T) {
	st, db := accountstest.Open(t)
	setup, _ := st.CreateLink(ctx, accounts.NewLink{Kind: accounts.LinkSetup, TTL: time.Hour})
	u, live := redeem(t, st, setup, accounts.Redeem{Email: "r@example.com", Name: "R"})
	old, _ := st.CreateSession(ctx, u, "203.0.113.9", "old browser", time.Hour)
	if _, err := st.CreateLink(ctx, accounts.NewLink{Kind: accounts.LinkInvite, Email: "late@example.com", TTL: time.Hour}); err != nil {
		t.Fatal(err)
	}
	pending, _ := st.CreateLink(ctx, accounts.NewLink{Kind: accounts.LinkInvite, Email: "soon@example.com", TTL: time.Hour})
	if _, err := db.Exec(ctx, "update sessions set expires_at = now() - interval '1 second' where id_hash = sha256($1)", []byte(old)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "update one_time_links set expires_at = now() - interval '1 second' where email = 'late@example.com'"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "update one_time_links set expires_at = now() - interval '1 second' where used_at is not null"); err != nil {
		t.Fatal(err)
	}

	if n, err := st.DeleteExpired(ctx); err != nil || n != 2 {
		t.Fatalf("deleted %d, %v", n, err)
	}
	var sessions, links int
	if err := db.QueryRow(ctx, "select (select count(*) from sessions where ip = '203.0.113.9'), (select count(*) from one_time_links where email = 'late@example.com')").Scan(&sessions, &links); err != nil || sessions+links != 0 {
		t.Fatalf("left: %d sessions, %d links, %v", sessions, links, err)
	}
	if _, err := st.SessionUser(ctx, live, time.Hour); err != nil {
		t.Fatalf("a live session must stay: %v", err)
	}
	if _, err := st.InspectLink(ctx, pending); err != nil {
		t.Fatalf("a pending link must stay: %v", err)
	}
	if n, err := st.DeleteExpired(ctx); err != nil || n != 0 {
		t.Fatalf("again: %d, %v", n, err)
	}
}
