package accounts_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dbaio/webhookgo/internal/accounts"
	"github.com/dbaio/webhookgo/internal/accounts/accountstest"
)

func TestSignInExternal(t *testing.T) {
	st, db := accountstest.Open(t)
	in := accounts.ExternalSignIn{Email: "new@example.com", Provider: "google", IP: "192.0.2.1", SessionTTL: time.Hour}

	if _, _, _, err := st.SignInExternal(ctx, in); !errors.Is(err, accounts.ErrNotInvited) {
		t.Fatalf("may not join: %v", err)
	}
	in.MayJoin = true
	u, secret, joined, err := st.SignInExternal(ctx, in)
	if err != nil || !joined || u.Role != accounts.RoleUser || u.HasPassword || u.Name != "" || u.LastLoginAt == nil {
		t.Fatalf("join: %+v, %v, %v", u, joined, err)
	}
	if got, err := st.SessionUser(ctx, secret, time.Hour); err != nil || got.ID != u.ID || got.HasPassword {
		t.Fatalf("session: %+v, %v", got, err)
	}

	// The address is matched whatever its case, and the account kept.
	in.Email, in.MayJoin = "NEW@example.com", false
	again, _, joined, err := st.SignInExternal(ctx, in)
	if err != nil || joined || again.ID != u.ID {
		t.Fatalf("again: %+v, %v, %v", again, joined, err)
	}
	if _, err := db.Exec(ctx, "update users set disabled_at = now() where id = $1", u.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := st.SignInExternal(ctx, in); !errors.Is(err, accounts.ErrDisabled) {
		t.Fatalf("disabled: %v", err)
	}
}

func TestSignInExternalAtOnce(t *testing.T) {
	st, _ := accountstest.Open(t)
	in := accounts.ExternalSignIn{Email: "twice@example.com", Provider: "google", MayJoin: true, SessionTTL: time.Hour}
	const n = 4
	var (
		wg     sync.WaitGroup
		ids    [n]string
		joins  [n]bool
		errs   [n]error
		starts = make(chan struct{})
	)
	for i := range n {
		wg.Go(func() {
			<-starts
			var u *accounts.User
			u, _, joins[i], errs[i] = st.SignInExternal(ctx, in)
			if u != nil {
				ids[i] = u.ID
			}
		})
	}
	close(starts)
	wg.Wait()
	joined := 0
	for i := range n {
		if errs[i] != nil || ids[i] != ids[0] {
			t.Fatalf("sign-in %d: %s, %v (first: %s)", i, ids[i], errs[i], ids[0])
		}
		if joins[i] {
			joined++
		}
	}
	if joined != 1 {
		t.Fatalf("%d sign-ins created the account", joined)
	}
}
