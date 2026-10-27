package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/dbaio/webhookgo/internal/config"
	"github.com/dbaio/webhookgo/internal/store"
)

const retention = 24 * time.Hour

func (e *env) record(id string) (*store.Record, error) {
	e.t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: e.mr.Addr()})
	e.t.Cleanup(func() { rdb.Close() })
	return store.New(rdb, time.Hour, 500, 0, retention).GetRecord(context.Background(), id)
}

func TestAuditRecordOutlivesDeletion(t *testing.T) {
	e := setup(t, func(c *config.Config) { c.Retention = retention })
	var tok map[string]any
	e.json(e.do("POST", "/token", strings.NewReader(`{}`), "User-Agent", "phish-kit/1.0"), 201, &tok)
	id := tok["uuid"].(string)
	audit := "wh:{" + id + "}:audit"
	if got := e.mr.TTL(audit); got != time.Hour+retention {
		t.Fatalf("record TTL %v, want the URL's TTL plus the retention", got)
	}
	for range 3 {
		e.do("POST", "/"+id, strings.NewReader("user=victim&pass=x"))
	}
	e.json(e.do("DELETE", "/token/"+id+"/request", nil), 200, nil) // clearing requests keeps the count

	if r, err := e.record(id); err != nil || r.Status != "live" || r.Requests != 3 || r.UserAgent != "phish-kit/1.0" ||
		r.IP != "127.0.0.1" || r.CreatedAt == nil || r.FirstRequestAt == nil || r.LastRequestAt == nil {
		t.Fatalf("live record: %+v, %v", r, err)
	}

	e.json(e.do("DELETE", "/token/"+id, nil, "User-Agent", "abuse-desk"), 204, nil)
	for _, k := range []string{"token", "index", "sum", "data", "body"} {
		if e.mr.Exists("wh:{" + id + "}:" + k) {
			t.Errorf("%s kept after delete", k)
		}
	}
	if got := e.mr.TTL(audit); got != retention {
		t.Fatalf("record TTL %v after delete, want %v", got, retention)
	}

	var gone map[string]any
	e.json(e.do("GET", "/token/"+id, nil), 410, &gone)
	if gone["deleted_at"] == nil {
		t.Fatalf("410 without deleted_at: %v", gone)
	}
	e.json(e.do("GET", "/token/"+id+"/requests", nil), 410, nil)
	e.json(e.do("POST", "/"+id, strings.NewReader("more")), 410, nil)
	if e.mr.Exists("wh:{" + id + "}:index") {
		t.Fatal("a capture on a deleted URL must not store anything")
	}

	r, err := e.record(id)
	if err != nil || r.Status != "deleted" || r.Requests != 3 || r.DeletedAt == nil ||
		r.DeletedIP != "127.0.0.1" || r.DeletedUserAgent != "abuse-desk" || r.UserAgent != "phish-kit/1.0" || r.KeptUntil == nil {
		t.Fatalf("deleted record: %+v, %v", r, err)
	}

	e.mr.FastForward(retention + time.Second)
	e.json(e.do("GET", "/token/"+id, nil), 404, nil)
	if _, err := e.record(id); err != store.ErrNotFound {
		t.Fatalf("record after the retention period: %v", err)
	}
}

func TestAuditRecordOutlivesExpiry(t *testing.T) {
	e := setup(t, func(c *config.Config) { c.Retention = retention })
	id := e.newToken(`{}`)["uuid"].(string)
	e.do("POST", "/"+id, nil)
	e.mr.FastForward(61 * time.Minute)
	e.json(e.do("GET", "/token/"+id, nil), 404, nil) // expired, not deleted
	if r, err := e.record(id); err != nil || r.Status != "expired" || r.Requests != 1 {
		t.Fatalf("expired record: %+v, %v", r, err)
	}
}

func TestNoAuditRecords(t *testing.T) {
	e := setup(t) // Retention 0
	id := e.newToken(`{}`)["uuid"].(string)
	e.do("POST", "/"+id, nil)
	e.json(e.do("DELETE", "/token/"+id, nil), 204, nil)
	if e.mr.Exists("wh:{" + id + "}:audit") {
		t.Fatal("no record may be kept with a retention of 0")
	}
	e.json(e.do("GET", "/token/"+id, nil), 404, nil)
}

func TestAbusePage(t *testing.T) {
	e := setup(t, func(c *config.Config) { c.Retention = retention; c.AbuseContact = "abuse@example.com" })
	resp := e.do("GET", "/abuse", nil)
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatalf("abuse page: %d %v", resp.StatusCode, resp.Header)
	}
	var cfg map[string]any
	e.json(e.do("GET", "/api/config", nil), 200, &cfg)
	if cfg["abuse_contact"] != "abuse@example.com" || cfg["retention"].(float64) != retention.Seconds() {
		t.Fatalf("config: %v", cfg)
	}
}
