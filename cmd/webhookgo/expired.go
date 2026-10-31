package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/dbaio/webhookgo/internal/accounts"
)

// expiredEvery is how often expired sessions and unused links are deleted.
const expiredEvery = time.Hour

// deleteExpired runs with accounts on: it deletes the expired sessions and
// one-time links at startup, then every expiredEvery, until ctx ends.
func deleteExpired(ctx context.Context, acc *accounts.Store) {
	t := time.NewTicker(expiredEvery)
	defer t.Stop()
	for {
		if n, err := acc.DeleteExpired(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("expired sessions not deleted", "err", err)
		} else if n > 0 {
			slog.Debug("expired sessions and links deleted", "rows", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
