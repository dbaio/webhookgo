package server

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/dbaio/webhookgo/internal/accounts"
	"github.com/dbaio/webhookgo/internal/auth"
	"github.com/dbaio/webhookgo/internal/store"
)

// tokens is the one place handlers read and write URLs. An anonymous URL
// lives in Redis only, as always. A URL that belongs to an account lives in
// Postgres, and its Redis copy is a cache: writes go to Postgres first, and a
// read that misses the cache fills it again from Postgres, so the URL never
// expires while its row exists.
type tokens struct {
	store    *store.Store
	accounts *accounts.Store // nil when accounts are off
	maxURLs  int             // per owner; 0 = unlimited
}

// restoreTimeout bounds the Postgres read that fills the cache again. Past
// it the caller answers 503, which webhook senders retry, never 404.
const restoreTimeout = 500 * time.Millisecond

// read loads a URL. With accounts on, a URL missing from Redis is looked up
// in Postgres and, when found, put back in the cache. An ID found nowhere is
// remembered for a minute, so random IDs do not become database queries.
func (tk *tokens) read(ctx context.Context, id string) (*store.Token, error) {
	t, err := tk.store.GetToken(ctx, id)
	if err == nil || tk.accounts == nil || !errors.Is(err, store.ErrNotFound) {
		return t, err
	}
	miss := err // ErrNotFound, or a *DeletedError that says more
	if unknown, err := tk.store.IsUnknown(ctx, id); err != nil {
		return nil, err
	} else if unknown {
		return nil, miss
	}
	dbCtx, cancel := context.WithTimeout(ctx, restoreTimeout)
	defer cancel()
	t, err = tk.accounts.URL(dbCtx, id)
	if errors.Is(err, accounts.ErrNotFound) {
		_ = tk.store.MarkUnknown(ctx, id) // best effort: it only saves work
		return nil, miss
	}
	if err != nil {
		return nil, fmt.Errorf("restore url %s: %w", id, err)
	}
	return tk.restore(ctx, t)
}

// restore puts t, just read from Postgres, back in the cache and reads it
// again: SET NX, as a replica may have restored a newer version meanwhile,
// and reading again rather than returning t makes every replica agree. A
// URL deleted since the row was read must not come back: with audit records
// on, its tombstone refuses the restore; without, the row is checked again.
func (tk *tokens) restore(ctx context.Context, t *store.Token) (*store.Token, error) {
	written, err := tk.store.RestoreToken(ctx, t)
	if err != nil {
		return nil, err
	}
	if written && tk.store.Retention() <= 0 {
		dbCtx, cancel := context.WithTimeout(ctx, restoreTimeout)
		defer cancel()
		switch _, err := tk.accounts.URL(dbCtx, t.ID); {
		case errors.Is(err, accounts.ErrNotFound):
			if err := tk.store.DropRestored(ctx, t); err != nil {
				return nil, err
			}
			return nil, store.ErrNotFound
		case err != nil:
			return nil, fmt.Errorf("restore url %s: %w", t.ID, err)
		}
	}
	return tk.store.GetToken(ctx, t.ID)
}

// exists reports whether a URL is live, in Redis or in Postgres, without
// extending its life or filling the cache. A deleted URL does not exist.
func (tk *tokens) exists(ctx context.Context, id string) (bool, error) {
	_, err := tk.store.PeekToken(ctx, id)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return false, err
	}
	if tk.accounts == nil {
		return false, nil
	}
	if unknown, err := tk.store.IsUnknown(ctx, id); err != nil || unknown {
		return false, err
	}
	switch _, err := tk.accounts.URL(ctx, id); {
	case errors.Is(err, accounts.ErrNotFound):
		_ = tk.store.MarkUnknown(ctx, id) // best effort: it only saves work
		return false, nil
	case err != nil:
		return false, err
	}
	return true, nil
}

// create stores a new URL. An owned one goes to Postgres first, where the
// owner's limit is checked (accounts.ErrTooManyURLs).
func (tk *tokens) create(ctx context.Context, t *store.Token) error {
	if t.Owned() {
		if err := tk.accounts.InsertURL(ctx, t, tk.maxURLs); err != nil {
			return err
		}
	}
	return tk.store.CreateToken(ctx, t)
}

