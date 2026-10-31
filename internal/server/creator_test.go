package server

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// The abuse page shows who created an anonymous URL to anyone, with or
// without accounts, without extending the URL's life.
func TestAbuseCreator(t *testing.T) {
	plain := setup(t)
	id := plain.newToken(`{}`)["uuid"].(string)
	plain.mr.FastForward(10 * time.Minute)
	ttl := plain.mr.TTL(tokenKey(id))
	var got map[string]any
	plain.json(plain.do("GET", "/api/abuse/urls/"+strings.ToUpper(id), nil), 200, &got)
	if got["ip"] != "127.0.0.1" || got["user_agent"] == "" || got["created_at"] == nil {
		t.Fatalf("creator: %v", got)
	}
	if plain.mr.TTL(tokenKey(id)) != ttl {
		t.Fatal("the lookup must not extend the URL's life")
	}
	plain.json(plain.do("GET", "/api/abuse/urls/"+zeroUUID, nil), 404, nil)
	plain.json(plain.do("GET", "/api/abuse/urls/nope", nil), 404, nil)
	plain.json(plain.do("DELETE", "/token/"+id, nil), 204, nil)
	plain.json(plain.do("GET", "/api/abuse/urls/"+id, nil), 404, nil)

	e := setupAccounts(t)
	_, ana := e.invite("ana@example.com", "ana's password")
	owned := e.ownedToken(ana, `{}`)["uuid"].(string)
	e.json(e.do("GET", "/api/abuse/urls/"+owned, nil), 403, nil)
	anon := e.newToken(`{}`)["uuid"].(string)
	e.json(e.do("GET", "/api/abuse/urls/"+anon, nil), 200, &got)
	if got["ip"] != "127.0.0.1" {
		t.Fatalf("with accounts: %v", got)
	}
}

// Who created a URL (ip, user_agent) is for its owner and the admins only.
func TestCreatorHidden(t *testing.T) {
	plain := setup(t)
	created := plain.newToken(`{}`)
	id := created["uuid"].(string)
	var got map[string]any
	plain.json(plain.do("GET", "/token/"+id, nil), 200, &got)
	for _, tok := range []map[string]any{created, got} {
		if tok["ip"] != "" || tok["user_agent"] != "" {
			t.Fatalf("anyone with the link must not see the creator: %v", tok)
		}
	}

	e := setupAccounts(t)
	_, admin := e.firstAdmin("root@example.com", "correct horse battery")
	_, ana := e.invite("ana@example.com", "ana's password")
	_, bob := e.invite("bob@example.com", "bob's password")
	anon := e.newToken(`{}`)["uuid"].(string)
	owned := e.ownedToken(ana, `{}`)["uuid"].(string)
	for _, c := range []struct {
		name, id string
		who      string // "" = signed out
		sees     bool
	}{
		{"anonymous, signed out", anon, "", false},
		{"anonymous, a user", anon, "bob", false},
		{"anonymous, an admin", anon, "admin", true},
		{"private, its owner", owned, "ana", true},
		{"private, an admin", owned, "admin", true},
	} {
		cookie := map[string]*http.Cookie{"": nil, "bob": bob, "ana": ana, "admin": admin}[c.who]
		e.json(e.as(cookie, "GET", "/token/"+c.id, ""), 200, &got)
		if sees := got["ip"] == "127.0.0.1" && got["user_agent"] != ""; sees != c.sees {
			t.Fatalf("%s: ip %q user_agent %q", c.name, got["ip"], got["user_agent"])
		}
	}

	// An admin's change reaches every live viewer without the creator.
	sub, stop := e.hub.Subscribe(anon)
	defer stop()
	e.json(e.as(admin, "PUT", "/token/"+anon, `{"description":"checked"}`), 200, &got)
	if got["ip"] != "127.0.0.1" {
		t.Fatalf("the admin's own answer keeps it: %v", got["ip"])
	}
	select {
	case b := <-sub:
		if msg := string(b); !strings.Contains(msg, `"token.updated"`) || !strings.Contains(msg, `"ip":"","user_agent":""`) {
			t.Fatalf("event: %s", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no token.updated event")
	}
}
