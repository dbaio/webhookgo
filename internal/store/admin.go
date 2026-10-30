package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// adminViewTTL is how long an admin's look at someone else's URL is
// remembered, so that it is logged once an hour rather than per request:
//
//	wh:adminview:<admin>:<id>  STRING  set while the look is remembered
const adminViewTTL = time.Hour

func adminViewKey(adminID, tokenID string) string { return "wh:adminview:" + adminID + ":" + tokenID }

// PeekToken loads a token without extending the life of the URL, for the
// admin lookup. A deleted URL whose audit record is still kept gives a
// *DeletedError.
func (s *Store) PeekToken(ctx context.Context, id string) (*Token, error) {
	v, err := s.rdb.Get(ctx, tokenKey(id)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, s.missing(ctx, id)
	}
	if err != nil {
		return nil, err
	}
	var t Token
	if err := json.Unmarshal([]byte(v), &t); err != nil {
		return nil, fmt.Errorf("decode token %s: %w", id, err)
	}
	return &t, nil
}

// ExpiresAt is when an anonymous URL expires unless it is used, read without
// extending its life.
func (s *Store) ExpiresAt(ctx context.Context, id string) (time.Time, error) {
	left, err := s.rdb.PTTL(ctx, tokenKey(id)).Result()
	return s.expiresAt(time.Now().UTC(), left), err
}

// expiresAt turns what is left of a token key's TTL into a time; without
// one (a key that just went), a full TTL from now.
func (s *Store) expiresAt(now time.Time, left time.Duration) time.Time {
	if left <= 0 {
		left = s.ttl
	}
	return now.Add(left).Truncate(time.Second)
}

// NoteAdminView remembers for an hour that an admin opened a URL, and
// reports whether this is the first time within that hour.
func (s *Store) NoteAdminView(ctx context.Context, adminID, tokenID string) (bool, error) {
	return s.rdb.SetNX(ctx, adminViewKey(adminID, tokenID), 1, adminViewTTL).Result()
}

// RedisStats is the memory use of Redis, as INFO reports it.
type RedisStats struct {
	UsedMemory  int64 `json:"used_memory"`
	MaxMemory   int64 `json:"maxmemory"` // 0 = no limit
	EvictedKeys int64 `json:"evicted_keys"`
}

// RedisStats reads used_memory and maxmemory (INFO memory) and evicted_keys
// (INFO stats). A field the server does not report is left 0.
func (s *Store) RedisStats(ctx context.Context) (RedisStats, error) {
	info, err := s.rdb.Info(ctx).Result()
	if err != nil {
		return RedisStats{}, err
	}
	fields := map[string]int64{}
	for line := range strings.SplitSeq(info, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			fields[k] = n
		}
	}
	return RedisStats{UsedMemory: fields["used_memory"], MaxMemory: fields["maxmemory"], EvictedKeys: fields["evicted_keys"]}, nil
}
