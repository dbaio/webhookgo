package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dbaio/webhookgo/internal/accounts"
	"github.com/dbaio/webhookgo/internal/config"
	"github.com/dbaio/webhookgo/internal/store"
)

const zeroUUID = "00000000-0000-0000-0000-000000000000"

// adminRoutes is every panel route, with a body for the writes.
var adminRoutes = []struct{ method, path, body string }{
	{"GET", "/api/admin/stats", ""},
	{"GET", "/api/admin/users", ""},
	{"POST", "/api/admin/users", `{"email":"x@example.com"}`},
	{"PATCH", "/api/admin/users/" + zeroUUID, `{"role":"user"}`},
	{"POST", "/api/admin/users/" + zeroUUID + "/reset", ""},
	{"DELETE", "/api/admin/users/" + zeroUUID, ""},
	{"GET", "/api/admin/urls", ""},
	{"GET", "/api/admin/urls/" + zeroUUID, ""},
	{"DELETE", "/api/admin/urls/" + zeroUUID, ""},
	{"GET", "/api/admin/reports", ""},
	{"POST", "/api/admin/reports/resolve", `{"token_id":"` + zeroUUID + `","action":"dismiss"}`},
	{"GET", "/api/admin/log", ""},
}

type adminPage struct {
	Data       []map[string]any `json:"data"`
	Total      int              `json:"total"`
	IsLastPage bool             `json:"is_last_page"`
}

// adminList reads one page of a panel list.
func (e *env) adminList(c *http.Cookie, path string) adminPage {
	e.t.Helper()
	var p adminPage
	e.json(e.as(c, "GET", path, ""), 200, &p)
	return p
}

// logRows reads the admin log, newest first.
func (e *env) logRows(c *http.Cookie, query string) []map[string]any {
	e.t.Helper()
	return e.adminList(c, "/api/admin/log?"+query).Data
}

func details(row map[string]any) map[string]any {
	d, _ := row["details"].(map[string]any)
	return d
}

