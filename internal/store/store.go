// Package store persists webhook URLs and captured requests in Redis.
//
// Every URL owns five keys that share a hash tag (so they live in the same
// Redis Cluster slot) and one TTL, refreshed on every use:
//
//	wh:{id}:token  STRING  the Token as JSON
//	wh:{id}:index  ZSET    request IDs scored by arrival time (ms)
//	wh:{id}:sum    HASH    request ID -> Summary JSON (small, for lists)
//	wh:{id}:data   HASH    request ID -> Request JSON without the body
//	wh:{id}:body   HASH    request ID -> raw body bytes
//
// Keeping bodies as raw bytes avoids JSON escaping blowing up their size, and
// lets lists read only the summaries. Live updates are published on the
// channel wh:events:{id}.
//
// Unless the retention is 0, a sixth key outlives the others, for abuse
// investigations:
//
//	wh:{id}:audit  HASH    who created the URL, how many requests it got and
//	                       who deleted it; it expires the retention period
//	                       after the URL does
package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrNotFound = errors.New("not found")

// DeletedError reports a URL that was deleted, as opposed to one that expired
// or never existed. It matches ErrNotFound.
type DeletedError struct{ At time.Time }

func (e *DeletedError) Error() string        { return "deleted" }
func (e *DeletedError) Is(target error) bool { return target == ErrNotFound }

const EventChannelPrefix = "wh:events:"

// scanChunk bounds how many full requests a search holds in memory at once.
const scanChunk = 25

func tokenKey(id string) string { return "wh:{" + id + "}:token" }
func indexKey(id string) string { return "wh:{" + id + "}:index" }
func sumKey(id string) string   { return "wh:{" + id + "}:sum" }
func dataKey(id string) string  { return "wh:{" + id + "}:data" }
func bodyKey(id string) string  { return "wh:{" + id + "}:body" }
func auditKey(id string) string { return "wh:{" + id + "}:audit" }

func EventChannel(id string) string { return EventChannelPrefix + id }

// touchScript reads a token and pushes back the expiry of its five keys to
// ARGV[1] seconds, and of its audit record (KEYS[6]) to ARGV[2] (0 = none).
var touchScript = redis.NewScript(`
local v = redis.call('GET', KEYS[1])
if not v then return false end
for i = 1, 5 do redis.call('EXPIRE', KEYS[i], ARGV[1]) end
if tonumber(ARGV[2]) > 0 then redis.call('EXPIRE', KEYS[6], ARGV[2]) end
return v
`)

// addScript stores a request, then evicts the oldest ones while the URL holds
// more than ARGV[6] requests or more than ARGV[8] bytes (0 = no byte quota),
// always keeping the newest. It refreshes the expiry, counts the request in
// the audit record (KEYS[6], kept ARGV[9] seconds; 0 = none) and returns
// {total, evicted ids...}, or {-1} when the token no longer exists.
var addScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then return {-1} end
local id = ARGV[1]
redis.call('ZADD', KEYS[2], ARGV[2], id)
redis.call('HSET', KEYS[3], id, ARGV[3])
redis.call('HSET', KEYS[4], id, ARGV[4])
redis.call('HSET', KEYS[5], id, ARGV[5])
local max, quota = tonumber(ARGV[6]), tonumber(ARGV[8])
local ids = redis.call('ZRANGE', KEYS[2], 0, -1)
local total = #ids
local function size(x)
  return redis.call('HSTRLEN', KEYS[4], x) + redis.call('HSTRLEN', KEYS[5], x)
end
local used = 0
if quota > 0 then
  for _, x in ipairs(ids) do used = used + size(x) end
end
local result = {0}
local i = 1
while total > 1 and (total > max or (quota > 0 and used > quota)) do
  local old = ids[i]
  if quota > 0 then used = used - size(old) end
  redis.call('ZREM', KEYS[2], old)
  for k = 3, 5 do redis.call('HDEL', KEYS[k], old) end
  table.insert(result, old)
  total = total - 1
  i = i + 1
end
result[1] = total
for k = 1, 5 do redis.call('EXPIRE', KEYS[k], ARGV[7]) end
local audit = tonumber(ARGV[9])
if audit > 0 then
  redis.call('HINCRBY', KEYS[6], 'requests', 1)
  redis.call('HSETNX', KEYS[6], 'first_request_at', ARGV[2])
  redis.call('HSET', KEYS[6], 'last_request_at', ARGV[2])
  redis.call('EXPIRE', KEYS[6], audit)
