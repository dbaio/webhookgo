package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// With accounts on, the admin panel lists the anonymous URLs. They live in
// Redis only, so one more key indexes them, without a TTL so that eviction
// never takes it:
//
//	wh:anonymous        ZSET    anonymous URL IDs scored by creation time (ms)
//	wh:anonymous:lock:* STRING  set while a replica sweeps the index
//
// The token layer adds a URL when it is created or released and removes it
// when it is claimed or deleted. One that expires or is evicted stays until
// a list reads past it or PruneAnonymous runs; URLs created while accounts
// were off are added by IndexAnonymousURLs, at startup.
const anonymousKey = "wh:anonymous"

func anonymousLockKey(job string) string { return "wh:anonymous:lock:" + job }

// anonymousChunk is how many URLs a list, search or sweep reads at once.
const anonymousChunk = 200

// AnonymousURL is a live anonymous URL, for the admin list.
type AnonymousURL struct {
	Token     *Token
	ExpiresAt time.Time
}

// IndexAnonymous adds an anonymous URL to the admin list.
func (s *Store) IndexAnonymous(ctx context.Context, t *Token) error {
	return s.rdb.ZAdd(ctx, anonymousKey, redis.Z{Score: float64(t.CreatedAt.UnixMilli()), Member: t.ID}).Err()
}

// UnindexAnonymous takes a URL off the admin list.
func (s *Store) UnindexAnonymous(ctx context.Context, id string) error {
	return s.rdb.ZRem(ctx, anonymousKey, id).Err()
}

// ListAnonymous returns one page of the anonymous URLs, newest first, and
// how many there are, without extending their lives. Entries whose URL is
// gone, or belongs to an account now, are dropped from the index on the way.
func (s *Store) ListAnonymous(ctx context.Context, page, perPage int) ([]AnonymousURL, int, error) {
	start := int64((page - 1) * perPage)
	var urls []AnonymousURL
	// Dropping stale entries moves later ones into the page: read it again.
	for range 4 {
		ids, err := s.rdb.ZRevRange(ctx, anonymousKey, start, start+int64(perPage)-1).Result()
		if err != nil {
			return nil, 0, err
		}
		var stale int
		if urls, stale, err = s.loadAnonymous(ctx, ids); err != nil {
			return nil, 0, err
		}
		if stale == 0 {
			break
		}
	}
	total, err := s.rdb.ZCard(ctx, anonymousKey).Result()
	return urls, int(total), err
}

// EachAnonymous calls fn with every anonymous URL, newest first, a chunk at
// a time, without extending their lives, and drops stale entries on the way
// as ListAnonymous does. It stops at the first error fn returns.
func (s *Store) EachAnonymous(ctx context.Context, fn func([]AnonymousURL) error) error {
	for start := int64(0); ; start += anonymousChunk {
		ids, err := s.rdb.ZRevRange(ctx, anonymousKey, start, start+anonymousChunk-1).Result()
		if err != nil {
			return err
		}
		found, stale, err := s.loadAnonymous(ctx, ids)
		if err != nil {
			return err
		}
		if len(found) > 0 {
			if err := fn(found); err != nil {
				return err
			}
		}
		if len(ids) < anonymousChunk {
			return nil
		}
		start -= int64(stale) // they left the index: the next chunk starts earlier
	}
}