// oneTimeLink reads the answer of an invite or reset and returns its token.
func (e *env) oneTimeLink(resp *http.Response, page string) string {
	e.t.Helper()
	var out struct {
		Link      string    `json:"link"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	e.json(resp, 201, &out)
	prefix := e.server.cfg.PublicURL
	if prefix == "" {
		prefix = e.srv.URL
	}
	prefix += page + "#"
	if !strings.HasPrefix(out.Link, prefix) || time.Until(out.ExpiresAt) < 71*time.Hour || time.Until(out.ExpiresAt) > 72*time.Hour {
		e.t.Fatalf("link: %+v", out)
	}
	return strings.TrimPrefix(out.Link, prefix)
}

func TestAdminRoutesGuarded(t *testing.T) {
	off := setup(t)
	for _, rt := range adminRoutes {
		off.json(off.as(nil, rt.method, rt.path, rt.body), 404, nil)
	}
	off.json(off.do("GET", "/admin", nil), 404, nil)

	e := setupAccounts(t)
	_, admin := e.firstAdmin("root@example.com", "correct horse battery")
	_, user := e.invite("ana@example.com", "ana's password")
	if resp := e.do("GET", "/admin", nil); resp.StatusCode != 200 {
		t.Fatalf("/admin page: %d", resp.StatusCode)
	}
	for _, rt := range adminRoutes {
		for _, c := range []*http.Cookie{nil, user} {
			var fail map[string]any
			e.json(e.as(c, rt.method, rt.path, rt.body), 403, &fail)
			if fail["error"] != "Admins only" {
				t.Fatalf("%s %s: %v", rt.method, rt.path, fail)
			}
		}
		if resp := e.as(admin, rt.method, rt.path, rt.body); resp.StatusCode == 401 || resp.StatusCode == 403 {
			t.Fatalf("%s %s as admin: %d", rt.method, rt.path, resp.StatusCode)
		}
	}
}

func TestAdminStats(t *testing.T) {
	e := setupAccounts(t)
	_, admin := e.firstAdmin("root@example.com", "correct horse battery")
	_, user := e.invite("ana@example.com", "ana's password")
	e.ownedToken(user, `{}`)
	e.newToken(`{}`) // anonymous: not counted
	e.exec("update users set disabled_at = now() where email = 'ana@example.com'")
	var st struct {
		Users    map[string]int   `json:"users"`
		URLs     map[string]int   `json:"urls"`
		Redis    map[string]int64 `json:"redis"`
		Database map[string]int64 `json:"database"`
		Version  string           `json:"version"`
	}
	e.json(e.as(admin, "GET", "/api/admin/stats", ""), 200, &st)
	if st.Users["total"] != 2 || st.Users["admins"] != 1 || st.Users["disabled"] != 1 || st.URLs["owned"] != 1 || st.Database["size_bytes"] <= 0 || st.Version != "test" {
		t.Fatalf("stats: %+v", st)
	}
	for _, k := range []string{"used_memory", "maxmemory", "evicted_keys"} {
		if _, ok := st.Redis[k]; !ok {
			t.Fatalf("redis stats need %s: %v", k, st.Redis)
		}
	}
}

func TestAdminInvite(t *testing.T) {
	e := setupAccounts(t)
	root, admin := e.firstAdmin("root@example.com", "correct horse battery")
	e.json(e.as(admin, "POST", "/api/admin/users", `{"email":"not an email"}`), 422, nil)
	e.json(e.as(admin, "POST", "/api/admin/users", `{"email":"x@example.com","role":"root"}`), 422, nil)
	e.json(e.as(admin, "POST", "/api/admin/users", `{"email":"ROOT@example.com"}`), 409, nil)

	token := e.oneTimeLink(e.as(admin, "POST", "/api/admin/users", `{"email":" ana@example.com ","role":"Admin"}`), "/invite")
	var l map[string]string
	e.json(e.post("POST", "/api/links/inspect", map[string]string{"token": token}), 200, &l)
	if l["kind"] != "invite" || l["email"] != "ana@example.com" || l["role"] != "admin" {
		t.Fatalf("inspect: %v", l)
	}
	resp := e.post("POST", "/api/links/redeem", map[string]string{"token": token, "name": "Ana", "password": "ana's password"})
	var out struct {
		User map[string]any `json:"user"`
	}
	e.json(resp, 200, &out)
	if out.User["email"] != "ana@example.com" || out.User["role"] != "admin" || sessionCookie(resp) == nil {
		t.Fatalf("redeemed: %v", out.User)
	}
	// The link is spent, and the email cannot be invited again.
	e.json(e.post("POST", "/api/links/inspect", map[string]string{"token": token}), 404, nil)
	e.json(e.as(admin, "POST", "/api/admin/users", `{"email":"ana@example.com"}`), 409, nil)

	rows := e.logRows(admin, "action=user.invite")
	if len(rows) != 1 || rows[0]["actor_id"] != root["id"] || rows[0]["actor_email"] != "root@example.com" ||
		rows[0]["target_type"] != "link" || rows[0]["target_id"] != "ana@example.com" || rows[0]["ip"] != "127.0.0.1" {
		t.Fatalf("log: %v", rows)
	}
	if d := details(rows[0]); d["email"] != "ana@example.com" || d["role"] != "admin" || d["expires_at"] == nil {
		t.Fatalf("details: %v", d)
	}

	// With PUBLIC_URL, the link is built on it.
	pub := setupAccounts(t, func(c *config.Config) { c.PublicURL = "https://hooks.example.com" })
	_, padmin := pub.firstAdmin("root@example.com", "correct horse battery")
	pub.oneTimeLink(pub.as(padmin, "POST", "/api/admin/users", `{"email":"ana@example.com"}`), "/invite")
}

func TestAdminUsers(t *testing.T) {
	e := setupAccounts(t)
	root, admin := e.firstAdmin("root@example.com", "correct horse battery")
	rootID := root["id"].(string)
	ana, anaC := e.invite("ana@example.com", "ana's password")
	anaID := ana["id"].(string)
	anaC2 := sessionCookie(e.login("ana@example.com", "ana's password"))
	id := e.ownedToken(anaC, `{}`)["uuid"].(string)

	page := e.adminList(admin, "/api/admin/users")
	if page.Total != 2 || len(page.Data) != 2 || page.Data[0]["email"] != "ana@example.com" || page.Data[0]["url_count"].(float64) != 1 ||
		page.Data[0]["disabled_at"] != nil || page.Data[0]["last_login_at"] == nil || page.Data[1]["id"] != rootID {
		t.Fatalf("users: %+v", page)
	}
	if _, has := page.Data[0]["password_hash"]; has {
		t.Fatal("the hash must not leave")
	}
	if page = e.adminList(admin, "/api/admin/users?q=SOMEONE"); page.Total != 1 || page.Data[0]["id"] != anaID { // the invited name
		t.Fatalf("search by name: %+v", page)
	}
	if page = e.adminList(admin, "/api/admin/users?q=root&per_page=1"); page.Total != 1 || page.Data[0]["id"] != rootID || !page.IsLastPage {
		t.Fatalf("search by email: %+v", page)
	}

	// Guards and validation.
	var fail map[string]any
	e.json(e.as(admin, "PATCH", "/api/admin/users/"+rootID, `{"role":"user"}`), 409, &fail)
	if fail["error"] != "You can't do that to your own account" {
		t.Fatalf("self: %v", fail)
	}
	e.json(e.as(admin, "PATCH", "/api/admin/users/"+rootID, `{"disabled":true}`), 409, nil)
	e.json(e.as(admin, "DELETE", "/api/admin/users/"+rootID, ""), 409, nil)
	e.json(e.as(admin, "PATCH", "/api/admin/users/"+anaID, `{}`), 422, nil)
	e.json(e.as(admin, "PATCH", "/api/admin/users/"+anaID, `{"role":"root"}`), 422, nil)
	e.json(e.as(admin, "PATCH", "/api/admin/users/"+zeroUUID, `{"role":"user"}`), 404, nil)
	e.json(e.as(admin, "PATCH", "/api/admin/users/nope", `{"role":"user"}`), 404, nil)

	// Roles: Ana becomes an admin, demotes root and promotes him again.
	var u map[string]any
	e.json(e.as(admin, "PATCH", "/api/admin/users/"+anaID, `{"role":"admin"}`), 200, &u)
	if u["id"] != anaID || u["role"] != "admin" || u["url_count"].(float64) != 1 || u["disabled_at"] != nil {
		t.Fatalf("promoted: %v", u)
	}
	if e.me(anaC)["role"] != "admin" {
		t.Fatal("the role must apply to live sessions")
	}
	e.json(e.as(anaC, "PATCH", "/api/admin/users/"+rootID, `{"role":"user"}`), 200, &u)
	if u["role"] != "user" {
		t.Fatalf("demoted: %v", u)
	}
	e.json(e.as(anaC, "PATCH", "/api/admin/users/"+rootID, `{"role":"user"}`), 200, nil) // no change: no error
	e.json(e.as(admin, "GET", "/api/admin/stats", ""), 403, nil)
	e.json(e.as(anaC, "PATCH", "/api/admin/users/"+rootID, `{"role":"admin"}`), 200, nil)

	// Disabling ends Ana's sessions; she cannot sign in or reach her URL.
	e.json(e.as(admin, "PATCH", "/api/admin/users/"+anaID, `{"disabled":true}`), 200, &u)
	if u["disabled_at"] == nil {
		t.Fatalf("disabled: %v", u)
	}
	for _, c := range []*http.Cookie{anaC, anaC2} {
		if e.me(c) != nil {
			t.Fatal("sessions must end")
		}
		e.json(e.as(c, "GET", "/token/"+id, ""), 403, nil)
	}
	e.json(e.login("ana@example.com", "ana's password"), 403, nil)
	var st struct {
		Users map[string]int `json:"users"`
	}
	e.json(e.as(admin, "GET", "/api/admin/stats", ""), 200, &st)
	if st.Users["admins"] != 1 || st.Users["disabled"] != 1 {
		t.Fatalf("stats: %v", st.Users)
	}
	e.json(e.as(admin, "PATCH", "/api/admin/users/"+anaID, `{"disabled":false}`), 200, &u)
	if u["disabled_at"] != nil {
		t.Fatalf("enabled: %v", u)
	}
	anaC3 := sessionCookie(e.login("ana@example.com", "ana's password"))
	e.json(e.as(anaC3, "GET", "/token/"+id, ""), 200, nil)

	// The last active admin stays: an admin demoted while their request is
	// in flight (so still passing as one) cannot take the other down.
	ctx := context.Background()
	e.exec("update users set role = 'user' where id = $1", anaID)
	stale := &accounts.User{ID: anaID, Email: "ana@example.com", Role: accounts.RoleAdmin}
	role, disabled := accounts.RoleUser, true
	if _, err := e.acc.UpdateUser(ctx, stale, rootID, accounts.UserUpdate{Role: &role}, ""); !errors.Is(err, accounts.ErrLastAdmin) {
		t.Fatalf("demote the last admin: %v", err)
	}
	if _, err := e.acc.UpdateUser(ctx, stale, rootID, accounts.UserUpdate{Disabled: &disabled}, ""); !errors.Is(err, accounts.ErrLastAdmin) {
		t.Fatalf("disable the last admin: %v", err)
	}
	if err := e.acc.DeleteUser(ctx, stale, rootID, 0, ""); !errors.Is(err, accounts.ErrLastAdmin) {
		t.Fatalf("delete the last admin: %v", err)
	}
	if _, err := e.acc.UpdateUser(ctx, stale, anaID, accounts.UserUpdate{Role: &role}, ""); !errors.Is(err, accounts.ErrSelf) {
		t.Fatalf("self: %v", err)
	}
	if me := e.me(admin); me["role"] != "admin" {
		t.Fatalf("root must stay: %v", me)
	}

	// Every change left a row.
	var actions []string
	for _, row := range e.logRows(admin, "") {
		actions = append(actions, row["action"].(string))
	}
	want := []string{"user.enable", "user.disable", "user.role", "user.role", "user.role", "setup"}
	if strings.Join(actions, " ") != strings.Join(want, " ") {
		t.Fatalf("log: %v", actions)
	}
	rows := e.logRows(admin, "action=user.disable")
	if d := details(rows[0]); rows[0]["actor_email"] != "root@example.com" || rows[0]["target_id"] != anaID || d["email"] != "ana@example.com" {
		t.Fatalf("disable row: %v", rows[0])
	}
	rows = e.logRows(admin, "action=user.role")
	if d := details(rows[0]); rows[0]["actor_email"] != "ana@example.com" || rows[0]["target_id"] != rootID || d["from"] != "user" || d["to"] != "admin" {
		t.Fatalf("role row: %v", rows[0])
	}
}

func TestAdminResetLink(t *testing.T) {
	e := setupAccounts(t)
	_, admin := e.firstAdmin("root@example.com", "correct horse battery")
	ana, anaC := e.invite("ana@example.com", "ana's password")
	anaID := ana["id"].(string)
	e.json(e.as(admin, "POST", "/api/admin/users/"+zeroUUID+"/reset", ""), 404, nil)

	token := e.oneTimeLink(e.as(admin, "POST", "/api/admin/users/"+anaID+"/reset", ""), "/reset")
	var l map[string]string
	e.json(e.post("POST", "/api/links/inspect", map[string]string{"token": token}), 200, &l)
	if l["kind"] != "reset" || l["email"] != "ana@example.com" {
		t.Fatalf("inspect: %v", l)
	}
	e.json(e.post("POST", "/api/links/redeem", map[string]string{"token": token, "password": "a brand new password"}), 200, nil)
	if e.me(anaC) != nil {
		t.Fatal("a reset must end the old sessions")
	}
	e.json(e.login("ana@example.com", "ana's password"), 401, nil)
	e.json(e.login("ana@example.com", "a brand new password"), 200, nil)

	rows := e.logRows(admin, "action=user.reset_link")
	if len(rows) != 1 || rows[0]["target_type"] != "user" || rows[0]["target_id"] != anaID || details(rows[0])["email"] != "ana@example.com" {
		t.Fatalf("log: %v", rows)
	}
}

func TestAdminDeleteUser(t *testing.T) {
	e := setupAccounts(t, func(c *config.Config) { c.Retention = retention })
	_, admin := e.firstAdmin("root@example.com", "correct horse battery")
	ana, anaC := e.invite("ana@example.com", "ana's password")
	anaID := ana["id"].(string)
	ids := []string{e.ownedToken(anaC, `{"description":"one"}`)["uuid"].(string), e.ownedToken(anaC, `{}`)["uuid"].(string)}
	e.do("POST", "/"+ids[0], strings.NewReader("hi"))
	anon := e.newToken(`{}`)["uuid"].(string)

	e.json(e.as(admin, "DELETE", "/api/admin/users/"+zeroUUID, ""), 404, nil)
	if resp := e.as(admin, "DELETE", "/api/admin/users/"+anaID, ""); resp.StatusCode != 204 {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	var n int
	if err := e.db.QueryRow(context.Background(), "select count(*) from users where id = $1", anaID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("user row: %d %v", n, err)
	}
	for _, id := range ids {
		if _, ok := e.urlRow(id); ok {
			t.Fatal("the url row must go")
		}
		for _, k := range []string{"token", "index", "sum", "data", "body"} {
			if e.mr.Exists("wh:{" + id + "}:" + k) {
				t.Fatalf("redis key %s of %s must go", k, id)
			}
		}
		if e.mr.HGet("wh:{"+id+"}:audit", "owned") != "1" || e.mr.HGet("wh:{"+id+"}:audit", "deleted_ip") != "127.0.0.1" {
			t.Fatalf("audit record of %s", id)
		}
		e.json(e.as(admin, "GET", "/token/"+id, ""), 410, nil)
		e.json(e.do("POST", "/"+id, nil), 404, nil)
	}
	if e.me(anaC) != nil {
		t.Fatal("the session must go")
	}
	e.json(e.login("ana@example.com", "ana's password"), 401, nil)
	e.json(e.do("GET", "/token/"+anon, nil), 200, nil) // untouched
	e.json(e.as(admin, "DELETE", "/api/admin/users/"+anaID, ""), 404, nil)

	rows := e.logRows(admin, "action=user.delete")
	if d := details(rows[0]); len(rows) != 1 || rows[0]["target_id"] != anaID || d["email"] != "ana@example.com" || d["role"] != "user" || d["urls"].(float64) != 2 {
		t.Fatalf("log: %v", rows)
	}
}

func TestAdminURLs(t *testing.T) {
	e := setupAccounts(t, func(c *config.Config) { c.Retention = retention })
	root, admin := e.firstAdmin("root@example.com", "correct horse battery")
	rootID := root["id"].(string)
	ana, anaC := e.invite("ana@example.com", "ana's password")
	anaID := ana["id"].(string)
	first := e.ownedToken(anaC, `{"description":"Stripe"}`)["uuid"].(string)
	second := e.ownedToken(admin, `{"description":"GitHub"}`)["uuid"].(string)
	anon := e.newToken(`{"description":"nobody's"}`)["uuid"].(string)
	e.do("POST", "/"+first, strings.NewReader("x"))

	page := e.adminList(admin, "/api/admin/urls")
	if page.Total != 2 || len(page.Data) != 2 || page.Data[0]["uuid"] != second || page.Data[1]["uuid"] != first {
		t.Fatalf("urls: %+v", page)
	}
	item := page.Data[1]
	owner, _ := item["owner"].(map[string]any)
	if owner["id"] != anaID || owner["email"] != "ana@example.com" || item["request_count"].(float64) != 1 || item["latest_request_at"] == nil ||
		item["url"] != e.srv.URL+"/"+first || item["expires_at"] != nil || item["description"] != "Stripe" {
		t.Fatalf("item: %v", item)
	}
	if page.Data[0]["latest_request_at"] != nil || page.Data[0]["request_count"].(float64) != 0 {
		t.Fatalf("idle item: %v", page.Data[0])
	}
	for query, want := range map[string]string{
		"q=stripe": first, "q=" + strings.ToUpper(second): second, "owner=" + anaID: first, "owner=" + rootID: second,
	} {
		if page = e.adminList(admin, "/api/admin/urls?"+query); page.Total != 1 || page.Data[0]["uuid"] != want {
			t.Fatalf("%s: %+v", query, page)
		}
	}
	if page = e.adminList(admin, "/api/admin/urls?q="+anon); page.Total != 0 { // anonymous URLs are not listed
		t.Fatalf("anonymous: %+v", page)
	}
	e.json(e.as(admin, "GET", "/api/admin/urls?owner=nope", ""), 422, nil)

	// Lookup: any UUID, without extending the URL's life.
	var l struct {
		Token  map[string]any `json:"token"`
		Owner  map[string]any `json:"owner"`
		Record *store.Record  `json:"record"`
	}
	e.mr.FastForward(30 * time.Minute)
	ttl := e.mr.TTL(tokenKey(anon))
	e.json(e.as(admin, "GET", "/api/admin/urls/"+anon, ""), 200, &l)
	if l.Token == nil || l.Token["uuid"] != anon || l.Owner != nil || l.Record == nil || l.Record.Status != "live" || l.Record.IP != "127.0.0.1" || l.Record.CreatedAt == nil {
		t.Fatalf("anonymous lookup: %+v", l)
	}
	if e.mr.TTL(tokenKey(anon)) != ttl {
		t.Fatal("a lookup must not extend the URL's life")
	}
	if exp, err := time.Parse(time.RFC3339, l.Token["expires_at"].(string)); err != nil || exp.After(time.Now().Add(ttl+time.Minute)) {
		t.Fatalf("expires_at %v: the URL expires in %v", l.Token["expires_at"], ttl)
	}
	e.json(e.as(admin, "GET", "/api/admin/urls/"+first, ""), 200, &l)
	if l.Token["uuid"] != first || l.Token["owner_id"] != anaID || l.Owner["id"] != anaID || l.Owner["email"] != "ana@example.com" ||
		l.Record.Status != "live" || l.Record.Requests != 1 || l.Token["request_count"].(float64) != 1 {
		t.Fatalf("owned lookup: %+v", l)
	}
	// An owned URL that fell out of the cache is read from Postgres and left out of it.
	e.mr.Del(tokenKey(first))
	e.json(e.as(admin, "GET", "/api/admin/urls/"+first, ""), 200, &l)
	if l.Token["uuid"] != first || l.Record.Status != "live" || e.mr.Exists(tokenKey(first)) {
		t.Fatalf("uncached lookup: %+v", l)
	}
	e.json(e.as(anaC, "DELETE", "/token/"+first, ""), 204, nil)
	e.json(e.as(admin, "GET", "/api/admin/urls/"+first, ""), 200, &l)
	if l.Token != nil || l.Owner != nil || l.Record == nil || l.Record.Status != "deleted" || l.Record.DeletedIP != "127.0.0.1" {
		t.Fatalf("deleted lookup: %+v", l)
	}
	e.json(e.as(admin, "GET", "/api/admin/urls/"+zeroUUID, ""), 200, &l)
	if l.Token != nil || l.Owner != nil || l.Record != nil {
		t.Fatalf("unknown lookup: %+v", l)
	}
	e.json(e.as(admin, "GET", "/api/admin/urls/nope", ""), 404, nil)

	// Delete: anonymous and owned alike, through the token layer.
	if resp := e.as(admin, "DELETE", "/api/admin/urls/"+anon, ""); resp.StatusCode != 204 {
		t.Fatalf("delete anonymous: %d", resp.StatusCode)
	}
	if r, err := e.record(anon); err != nil || r.Status != "deleted" || r.DeletedIP != "127.0.0.1" || e.mr.Exists(tokenKey(anon)) {
		t.Fatalf("anonymous record: %+v, %v", r, err)
	}
	e.json(e.as(admin, "DELETE", "/api/admin/urls/"+anon, ""), 404, nil)
	if resp := e.as(admin, "DELETE", "/api/admin/urls/"+second, ""); resp.StatusCode != 204 {
		t.Fatalf("delete owned: %d", resp.StatusCode)
	}
	if _, ok := e.urlRow(second); ok || e.mr.Exists(tokenKey(second)) || e.mr.HGet("wh:{"+second+"}:audit", "owned") != "1" {
		t.Fatal("the owned URL must go from Postgres and Redis")
	}
	e.json(e.as(admin, "GET", "/token/"+second, ""), 410, nil)
	e.json(e.as(admin, "DELETE", "/api/admin/urls/"+zeroUUID, ""), 404, nil)

	rows := e.logRows(admin, "action=url.delete")
	if len(rows) != 2 || rows[0]["target_type"] != "url" || rows[0]["target_id"] != second || rows[1]["target_id"] != anon {
		t.Fatalf("log: %v", rows)
	}
	if d := details(rows[0]); d["owner_id"] != rootID || d["owner_email"] != "root@example.com" || d["description"] != "GitHub" {
		t.Fatalf("owned delete details: %v", d)
	}
	if d := details(rows[1]); d["owner_id"] != nil || d["description"] != "nobody's" {
		t.Fatalf("anonymous delete details: %v", d)
	}
}

func TestAdminViewLogged(t *testing.T) {
	e := setupAccounts(t)
	root, admin := e.firstAdmin("root@example.com", "correct horse battery")
	_, anaC := e.invite("ana@example.com", "ana's password")
	id := e.ownedToken(anaC, `{}`)["uuid"].(string)
	own := e.ownedToken(admin, `{}`)["uuid"].(string)
	for range 3 {
		e.json(e.as(admin, "GET", "/token/"+id, ""), 200, nil)
		e.json(e.as(admin, "GET", "/token/"+id+"/requests", ""), 200, nil)
	}
	e.json(e.as(admin, "GET", "/token/"+own, ""), 200, nil) // their own
	e.json(e.as(anaC, "GET", "/token/"+id, ""), 200, nil)   // the owner
	rows := e.logRows(admin, "action=url.view")
	if len(rows) != 1 || rows[0]["target_type"] != "url" || rows[0]["target_id"] != id || rows[0]["actor_id"] != root["id"] || details(rows[0])["owner_email"] != "ana@example.com" {
		t.Fatalf("log: %v", rows)
	}
	if !e.mr.Exists("wh:adminview:" + root["id"].(string) + ":" + id) {
		t.Fatal("the view must be remembered in Redis")
	}
	e.mr.FastForward(61 * time.Minute)
	e.json(e.as(admin, "GET", "/token/"+id, ""), 200, nil)
	if rows := e.logRows(admin, "action=url.view"); len(rows) != 2 {
		t.Fatalf("an hour later: %v", rows)
	}
}

func TestAdminLog(t *testing.T) {
	e := setupAccounts(t)
	root, admin := e.firstAdmin("root@example.com", "correct horse battery")
	rows := e.logRows(admin, "")
	if len(rows) != 1 || rows[0]["action"] != "setup" || rows[0]["actor_email"] != "setup" || rows[0]["actor_id"] != "" ||
		rows[0]["target_type"] != "user" || rows[0]["target_id"] != root["id"] || rows[0]["ip"] != "127.0.0.1" || details(rows[0])["email"] != "root@example.com" {
		t.Fatalf("setup row: %v", rows)
	}
	for _, k := range []string{"id", "actor_id", "actor_email", "action", "target_type", "target_id", "details", "ip", "created_at"} {
		if _, ok := rows[0][k]; !ok {
			t.Fatalf("row without %s: %v", k, rows[0])
		}
	}
	// The CLI logs as "cli".
	if err := e.acc.Log(context.Background(), accounts.NewLogEntry{ActorEmail: accounts.ActorCLI, Action: accounts.ActionCLIInvite, TargetType: accounts.TargetLink, TargetID: "bob@example.com"}); err != nil {
		t.Fatal(err)
	}
	e.json(e.as(admin, "POST", "/api/admin/users", `{"email":"ana@example.com"}`), 201, nil)

	page := e.adminList(admin, "/api/admin/log?per_page=2")
	if page.Total != 3 || len(page.Data) != 2 || page.Data[0]["action"] != "user.invite" || page.Data[1]["action"] != "cli.invite" || page.IsLastPage {
		t.Fatalf("page 1: %+v", page)
	}
	if page = e.adminList(admin, "/api/admin/log?per_page=2&page=2"); len(page.Data) != 1 || page.Data[0]["action"] != "setup" || !page.IsLastPage {
		t.Fatalf("page 2: %+v", page)
	}
	if rows = e.logRows(admin, "actor=cli"); len(rows) != 1 || rows[0]["action"] != "cli.invite" || rows[0]["ip"] != "" || details(rows[0]) == nil {
		t.Fatalf("actor=cli: %v", rows)
	}
	if rows = e.logRows(admin, "actor=ROOT@example.com"); len(rows) != 1 || rows[0]["action"] != "user.invite" {
		t.Fatalf("actor by email: %v", rows)
	}
	if rows = e.logRows(admin, "actor="+root["id"].(string)); len(rows) != 1 {
		t.Fatalf("actor by id: %v", rows)
	}
	if rows = e.logRows(admin, "action=nope"); len(rows) != 0 {
		t.Fatalf("unknown action: %v", rows)
	}
}