end
return result
`)

// deleteScript removes a URL's five keys. When audit records are kept
// (ARGV[1] seconds), it notes who created and who deleted the URL in KEYS[6]
// and starts the retention period; otherwise it removes the record too.
var deleteScript = redis.NewScript(`
for k = 1, 5 do redis.call('DEL', KEYS[k]) end
local ttl = tonumber(ARGV[1])
if ttl <= 0 then
  redis.call('DEL', KEYS[6])
  return 0
end
redis.call('HSET', KEYS[6], 'created_at', ARGV[2], 'ip', ARGV[3], 'user_agent', ARGV[4],
  'deleted_at', ARGV[5], 'deleted_ip', ARGV[6], 'deleted_user_agent', ARGV[7])
redis.call('EXPIRE', KEYS[6], ttl)
return 1
`)

type Store struct {
	rdb         redis.UniversalClient
	ttl         time.Duration
	maxRequests int
	maxBytes    int64
	retention   time.Duration
}

// New returns a store keeping at most maxRequests requests and maxBytes bytes
// (0 = unlimited) per URL, for ttl after the URL was last used. Audit records
// are kept for retention after the URL expires or is deleted (0 = none).
func New(rdb redis.UniversalClient, ttl time.Duration, maxRequests int, maxBytes int64, retention time.Duration) *Store {
	return &Store{rdb: rdb, ttl: ttl, maxRequests: maxRequests, maxBytes: maxBytes, retention: retention}
}

func (s *Store) TTL() time.Duration { return s.ttl }

func (s *Store) Ping(ctx context.Context) error { return s.rdb.Ping(ctx).Err() }

func keys(id string) []string {
	return []string{tokenKey(id), indexKey(id), sumKey(id), dataKey(id), bodyKey(id)}
}

// withAudit is keys(id) plus the audit record, for the scripts that keep it.
func withAudit(id string) []string { return append(keys(id), auditKey(id)) }

// auditTTL is how long, in seconds, an audit record outlives the URL's last
// use while the URL exists; 0 when records are not kept.
func (s *Store) auditTTL() int {
	if s.retention <= 0 {
		return 0
	}
	return int((s.ttl + s.retention).Seconds())
}

// maxAuditUserAgent caps the user agents kept in audit records.
const maxAuditUserAgent = 512

// marshal encodes without HTML escaping: <, > and & stay one byte.
func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// SaveToken creates or replaces a token and resets its expiry.
func (s *Store) SaveToken(ctx context.Context, t *Token) error {
	b, err := marshal(t)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, tokenKey(t.ID), b, s.ttl).Err()
}

// CreateToken saves a new token and starts its audit record.
func (s *Store) CreateToken(ctx context.Context, t *Token) error {
	b, err := marshal(t)
	if err != nil {
		return err
	}
	_, err = s.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Set(ctx, tokenKey(t.ID), b, s.ttl)
		if ttl := s.auditTTL(); ttl > 0 {
			p.HSet(ctx, auditKey(t.ID), "created_at", t.CreatedAt.UnixMilli(), "ip", t.IP,
				"user_agent", clip(t.UserAgent, maxAuditUserAgent))
			p.Expire(ctx, auditKey(t.ID), time.Duration(ttl)*time.Second)
		}
		return nil
	})
	return err
}

// GetToken loads a token and extends the life of the URL. A deleted URL
// whose audit record is still kept gives a *DeletedError.
func (s *Store) GetToken(ctx context.Context, id string) (*Token, error) {
	v, err := touchScript.Run(ctx, s.rdb, withAudit(id), int(s.ttl.Seconds()), s.auditTTL()).Text()
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

// missing tells a deleted URL from one that expired or never existed.
func (s *Store) missing(ctx context.Context, id string) error {
	ms, err := s.rdb.HGet(ctx, auditKey(id), "deleted_at").Int64()
	if errors.Is(err, redis.Nil) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return &DeletedError{At: time.UnixMilli(ms).UTC()}
}

// DeleteToken removes a URL and its requests. The audit record keeps who
// created the URL and who (ip, userAgent) deleted it.
func (s *Store) DeleteToken(ctx context.Context, t *Token, ip, userAgent string) error {
	return deleteScript.Run(ctx, s.rdb, withAudit(t.ID), int(s.retention.Seconds()),
		t.CreatedAt.UnixMilli(), t.IP, clip(t.UserAgent, maxAuditUserAgent),
		time.Now().UnixMilli(), ip, clip(userAgent, maxAuditUserAgent),
	).Err()
}

// GetRecord returns what is known about a URL for an abuse investigation,
// without extending its life.
func (s *Store) GetRecord(ctx context.Context, id string) (*Record, error) {
	var (
		fields *redis.MapStringStringCmd
		keep   *redis.DurationCmd
		token  *redis.StringCmd
	)
	_, err := s.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		fields = p.HGetAll(ctx, auditKey(id))
		keep = p.PTTL(ctx, auditKey(id))
		token = p.Get(ctx, tokenKey(id))
		return nil
	})
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	f := fields.Val()
	at := func(k string) *time.Time {
		ms, err := strconv.ParseInt(f[k], 10, 64)
		if err != nil {
			return nil
		}
		t := time.UnixMilli(ms).UTC()
		return &t
	}
	r := &Record{
		ID:               id,
		Status:           "expired",
		CreatedAt:        at("created_at"),
		IP:               f["ip"],
		UserAgent:        f["user_agent"],
		FirstRequestAt:   at("first_request_at"),
		LastRequestAt:    at("last_request_at"),
		DeletedAt:        at("deleted_at"),
		DeletedIP:        f["deleted_ip"],
		DeletedUserAgent: f["deleted_user_agent"],
	}
	r.Requests, _ = strconv.ParseInt(f["requests"], 10, 64)
	var t Token
	switch {
	case token.Err() == nil && json.Unmarshal([]byte(token.Val()), &t) == nil:
		r.Status = "live"
		if r.CreatedAt == nil { // created before audit records were kept
			r.CreatedAt, r.IP, r.UserAgent = &t.CreatedAt, t.IP, clip(t.UserAgent, maxAuditUserAgent)
		}
	case len(f) == 0:
		return nil, ErrNotFound
	case r.DeletedAt != nil:
		r.Status = "deleted"
	}
	if d := keep.Val(); d > 0 {
		until := time.Now().Add(d).UTC().Truncate(time.Second)
		r.KeptUntil = &until
	}
	return r, nil
}

func (s *Store) CountRequests(ctx context.Context, tokenID string) (int64, error) {
	return s.rdb.ZCard(ctx, indexKey(tokenID)).Result()
}

// AddRequest stores a request and its raw body. It returns the number of
// stored requests and the IDs evicted to stay within the limits.
func (s *Store) AddRequest(ctx context.Context, r *Request, body []byte) (int64, []string, error) {
	meta := *r
	meta.Content, meta.Encoding = "", ""
	m, err := marshal(&meta)
	if err != nil {
		return 0, nil, err
	}
	sum, err := marshal(r.Summary())
	if err != nil {
		return 0, nil, err
	}
	res, err := addScript.Run(ctx, s.rdb, withAudit(r.TokenID),
		r.ID, r.CreatedAt.UnixMilli(), sum, m, body, s.maxRequests, int(s.ttl.Seconds()), s.maxBytes, s.auditTTL(),
	).Slice()
	if err != nil {
		return 0, nil, err
	}
	total, _ := res[0].(int64)
	if total < 0 {
		return 0, nil, ErrNotFound
	}
	evicted := make([]string, 0, len(res)-1)
	for _, v := range res[1:] {
		if id, ok := v.(string); ok {
			evicted = append(evicted, id)
		}
	}
	return total, evicted, nil
}

func (s *Store) GetRequest(ctx context.Context, tokenID, id string) (*Request, error) {
	reqs, err := s.loadFull(ctx, tokenID, []string{id})
	if err != nil {
		return nil, err
	}
	if len(reqs) == 0 {
		return nil, ErrNotFound
	}
	return reqs[0], nil
}

func (s *Store) DeleteRequest(ctx context.Context, tokenID, id string) (bool, error) {
	var removed *redis.IntCmd
	_, err := s.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		removed = p.ZRem(ctx, indexKey(tokenID), id)
		p.HDel(ctx, sumKey(tokenID), id)
		p.HDel(ctx, dataKey(tokenID), id)
		p.HDel(ctx, bodyKey(tokenID), id)
		return nil
	})
	if err != nil {
		return false, err
	}
	return removed.Val() > 0, nil
}

func (s *Store) DeleteRequests(ctx context.Context, tokenID string) error {
	return s.rdb.Del(ctx, keys(tokenID)[1:]...).Err()
}

type ListOptions struct {
	Page    int // 1-based
	PerPage int
	Newest  bool     // newest first instead of oldest first
	Search  string   // case-insensitive text search
	Methods []string // upper-case HTTP methods to keep; empty keeps all
}

const maxPage = 1_000_000

// pageIDs returns the IDs of one page of matching requests and the number of
// matches. Method filters only read summaries; text search scans full
// requests a chunk at a time so memory stays bounded.
func (s *Store) pageIDs(ctx context.Context, tokenID string, o ListOptions) ([]string, int, error) {
	o.Page = min(max(o.Page, 1), maxPage)
	o.PerPage = max(o.PerPage, 1)
	offset := (o.Page - 1) * o.PerPage

	if o.Search == "" && len(o.Methods) == 0 {
		var (
			count *redis.IntCmd
			ids   *redis.StringSliceCmd
		)
		_, err := s.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
			count = p.ZCard(ctx, indexKey(tokenID))
			ids = p.ZRangeArgs(ctx, redis.ZRangeArgs{
				Key: indexKey(tokenID), Start: offset, Stop: offset + o.PerPage - 1, Rev: o.Newest,
			})
			return nil
		})
		if err != nil {
			return nil, 0, err
		}
		return ids.Val(), int(count.Val()), nil
	}

	all, err := s.rdb.ZRangeArgs(ctx, redis.ZRangeArgs{Key: indexKey(tokenID), Start: 0, Stop: -1, Rev: o.Newest}).Result()
	if err != nil {
		return nil, 0, err
	}
	if len(o.Methods) > 0 {
		sums, err := s.loadSummaries(ctx, tokenID, all)
		if err != nil {
			return nil, 0, err
		}
		all = all[:0]
		for _, sm := range sums {
			if slices.Contains(o.Methods, sm.Method) {
				all = append(all, sm.ID)
			}
		}
	}
	if o.Search != "" {
		needle := strings.ToLower(o.Search)
		matched := all[:0]
		for chunk := range slices.Chunk(slices.Clone(all), scanChunk) {
			reqs, err := s.loadFull(ctx, tokenID, chunk)
			if err != nil {
				return nil, 0, err
			}
			for _, r := range reqs {
				if r.contains(needle) {
					matched = append(matched, r.ID)
				}
			}
		}
		all = matched
	}
	total := len(all)
	if offset >= total {
		return []string{}, total, nil
	}
	return all[offset:min(total, offset+o.PerPage)], total, nil
}

// IDsSince returns the IDs of the requests that arrived at or after since,
// oldest first.
func (s *Store) IDsSince(ctx context.Context, tokenID string, since time.Time) ([]string, error) {
	return s.rdb.ZRangeArgs(ctx, redis.ZRangeArgs{
		Key: indexKey(tokenID), ByScore: true, Start: since.UnixMilli(), Stop: "+inf",
	}).Result()
}

// LoadRequests returns the full requests for ids, skipping any that are gone.
func (s *Store) LoadRequests(ctx context.Context, tokenID string, ids []string) ([]*Request, error) {
	return s.loadFull(ctx, tokenID, ids)
}

// ListSummaries returns one page of request summaries and the number matching.
func (s *Store) ListSummaries(ctx context.Context, tokenID string, o ListOptions) ([]Summary, int, error) {
	ids, total, err := s.pageIDs(ctx, tokenID, o)
	if err != nil {
		return nil, 0, err
	}
	sums, err := s.loadSummaries(ctx, tokenID, ids)
	return sums, total, err
}

// ListRequests returns one page of full requests and the number matching.
func (s *Store) ListRequests(ctx context.Context, tokenID string, o ListOptions) ([]*Request, int, error) {
	ids, total, err := s.pageIDs(ctx, tokenID, o)
	if err != nil {
		return nil, 0, err
	}
	reqs, err := s.loadFull(ctx, tokenID, ids)
	return reqs, total, err
}

func (s *Store) loadSummaries(ctx context.Context, tokenID string, ids []string) ([]Summary, error) {
	out := make([]Summary, 0, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	vals, err := s.rdb.HMGet(ctx, sumKey(tokenID), ids...).Result()
	if err != nil {
		return nil, err
	}
	var missing []string
	for i, v := range vals {
		var sm Summary
		raw, ok := v.(string)
		if !ok || json.Unmarshal([]byte(raw), &sm) != nil {
			missing = append(missing, ids[i])
			continue
		}
		out = append(out, sm)
	}
	s.repair(ctx, tokenID, missing)
	return out, nil
}

func (s *Store) loadFull(ctx context.Context, tokenID string, ids []string) ([]*Request, error) {
	out := make([]*Request, 0, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var data, body *redis.SliceCmd
	_, err := s.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		data = p.HMGet(ctx, dataKey(tokenID), ids...)
		body = p.HMGet(ctx, bodyKey(tokenID), ids...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	bodies := body.Val()
	var missing []string
	for i, v := range data.Val() {
		var r Request
		raw, ok := v.(string)
		if !ok || json.Unmarshal([]byte(raw), &r) != nil {
			missing = append(missing, ids[i])
			continue
		}
		b, _ := bodies[i].(string)
		r.SetBody([]byte(b))
		out = append(out, &r)
	}
	s.repair(ctx, tokenID, missing)
	return out, nil
}

// repair drops index entries whose data is gone (evicted by Redis under
// memory pressure), so counts and lists stay consistent.
func (s *Store) repair(ctx context.Context, tokenID string, ids []string) {
	if len(ids) == 0 {
		return
	}
	ctx = context.WithoutCancel(ctx)
	_, _ = s.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		members := make([]any, len(ids))
		for i, id := range ids {
			members[i] = id
		}
		p.ZRem(ctx, indexKey(tokenID), members...)
		p.HDel(ctx, sumKey(tokenID), ids...)
		p.HDel(ctx, dataKey(tokenID), ids...)
		p.HDel(ctx, bodyKey(tokenID), ids...)
		return nil
	})
}

// Contains reports whether text appears anywhere in the request's visible
// data, ignoring case.
func (r *Request) Contains(text string) bool { return r.contains(strings.ToLower(text)) }

// contains reports whether the lower-cased needle appears anywhere in the
// request's visible data.
func (r *Request) contains(needle string) bool {
	has := func(s string) bool { return strings.Contains(strings.ToLower(s), needle) }
	if has(r.Method) || has(r.URL) || has(r.IP) || has(r.ID) || has(r.UserAgent) {
		return true
	}
	if r.Encoding == "" && has(r.Content) {
		return true
	}
	for name, values := range r.Headers {
		if has(name) || slices.ContainsFunc(values, has) {
			return true
		}
	}
	for _, kv := range slices.Concat(r.Query, r.Form) {
		if has(kv.Name) || has(kv.Value) {
			return true
		}
	}
	for _, f := range r.Files {
		if has(f.Filename) || has(f.Field) {
			return true
		}
	}
	return false
}

func (s *Store) Publish(ctx context.Context, tokenID string, payload []byte) error {
	return s.rdb.Publish(ctx, EventChannel(tokenID), payload).Err()
}

func (s *Store) Subscribe(ctx context.Context) *redis.PubSub {
	return s.rdb.PSubscribe(ctx, EventChannelPrefix+"*")
}

// Allow implements a fixed one-minute window rate limiter. It returns whether
// the hit is allowed and, if not, how long until the window resets.
func (s *Store) Allow(ctx context.Context, bucket, key string, limit int) (bool, time.Duration, error) {
	if limit <= 0 {
		return true, 0, nil
	}
	now := time.Now()
	window := now.Unix() / 60
	k := fmt.Sprintf("wh:rl:%s:%s:%d", bucket, key, window)
	var incr *redis.IntCmd
	_, err := s.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		incr = p.Incr(ctx, k)
		p.Expire(ctx, k, 61*time.Second)
		return nil
	})
	if err != nil {
		return false, 0, err
	}
	if incr.Val() > int64(limit) {
		reset := time.Unix((window+1)*60, 0).Sub(now)
		return false, reset, nil
	}
	return true, 0, nil
}
