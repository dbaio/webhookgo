package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dbaio/webhookgo/internal/accounts"
	"github.com/dbaio/webhookgo/internal/config"
	"github.com/dbaio/webhookgo/internal/store"
)

// report sends an abuse report from ip (the test server trusts one proxy).
func (e *env) report(ip string, body any) *http.Response {
	e.t.Helper()
	return e.post("POST", "/api/abuse/reports", body, "X-Forwarded-For", ip)
}

// reportRows reads the stored reports, oldest first.
func (e *env) reportRows() []map[string]any {
	e.t.Helper()
	rows, err := e.db.Query(context.Background(), `select token_id::text, reporter_ip, contact, message, status, coalesce(handled_by::text, ''), handled_at is not null, note
		from abuse_reports order by created_at, id`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var (
			token, ip, contact, message, status, handledBy, note string
			handled                                              bool
		)
		if err := rows.Scan(&token, &ip, &contact, &message, &status, &handledBy, &handled, &note); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, map[string]any{"token_id": token, "ip": ip, "contact": contact, "message": message, "status": status, "handled_by": handledBy, "handled": handled, "note": note})
	}
	return out
}

type reportGroupResp struct {
	TokenID string `json:"token_id"`
	Reports []struct {
		ID             string     `json:"id"`
		ReporterIP     string     `json:"reporter_ip"`
		Contact        string     `json:"contact"`
		Message        string     `json:"message"`
		Status         string     `json:"status"`
		CreatedAt      time.Time  `json:"created_at"`
		HandledByEmail string     `json:"handled_by_email"`
		HandledAt      *time.Time `json:"handled_at"`
		Note           string     `json:"note"`
	} `json:"reports"`
	Open    int             `json:"open"`
	FirstAt time.Time       `json:"first_at"`
	LastAt  time.Time       `json:"last_at"`
	Token   map[string]any  `json:"token"`
	Owner   *accounts.Owner `json:"owner"`
	Record  *store.Record   `json:"record"`
}

// reports reads one page of the admin's report list.
func (e *env) reports(c *http.Cookie, query string) (groups []reportGroupResp, total int) {
	e.t.Helper()
	var page struct {
		Data  []reportGroupResp `json:"data"`
		Total int               `json:"total"`
	}
	e.json(e.as(c, "GET", "/api/admin/reports?"+query, ""), 200, &page)
	return page.Data, page.Total
}

func (e *env) resolve(c *http.Cookie, id, action, note string) *http.Response {
	e.t.Helper()
	b, _ := json.Marshal(map[string]string{"token_id": id, "action": action, "note": note})
	return e.as(c, "POST", "/api/admin/reports/resolve", string(b))
}

func TestAbuseReportsOff(t *testing.T) {
	e := setup(t)
	e.json(e.post("POST", "/api/abuse/reports", map[string]any{"ids": []string{zeroUUID}}), 404, nil)
}

