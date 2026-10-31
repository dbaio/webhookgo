package server

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

const anonymousIndex = "wh:anonymous"

func uuids(p adminPage) []string {
	out := make([]string, len(p.Data))
	for i, d := range p.Data {
		out[i] = d["uuid"].(string)
	}
	return out
}

func TestAdminListsAnonymousURLs(t *testing.T) {
	e := setupAccounts(t)
	_, admin := e.firstAdmin("root@example.com", "correct horse battery")
	_, ana := e.invite("ana@example.com", "ana's password")
	owned := e.ownedToken(ana, `{"description":"Ana's"}`)["uuid"].(string)
	stripe := e.newToken(`{"description":"Stripe test"}`)
	time.Sleep(2 * time.Millisecond) // distinct creation times, newest first
	github := e.newToken(`{"description":"GitHub"}`)["uuid"].(string)
	time.Sleep(2 * time.Millisecond)
	claimed := e.newToken(`{}`)
	time.Sleep(2 * time.Millisecond)
	gone := e.newToken(`{}`)["uuid"].(string)
	e.do("POST", "/"+github, strings.NewReader("x"))

	const list = "/api/admin/urls?kind=anonymous"
	page := e.adminList(admin, list)
	if want := []string{gone, claimed["uuid"].(string), github, stripe["uuid"].(string)}; !slices.Equal(uuids(page), want) || page.Total != 4 {
		t.Fatalf("anonymous urls: %v, want %v (total %d)", uuids(page), want, page.Total)
	}
	item := page.Data[2]
	exp, _ := time.Parse(time.RFC3339, item["expires_at"].(string))
	if item["owner"] != nil || item["request_count"].(float64) != 1 || item["latest_request_at"] == nil || item["ip"] != "127.0.0.1" ||
		item["url"] != e.srv.URL+"/"+github || item["claim_key"] != nil || item["claim_hash"] != nil || time.Until(exp) <= 0 {
		t.Fatalf("item: %v", item)
	}
	if p := e.adminList(admin, "/api/admin/urls"); p.Total != 1 || p.Data[0]["uuid"] != owned {
		t.Fatalf("the default list stays the private one: %+v", p)
	}

	// Neither the list nor a lookup extends a URL's life, and both say when
	// it expires as it stands.
	e.mr.FastForward(30 * time.Minute) // of the hour TOKEN_TTL
	soon := time.Now().Add(31 * time.Minute)
	page = e.adminList(admin, list)
	var l struct {
		Token map[string]any `json:"token"`
	}
	e.json(e.as(admin, "GET", "/api/admin/urls/"+github, ""), 200, &l)
	for _, at := range []any{page.Data[2]["expires_at"], l.Token["expires_at"]} {
		if exp, err := time.Parse(time.RFC3339, at.(string)); err != nil || exp.After(soon) {
			t.Fatalf("expires_at %v, want before %v", at, soon)
		}
	}

	// A claim, a delete and an expiry take URLs off the list.
	e.json(e.as(ana, "POST", "/token/"+claimed["uuid"].(string)+"/claim", `{"claim_key":"`+claimed["claim_key"].(string)+`"}`), 200, nil)
	e.json(e.do("DELETE", "/token/"+stripe["uuid"].(string), nil), 204, nil)
	e.mr.Del(tokenKey(gone)) // expired or evicted: still indexed until read past
	if page = e.adminList(admin, list); !slices.Equal(uuids(page), []string{github}) || page.Total != 1 {
		t.Fatalf("after claim, delete and expiry: %v (total %d)", uuids(page), page.Total)
	}
	if e.mr.Exists(anonymousIndex) && slices.Contains(must(e.mr.ZMembers(anonymousIndex)), gone) {
		t.Fatal("a list must drop the entries it reads past")
	}

	// Releasing a private URL lists it again.
	e.json(e.as(ana, "POST", "/token/"+owned+"/release", ""), 200, nil)
	if page = e.adminList(admin, list); !slices.Equal(uuids(page), []string{github, owned}) {
		t.Fatalf("after release: %v", uuids(page))
	}

	// Search: a name, an exact ID or the creator's IP.
	for q, want := range map[string][]string{"github": {github}, strings.ToUpper(owned): {owned}, "127.0.0.1": {github, owned}, "nope": {}} {
		if page = e.adminList(admin, list+"&q="+url.QueryEscape(q)); !slices.Equal(uuids(page), want) || page.Total != len(want) {
			t.Fatalf("q=%s: %v (total %d)", q, uuids(page), page.Total)
		}
	}
	if page = e.adminList(admin, list+"&q=127.0.0.1&per_page=1&page=2"); !slices.Equal(uuids(page), []string{owned}) || page.Total != 2 {
		t.Fatalf("second page of a search: %v (total %d)", uuids(page), page.Total)
	}
	e.json(e.as(admin, "GET", list+"&owner="+zeroUUID, ""), 422, nil)
	e.json(e.as(admin, "GET", "/api/admin/urls?kind=all", ""), 422, nil)
	_, user := e.invite("bob@example.com", "bob's password")
	e.json(e.as(user, "GET", list, ""), 403, nil)

	// Opening one is logged, once an hour, for admins only.
	e.json(e.as(user, "GET", "/token/"+github, ""), 200, nil)
	for range 2 {
		e.json(e.as(admin, "GET", "/token/"+github, ""), 200, nil)
		e.json(e.as(admin, "GET", "/token/"+github+"/requests", ""), 200, nil)
	}
	rows := e.logRows(admin, "action=url.view")
	if len(rows) != 1 || rows[0]["target_id"] != github || details(rows[0])["anonymous"] != true || details(rows[0])["owner_id"] != nil {
		t.Fatalf("log: %v", rows)
	}
}