// loadAnonymous reads the URLs of index entries, in order, and drops the
// entries whose URL is gone or owned now, reporting how many.
func (s *Store) loadAnonymous(ctx context.Context, ids []string) ([]AnonymousURL, int, error) {
	urls := make([]AnonymousURL, 0, len(ids))
	if len(ids) == 0 {
		return urls, 0, nil
	}
	gets := make([]*redis.StringCmd, len(ids))
	ttls := make([]*redis.DurationCmd, len(ids))
	_, err := s.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		for i, id := range ids {
			gets[i] = p.Get(ctx, tokenKey(id))
			ttls[i] = p.PTTL(ctx, tokenKey(id))
		}
		return nil
	})
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, 0, err
	}
	now := time.Now().UTC()
	var stale []any
	for i, id := range ids {
		v, err := gets[i].Result()
		if errors.Is(err, redis.Nil) {
			stale = append(stale, id)
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		var t Token
		if err := json.Unmarshal([]byte(v), &t); err != nil || t.Owned() {
			stale = append(stale, id)
			continue
		}
		urls = append(urls, AnonymousURL{Token: &t, ExpiresAt: s.expiresAt(now, ttls[i].Val())})
	}
	if len(stale) > 0 {
		if err := s.rdb.ZRem(ctx, anonymousKey, stale...).Err(); err != nil {
			return nil, 0, err
		}
	}
	return urls, len(stale), nil
}

// PruneAnonymous drops the index entries whose URL expired or was evicted,
// and reports how many. It does nothing while another replica prunes.
func (s *Store) PruneAnonymous(ctx context.Context, every time.Duration) (int, error) {
	if ok, err := s.rdb.SetNX(ctx, anonymousLockKey("prune"), 1, every*9/10).Result(); err != nil || !ok {
		return 0, err
	}
	removed := 0
	var cursor uint64
	for {
		members, next, err := s.rdb.ZScan(ctx, anonymousKey, cursor, "", anonymousChunk).Result()
		if err != nil {
			return removed, err
		}
		ids := make([]string, 0, len(members)/2)
		for i := 0; i < len(members); i += 2 { // member, score, member, score…
			ids = append(ids, members[i])
		}
		exists := make([]*redis.IntCmd, len(ids))
		if _, err := s.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
			for i, id := range ids {
				exists[i] = p.Exists(ctx, tokenKey(id))
			}
			return nil
		}); err != nil {
			return removed, err
		}
		var gone []any
		for i, id := range ids {
			if exists[i].Val() == 0 {
				gone = append(gone, id)
			}
		}
		if len(gone) > 0 {
			if err := s.rdb.ZRem(ctx, anonymousKey, gone...).Err(); err != nil {
				return removed, err
			}
			removed += len(gone)
		}
		if cursor = next; cursor == 0 {
			return removed, nil
		}
	}
}

// IndexAnonymousURLs walks every URL in Redis and indexes the anonymous ones
// the index misses: those created while accounts were off, or before it
// existed. It reports how many it added, and does nothing while another
// replica does it.
func (s *Store) IndexAnonymousURLs(ctx context.Context) (int, error) {
	if ok, err := s.rdb.SetNX(ctx, anonymousLockKey("index"), 1, 5*time.Minute).Result(); err != nil || !ok {
		return 0, err
	}
	added := 0
	iter := s.rdb.Scan(ctx, 0, tokenKey("*"), 1000).Iterator()
	var keys []string
	flush := func() error {
		gets := make([]*redis.StringCmd, len(keys))
		if _, err := s.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
			for i, k := range keys {
				gets[i] = p.Get(ctx, k)
			}
			return nil
		}); err != nil && !errors.Is(err, redis.Nil) {
			return err
		}
		var add []redis.Z
		for _, g := range gets {
			var t Token
			if v, err := g.Result(); err != nil || json.Unmarshal([]byte(v), &t) != nil || t.Owned() || t.ID == "" {
				continue
			}
			add = append(add, redis.Z{Score: float64(t.CreatedAt.UnixMilli()), Member: t.ID})
		}
		keys = keys[:0]
		if len(add) == 0 {
			return nil
		}
		n, err := s.rdb.ZAddNX(ctx, anonymousKey, add...).Result()
		added += int(n)
		return err
	}
	for iter.Next(ctx) {
		if keys = append(keys, iter.Val()); len(keys) == anonymousChunk {
			if err := flush(); err != nil {
				return added, err
			}
		}
	}
	if err := iter.Err(); err != nil {
		return added, err
	}
	return added, flush()
}