func TestAbuseReports(t *testing.T) {
	e := setupAccounts(t, func(c *config.Config) { c.Retention = retention; c.ProxyHops = 1 })
	_, anaC := e.invite("ana@example.com", "ana's password")
	owned := e.ownedToken(anaC, `{}`)["uuid"].(string)
	anon := e.newToken(`{}`)["uuid"].(string)
	gone := e.newToken(`{}`)["uuid"].(string)
	e.json(e.do("DELETE", "/token/"+gone, nil), 204, nil)

	// Validation.
	for _, body := range []any{
		map[string]any{},
		map[string]any{"ids": []string{}},
		map[string]any{"ids": []string{"nope"}},
		map[string]any{"ids": []string{anon, ""}},
		map[string]any{"ids": strings.Split(strings.Repeat(anon+",", 51), ",")[:51]},
		map[string]any{"ids": []string{anon}, "message": strings.Repeat("é", 2001)},
		map[string]any{"ids": []string{anon}, "contact": strings.Repeat("x", 255)},
	} {
		e.json(e.report("10.0.0.100", body), 422, nil)
	}
	e.json(e.do("POST", "/api/abuse/reports", strings.NewReader("{"), "Content-Type", "application/json"), 422, nil)
	e.json(e.report("10.0.0.100", map[string]any{"ids": []string{anon}, "message": strings.Repeat("x", 17<<10)}), 413, nil)
	if rows := e.reportRows(); len(rows) != 0 {
		t.Fatalf("invalid input stored: %v", rows)
	}

	// Unknown and deleted IDs are accepted but not stored.
	var out map[string]any
	e.json(e.report("10.0.0.1", map[string]any{"ids": []string{zeroUUID, gone}, "message": "phishing"}), 202, &out)
	if out["status"] != "received" {
		t.Fatalf("answer: %v", out)
	}
	if rows := e.reportRows(); len(rows) != 0 {
		t.Fatalf("unknown ids stored: %v", rows)
	}
	if !e.mr.Exists("wh:{" + zeroUUID + "}:none") {
		t.Fatal("an ID found nowhere must be remembered as unknown")
	}

	// Existing ones are, anonymous and owned alike, once per ID, without
	// touching the URL's life.
	e.mr.FastForward(10 * time.Minute)
	ttl := e.mr.TTL(tokenKey(anon))
	e.json(e.report("10.0.0.1", map[string]any{
		"ids": []string{strings.ToUpper(anon), owned, anon, zeroUUID}, "message": " It serves a fake login page. ", "contact": " victim@example.com ",
	}), 202, nil)
	rows := e.reportRows()
	if len(rows) != 2 || rows[0]["token_id"] != anon || rows[1]["token_id"] != owned {
		t.Fatalf("stored: %v", rows)
	}
	for _, row := range rows {
		if row["ip"] != "10.0.0.1" || row["contact"] != "victim@example.com" || row["message"] != "It serves a fake login page." || row["status"] != "open" || row["handled"] != false {
			t.Fatalf("row: %v", row)
		}
	}
	if e.mr.TTL(tokenKey(anon)) != ttl {
		t.Fatal("a report must not extend the URL's life")
	}

	// The same address reporting the same URL again within a day is ignored;
	// another address, or a day later, is not.
	e.json(e.report("10.0.0.1", map[string]any{"ids": []string{anon}, "message": "again"}), 202, nil)
	if rows = e.reportRows(); len(rows) != 2 {
		t.Fatalf("duplicate stored: %v", rows)
	}
	e.json(e.report("10.0.0.2", map[string]any{"ids": []string{anon}}), 202, nil)
	if rows = e.reportRows(); len(rows) != 3 || rows[2]["ip"] != "10.0.0.2" || rows[2]["message"] != "" {
		t.Fatalf("second reporter: %v", rows)
	}
	e.exec("update abuse_reports set created_at = created_at - interval '25 hours' where reporter_ip = '10.0.0.1'")
	e.json(e.report("10.0.0.1", map[string]any{"ids": []string{anon}, "message": "still up"}), 202, nil)
	if rows = e.reportRows(); len(rows) != 4 || rows[3]["message"] != "still up" {
		t.Fatalf("a day later: %v", rows)
	}

	// An owned URL that fell out of the cache still exists, and is left out of it.
	e.mr.Del(tokenKey(owned))
	e.json(e.report("10.0.0.3", map[string]any{"ids": []string{owned}}), 202, nil)
	if rows = e.reportRows(); len(rows) != 5 || rows[4]["token_id"] != owned || e.mr.Exists(tokenKey(owned)) {
		t.Fatalf("uncached owned URL: %v", rows)
	}

	// Ten submissions an hour per address, valid or not.
	freshWindow(t, time.Hour, 30*time.Second)
	for i := range 10 {
		if resp := e.report("10.0.0.9", map[string]any{"ids": []string{anon}}); resp.StatusCode != 202 {
			t.Fatalf("submission %d: %d", i+1, resp.StatusCode)
		}
	}
	resp := e.report("10.0.0.9", map[string]any{"ids": []string{anon}})
	e.json(resp, 429, nil)
	if ra := resp.Header.Get("Retry-After"); ra == "" || ra == "0" {
		t.Fatalf("Retry-After %q", ra)
	}
	e.json(e.report("10.0.0.10", map[string]any{"ids": []string{anon}}), 202, nil) // another address is fine
	e.mr.FastForward(61 * time.Minute)
	e.json(e.report("10.0.0.9", map[string]any{"ids": []string{anon}}), 202, nil)
}