// update replaces a URL's settings. For an owned URL whose row is gone
// (deleted or released elsewhere meanwhile) it returns store.ErrNotFound;
// for an anonymous one that an account claimed meanwhile, store.ErrOwned.
func (tk *tokens) update(ctx context.Context, t *store.Token) error {
	if !t.Owned() {
		return tk.store.SaveAnonymousToken(ctx, t)
	}
	if err := tk.accounts.UpdateURL(ctx, t); err != nil {
		if errors.Is(err, accounts.ErrNotFound) {
			return store.ErrNotFound
		}
		return err
	}
	if err := tk.store.SaveToken(ctx, t); err != nil {
		// Postgres has the new settings: do not let Redis serve the old ones.
		_ = tk.store.Uncache(context.WithoutCancel(ctx), t.ID)
		return err
	}
	return nil
}

// delete removes a URL and its requests. The Postgres row goes first, so a
// Redis failure cannot bring the URL back on the next read. An anonymous t
// that an account claimed meanwhile is refused with store.ErrOwned.
func (tk *tokens) delete(ctx context.Context, t *store.Token, ip, userAgent string) error {
	if t.Owned() {
		if err := tk.accounts.DeleteURL(ctx, t.ID); err != nil {
			return err
		}
	}
	return tk.store.DeleteToken(ctx, t, ip, userAgent)
}

// claim stores an anonymous URL that t's owner just took over.
func (tk *tokens) claim(ctx context.Context, t *store.Token) error {
	if err := tk.accounts.InsertURL(ctx, t, tk.maxURLs); err != nil {
		return err
	}
	if err := tk.store.ClaimToken(ctx, t); err != nil {
		// Postgres owns it now: do not let Redis keep the anonymous copy.
		_ = tk.store.Uncache(context.WithoutCancel(ctx), t.ID)
		return err
	}
	return nil
}

// release makes an owned URL anonymous again: t already carries no owner and
// a fresh claim hash. From here on it expires like any anonymous URL.
func (tk *tokens) release(ctx context.Context, t *store.Token) error {
	if err := tk.accounts.DeleteURL(ctx, t.ID); err != nil {
		return err
	}
	return tk.store.SaveToken(ctx, t)
}

// addRequest stores a captured request. When the token was evicted between
// the capture's read and this write, it is restored and the write retried
// once.
func (tk *tokens) addRequest(ctx context.Context, req *store.Request, body []byte) (int64, []string, error) {
	total, evicted, err := tk.store.AddRequest(ctx, req, body)
	if errors.Is(err, store.ErrNotFound) && tk.accounts != nil {
		if _, err := tk.read(ctx, req.TokenID); err != nil {
			return 0, nil, err
		}
		return tk.store.AddRequest(ctx, req, body)
	}
	return total, evicted, err
}

// newClaimKey returns the key that lets an account claim an anonymous URL,
// given out once, and the hash kept with the token.
func newClaimKey() (key, hash string) {
	key, sum := auth.NewSecret()
	return key, hex.EncodeToString(sum)
}

func claimHash(key string) string { return hex.EncodeToString(auth.HashSecret(key)) }

func isAdmin(u *accounts.User) bool { return u != nil && u.Role == accounts.RoleAdmin }

// noteAdminAccess logs url.view when an admin opens a URL that belongs to
// someone else, once an hour per admin and URL. A failure is only logged:
// it does not refuse the view.
func (s *Server) noteAdminAccess(r *http.Request, u *accounts.User, t *store.Token) {
	first, err := s.store.NoteAdminView(r.Context(), u.ID, t.ID)
	if err == nil && first {
		details := map[string]any{"owner_id": t.OwnerID}
		if owner, err := s.accounts.UserByID(r.Context(), t.OwnerID); err == nil {
			details["owner_email"] = owner.Email
		}
		err = s.logAdmin(r, accounts.ActionURLView, accounts.TargetURL, t.ID, details)
	}
	if err != nil {
		slog.Warn("admin view not logged", "admin", u.ID, "url", t.ID, "err", err)
	}
}

// tooManyURLs answers the owner's limit.
func (s *Server) tooManyURLs(w http.ResponseWriter) {
	writeError(w, http.StatusForbidden, fmt.Sprintf("You have reached the limit of %d URLs; delete one first", s.cfg.MaxURLs))
}
