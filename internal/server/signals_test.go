package server

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dbaio/webhookgo/internal/config"
)

func TestAbuseSignals(t *testing.T) {
	e := setupAccounts(t, func(c *config.Config) { c.ProxyHops = 1 })
	_, admin := e.firstAdmin("root@example.com", "correct horse battery")
	from := func(ip, body string) string {
		var tok map[string]any
		e.json(e.do("POST", "/token", strings.NewReader(body), "Content-Type", "application/json", "X-Forwarded-For", ip), 201, &tok)
		time.Sleep(2 * time.Millisecond) // distinct creation times
		return tok["uuid"].(string)
	}
	plain := from("203.0.113.1", `{"description":"plain"}`)
	phish := from("203.0.113.1", `{"description":"Bank login","default_content":"<form>password</form>","default_content_type":"text/html"}`)
	busy := from("203.0.113.1", `{}`)
	empty := from("198.51.100.7", `{"default_content_type":"text/html"}`) // HTML with nothing in it: no page
	svg := from("198.51.100.7", `{"default_content":"<svg/>","default_content_type":"image/svg+xml"}`)
	for range 3 {
		e.do("POST", "/"+busy, nil)
	}
	e.do("POST", "/"+phish, nil)
	_ = plain

	const list = "/api/admin/urls?kind=anonymous"
	page := e.adminList(admin, list)
	flagged := map[string]bool{}
	for _, d := range page.Data {
		flagged[d["uuid"].(string)] = d["serves_page"].(bool)
	}
	if !flagged[phish] || !flagged[svg] || flagged[empty] || flagged[plain] || flagged[busy] {
		t.Fatalf("serves_page: %v", flagged)
	}

	if p := e.adminList(admin, list+"&pages=1"); !slices.Equal(uuids(p), []string{svg, phish}) || p.Total != 2 {
		t.Fatalf("pages only: %v (total %d)", uuids(p), p.Total)
	}
	if p := e.adminList(admin, list+"&pages=1&q=bank"); !slices.Equal(uuids(p), []string{phish}) {
		t.Fatalf("pages only, searched: %v", uuids(p))
	}
	p := e.adminList(admin, list+"&sort=requests")
	if got := uuids(p); len(got) != 5 || got[0] != busy || got[1] != phish || p.Data[0]["request_count"].(float64) != 3 {
		t.Fatalf("by requests: %v", got)
	}
	if got := uuids(p)[2:]; !slices.Equal(got, []string{svg, empty, plain}) {
		t.Fatalf("ties keep the newest first: %v", got)
	}
	if p := e.adminList(admin, list+"&sort=requests&per_page=2&page=2"); !slices.Equal(uuids(p), []string{svg, empty}) || p.Total != 5 {
		t.Fatalf("second page by requests: %v (total %d)", uuids(p), p.Total)
	}
	e.json(e.as(admin, "GET", list+"&sort=oldest", ""), 422, nil)

	// The addresses that created the most.
	var ips struct {
		Data []creatorStats `json:"data"`
		URLs int            `json:"urls"`
	}
	e.json(e.as(admin, "GET", "/api/admin/ips", ""), 200, &ips)
	if ips.URLs != 5 || len(ips.Data) != 2 {
		t.Fatalf("ips: %+v", ips)
	}
	if c := ips.Data[0]; c.IP != "203.0.113.1" || c.URLs != 3 || c.Pages != 1 || c.Requests != 4 || c.LatestAt.IsZero() {
		t.Fatalf("top address: %+v", c)
	}
	if c := ips.Data[1]; c.IP != "198.51.100.7" || c.URLs != 2 || c.Pages != 1 || c.Requests != 0 {
		t.Fatalf("second address: %+v", c)
	}
	_, user := e.invite("ana@example.com", "ana's password")
	e.json(e.as(user, "GET", "/api/admin/ips", ""), 403, nil)
	off := setup(t)
	off.json(off.do("GET", "/api/admin/ips", nil), 404, nil)

	// Private URLs carry the flag too.
	e.ownedToken(admin, `{"default_content":"<html>","default_content_type":"text/html; charset=utf-8"}`)
	if p := e.adminList(admin, "/api/admin/urls"); p.Data[0]["serves_page"] != true {
		t.Fatalf("private: %v", p.Data[0])
	}
}