func TestAdminReports(t *testing.T) {
	e := setupAccounts(t, func(c *config.Config) { c.Retention = retention; c.ProxyHops = 1 })
	root, admin := e.firstAdmin("root@example.com", "correct horse battery")
	rootID := root["id"].(string)
	ana, anaC := e.invite("ana@example.com", "ana's password")
	anaID := ana["id"].(string)
	owned := e.ownedToken(anaC, `{"description":"Stripe"}`)["uuid"].(string)
	anon := e.newToken(`{}`)["uuid"].(string)
	e.do("POST", "/"+owned, strings.NewReader("x"))
	e.json(e.report("10.0.0.1", map[string]any{"ids": []string{owned, anon}, "message": "phishing kit", "contact": "me@example.com"}), 202, nil)
	e.json(e.report("10.0.0.2", map[string]any{"ids": []string{anon}, "message": "spam"}), 202, nil)

	// The list: grouped by URL, the one with the newest report first, with
	// what is known about the URL.
	groups, total := e.reports(admin, "")
	if total != 2 || len(groups) != 2 || groups[0].TokenID != anon || groups[1].TokenID != owned {
		t.Fatalf("open: %d %+v", total, groups)
	}
	g := groups[0]
	if len(g.Reports) != 2 || g.Open != 2 || g.Reports[0].Message != "spam" || g.Reports[0].ReporterIP != "10.0.0.2" || g.Reports[1].Contact != "me@example.com" ||
		g.Reports[0].Status != "open" || g.Reports[0].HandledAt != nil || g.Reports[0].HandledByEmail != "" || g.Reports[0].ID == "" ||
		g.FirstAt.IsZero() || g.LastAt.Before(g.FirstAt) || g.Token == nil || g.Token["uuid"] != anon || g.Owner != nil || g.Record == nil || g.Record.Status != "live" {
		t.Fatalf("anonymous group: %+v", g)
	}
	g = groups[1]
	if len(g.Reports) != 1 || g.Open != 1 || g.Owner == nil || g.Owner.ID != anaID || g.Owner.Email != "ana@example.com" ||
		g.Token["description"] != "Stripe" || g.Token["request_count"].(float64) != 1 || g.Record.Requests != 1 || g.Record.IP != "127.0.0.1" {
		t.Fatalf("owned group: %+v", g)
	}
	if _, total = e.reports(admin, "status=actioned"); total != 0 {
		t.Fatal("nothing actioned yet")
	}
	if _, total = e.reports(admin, "status=dismissed"); total != 0 {
		t.Fatal("nothing dismissed yet")
	}
	if groups, total = e.reports(admin, "status=all&per_page=1"); total != 2 || len(groups) != 1 || groups[0].TokenID != anon {
		t.Fatalf("all, page 1: %d %+v", total, groups)
	}
	e.json(e.as(admin, "GET", "/api/admin/reports?status=nope", ""), 422, nil)
	var st struct {
		Reports map[string]int `json:"reports"`
	}
	e.json(e.as(admin, "GET", "/api/admin/stats", ""), 200, &st)
	if st.Reports["open"] != 3 {
		t.Fatalf("stats: %v", st.Reports)
	}

	// Resolve: validation.
	e.json(e.resolve(admin, "nope", "dismiss", ""), 422, nil)
	e.json(e.resolve(admin, anon, "ignore", ""), 422, nil)
	e.json(e.resolve(admin, anon, "dismiss", strings.Repeat("x", 2001)), 422, nil)
	e.json(e.resolve(admin, zeroUUID, "dismiss", ""), 404, nil)

	// Dismiss: the reports close, the URL stays.
	var out map[string]int
	e.json(e.resolve(admin, anon, "Dismiss", " looked fine "), 200, &out)
	if out["updated"] != 2 {
		t.Fatalf("dismissed: %v", out)
	}
	e.json(e.do("GET", "/token/"+anon, nil), 200, nil)
	if groups, total = e.reports(admin, ""); total != 1 || groups[0].TokenID != owned {
		t.Fatalf("open after dismiss: %+v", groups)
	}
	groups, _ = e.reports(admin, "status=dismissed")
	g = groups[0]
	if len(groups) != 1 || g.TokenID != anon || g.Open != 0 || g.Reports[0].Status != "dismissed" || g.Reports[0].HandledByEmail != "root@example.com" ||
		g.Reports[0].HandledAt == nil || g.Reports[0].Note != "looked fine" || g.Reports[1].Status != "dismissed" {
		t.Fatalf("dismissed group: %+v", g)
	}
	e.json(e.resolve(admin, anon, "dismiss", ""), 200, &out) // nothing open: not an error
	if out["updated"] != 0 {
		t.Fatalf("dismissed again: %v", out)
	}
	e.json(e.as(admin, "GET", "/api/admin/stats", ""), 200, &st)
	if st.Reports["open"] != 1 {
		t.Fatalf("stats after dismiss: %v", st.Reports)
	}

	// Delete the URL: through the token layer, so the record is written.
	e.json(e.resolve(admin, owned, "delete_url", "confirmed"), 200, &out)
	if out["updated"] != 1 {
		t.Fatalf("delete_url: %v", out)
	}
	if _, ok := e.urlRow(owned); ok || e.mr.Exists(tokenKey(owned)) || e.mr.HGet("wh:{"+owned+"}:audit", "owned") != "1" {
		t.Fatal("the URL must go from Postgres and Redis")
	}
	e.json(e.as(admin, "GET", "/token/"+owned, ""), 410, nil)
	groups, _ = e.reports(admin, "status=actioned")
	g = groups[0]
	if len(groups) != 1 || g.TokenID != owned || g.Reports[0].Status != "actioned" || g.Reports[0].Note != "confirmed" || g.Token != nil || g.Owner != nil ||
		g.Record == nil || g.Record.Status != "deleted" {
		t.Fatalf("actioned group: %+v", g)
	}
	if _, total = e.reports(admin, ""); total != 0 {
		t.Fatal("nothing open after both")
	}
	// Deleting a URL that is already gone still closes its reports.
	e.json(e.report("10.0.0.3", map[string]any{"ids": []string{anon}}), 202, nil)
	e.json(e.as(admin, "DELETE", "/api/admin/urls/"+anon, ""), 204, nil)
	e.json(e.resolve(admin, anon, "delete_url", ""), 200, &out)
	if out["updated"] != 1 {
		t.Fatalf("delete_url on a gone URL: %v", out)
	}

	// Disable the owner: with the guards of the Users tab.
	second := e.ownedToken(anaC, `{}`)["uuid"].(string)
	mine := e.ownedToken(admin, `{}`)["uuid"].(string)
	orphan := e.newToken(`{}`)["uuid"].(string)
	e.json(e.report("10.0.0.1", map[string]any{"ids": []string{second, mine, orphan}, "message": "more"}), 202, nil)
	var fail map[string]any
	e.json(e.resolve(admin, orphan, "disable_owner", ""), 409, &fail)
	if fail["error"] != "This URL has no owner" {
		t.Fatalf("anonymous: %v", fail)
	}
	e.json(e.resolve(admin, mine, "disable_owner", ""), 409, &fail)
	if fail["error"] != "You can't do that to your own account" {
		t.Fatalf("self: %v", fail)
	}
	// The last active admin stays. Over HTTP the actor and the owner are
	// both active admins, so it only happens in a race; the store's guard is
	// what the route relies on.
	stale := &accounts.User{ID: anaID, Email: "ana@example.com", Role: accounts.RoleAdmin}
	disabled := true
	if _, err := e.acc.UpdateUser(context.Background(), stale, rootID, accounts.UserUpdate{Disabled: &disabled}, ""); !errors.Is(err, accounts.ErrLastAdmin) {
		t.Fatalf("disable the last admin: %v", err)
	}
	if groups, total = e.reports(admin, ""); total != 3 || len(groups) != 3 {
		t.Fatalf("refused actions must leave the reports open: %+v", groups)
	}
	e.json(e.resolve(admin, second, "disable_owner", "repeat offender"), 200, &out)
	if out["updated"] != 1 {
		t.Fatalf("disable_owner: %v", out)
	}
	if e.me(anaC) != nil {
		t.Fatal("the owner must be signed out")
	}
	e.json(e.login("ana@example.com", "ana's password"), 403, nil)
	e.json(e.as(admin, "GET", "/token/"+second, ""), 200, nil)       // the URL stays
	e.json(e.resolve(admin, second, "disable_owner", ""), 200, &out) // already disabled: nothing to do, nothing open
	if out["updated"] != 0 {
		t.Fatalf("disable_owner again: %v", out)
	}

	// Every resolution left a row, with the action; the delete and the
	// disable are logged as such too.
	rows := e.logRows(admin, "action=report.resolve")
	if len(rows) != 6 {
		t.Fatalf("log: %v", rows)
	}
	want := []map[string]any{
		{"target": second, "action": "disable_owner", "reports": 0.0},
		{"target": second, "action": "disable_owner", "reports": 1.0, "note": "repeat offender", "owner_email": "ana@example.com"},
		{"target": anon, "action": "delete_url", "reports": 1.0, "deleted": false},
		{"target": owned, "action": "delete_url", "reports": 1.0, "note": "confirmed", "deleted": true, "owner_email": "ana@example.com", "description": "Stripe"},
		{"target": anon, "action": "dismiss", "reports": 0.0},
		{"target": anon, "action": "dismiss", "reports": 2.0, "note": "looked fine"},
	}
	for i, w := range want {
		row, d := rows[i], details(rows[i])
		if row["target_type"] != "url" || row["target_id"] != w["target"] || row["actor_email"] != "root@example.com" || row["ip"] != "127.0.0.1" {
			t.Fatalf("row %d: %v", i, row)
		}
		for k, v := range w {
			if k != "target" && d[k] != v {
				t.Fatalf("row %d, %s: %v, want %v", i, k, d[k], v)
			}
		}
	}
	if rows = e.logRows(admin, "action=url.delete"); len(rows) != 2 || rows[1]["target_id"] != owned || details(rows[1])["owner_id"] != anaID {
		t.Fatalf("url.delete rows: %v", rows)
	}
	if rows = e.logRows(admin, "action=user.disable"); len(rows) != 1 || rows[0]["target_id"] != anaID {
		t.Fatalf("user.disable rows: %v", rows)
	}

	// Only admins.
	_, bob := e.invite("bob@example.com", "bob's password")
	for _, rt := range []struct{ method, path, body string }{
		{"GET", "/api/admin/reports", ""},
		{"POST", "/api/admin/reports/resolve", fmt.Sprintf(`{"token_id":%q,"action":"dismiss"}`, second)},
	} {
		e.json(e.as(bob, rt.method, rt.path, rt.body), 403, nil)
		e.json(e.as(anaC, rt.method, rt.path, rt.body), 403, nil)
	}
}
