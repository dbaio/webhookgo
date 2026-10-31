package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/dbaio/webhookgo/internal/store"
)

// anonymousPruneEvery is how often the admin panel's index of anonymous URLs
// drops the ones that expired.
const anonymousPruneEvery = 10 * time.Minute

// keepAnonymousIndex runs with accounts on: it adds the anonymous URLs the
// index misses (created while accounts were off), then prunes it every
// anonymousPruneEvery, until ctx ends.
func keepAnonymousIndex(ctx context.Context, st *store.Store) {
	prune := func() {
		if n, err := st.PruneAnonymous(ctx, anonymousPruneEvery); err != nil && ctx.Err() == nil {
			slog.Warn("anonymous URL index not pruned", "err", err)
		} else if n > 0 {
			slog.Debug("anonymous URL index pruned", "removed", n)
		}
	}
	prune()
	if n, err := st.IndexAnonymousURLs(ctx); err != nil && ctx.Err() == nil {
		slog.Warn("anonymous URLs not indexed", "err", err)
	} else if n > 0 {
		slog.Info("anonymous URLs indexed", "added", n)
	}
	t := time.NewTicker(anonymousPruneEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			prune()
		}
	}
}