func TestPrivateURLSearchByIP(t *testing.T) {
	e := setupAccounts(t)
	_, admin := e.firstAdmin("root@example.com", "correct horse battery")
	id := e.ownedToken(admin, `{}`)["uuid"].(string)
	if p := e.adminList(admin, "/api/admin/urls?q=127.0.0.1"); !slices.Equal(uuids(p), []string{id}) {
		t.Fatalf("by IP: %v", uuids(p))
	}
	if p := e.adminList(admin, "/api/admin/urls?q=127.0.0"); p.Total != 0 {
		t.Fatalf("an IP must match exactly: %v", uuids(p))
	}
}

func TestAnonymousIndexHeals(t *testing.T) {
	ctx := context.Background()
	e := setupAccounts(t)
	st := e.server.store
	_, admin := e.firstAdmin("root@example.com", "correct horse battery")
	e.ownedToken(admin, `{}`) // never indexed
	ids := []string{e.newToken(`{}`)["uuid"].(string), e.newToken(`{}`)["uuid"].(string), e.newToken(`{}`)["uuid"].(string)}

	// URLs created while accounts were off are added at startup.
	e.mr.Del(anonymousIndex)
	if n, err := st.IndexAnonymousURLs(ctx); err != nil || n != 3 {
		t.Fatalf("indexed %d, %v", n, err)
	}
	if got := must(e.mr.ZMembers(anonymousIndex)); len(got) != 3 || !slices.Contains(got, ids[0]) {
		t.Fatalf("index: %v", got)
	}
	e.mr.ZRem(anonymousIndex, ids[0])
	if n, err := st.IndexAnonymousURLs(ctx); err != nil || n != 0 {
		t.Fatalf("another replica indexed just now: %d, %v", n, err)
	}

	// Expired URLs are pruned, by one replica at a time.
	e.mr.Del(tokenKey(ids[1]))
	if n, err := st.PruneAnonymous(ctx, time.Minute); err != nil || n != 1 {
		t.Fatalf("pruned %d, %v", n, err)
	}
	e.mr.Del(tokenKey(ids[2]))
	if n, err := st.PruneAnonymous(ctx, time.Minute); err != nil || n != 0 {
		t.Fatalf("pruned again at once: %d, %v", n, err)
	}
	e.mr.FastForward(time.Minute)
	if n, err := st.PruneAnonymous(ctx, time.Minute); err != nil || n != 1 {
		t.Fatalf("pruned after the lock: %d, %v", n, err)
	}
}

func TestAnonymousIndexNeedsAccounts(t *testing.T) {
	e := setup(t)
	e.newToken(`{}`)
	if e.mr.Exists(anonymousIndex) {
		t.Fatal("with accounts off, nothing is indexed")
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
