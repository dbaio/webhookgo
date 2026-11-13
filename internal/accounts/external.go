package accounts

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	ErrDisabled   = errors.New("account disabled")
	ErrNotInvited = errors.New("no account or invitation for this address")
)

// ActionUserJoin is logged when someone creates their own account by
// signing in with an identity provider.
const ActionUserJoin = "user.join"

// ExternalSignIn is a sign-in vouched for by an identity provider, such as
// Google, which answers for Email: the caller has made sure it does (for
// Google, a Gmail or Workspace address).
type ExternalSignIn struct {
	Email      string
	Provider   string // "google", for the log
	MayJoin    bool   // with no account and no invitation, create one
	IP         string
	UserAgent  string
	SessionTTL time.Duration
}

// SignInExternal signs in the account with the given email, whatever its
// domain. Without one, a pending invitation for the address creates it with
// the invitation's role (and uses it up); otherwise, when MayJoin, it is
// created as a plain user with no password and no name. It returns the user,
// the session secret and whether the account is new. A disabled account
// gets ErrDisabled, and an address that may not join ErrNotInvited.
func (s *Store) SignInExternal(ctx context.Context, in ExternalSignIn) (*User, string, bool, error) {
	for attempt := 0; ; attempt++ {
		u, secret, joined, err := s.signInExternal(ctx, in)
		// Two first sign-ins at once: the other one created the account, so
		// this one signs in to it.
		if errors.Is(err, ErrEmailTaken) && attempt == 0 {
			continue
		}
		return u, secret, joined, err
	}
}

func (s *Store) signInExternal(ctx context.Context, in ExternalSignIn) (*User, string, bool, error) {
	var (
		u      *User
		secret string
		joined bool
	)
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		u, err = scanUser(tx.QueryRow(ctx, "select "+userColumns+" from users where lower(email) = lower($1)", in.Email))
		switch {
		case err == nil:
			if u.Disabled() {
				return ErrDisabled
			}
		case errors.Is(err, ErrNotFound):
			role, invited := RoleUser, false
			err = tx.QueryRow(ctx, `select role from one_time_links
				where kind = $1 and lower(email) = lower($2) and used_at is null and expires_at > now()
				order by created_at desc limit 1`, LinkInvite, in.Email).Scan(&role)
			switch {
			case err == nil:
				invited = true
			case !errors.Is(err, pgx.ErrNoRows):
				return err
			case !in.MayJoin:
				return ErrNotInvited
			}
			if u, err = insertUser(ctx, tx, in.Email, "", "", role); err != nil {
				return err
			}
			if invited {
				if _, err := tx.Exec(ctx, "update one_time_links set used_at = now() where kind = $1 and lower(email) = lower($2) and used_at is null",
					LinkInvite, in.Email); err != nil {
					return err
				}
			}
			if err := logEntry(ctx, tx, NewLogEntry{
				ActorID: u.ID, ActorEmail: u.Email, Action: ActionUserJoin, TargetType: TargetUser, TargetID: u.ID,
				Details: map[string]any{"email": u.Email, "role": u.Role, "via": in.Provider, "invited": invited}, IP: in.IP,
			}); err != nil {
				return err
			}
			joined = true
		default:
			return err
		}
		secret, err = createSession(ctx, tx, u, in.IP, in.UserAgent, in.SessionTTL)
		return err
	})
	if err != nil {
		return nil, "", false, err
	}
	return u, secret, joined, nil
}
