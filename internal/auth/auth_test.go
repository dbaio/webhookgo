package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPasswords(t *testing.T) {
	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") || len(strings.Split(hash, "$")) != 6 {
		t.Fatalf("PHC format: %s", hash)
	}
	if ok, err := VerifyPassword(hash, "correct horse battery"); err != nil || !ok {
		t.Fatalf("verify: %v %v", ok, err)
	}
	if ok, err := VerifyPassword(hash, "correct horse batter"); err != nil || ok {
		t.Fatalf("wrong password: %v %v", ok, err)
	}
	again, _ := HashPassword("correct horse battery")
	if again == hash {
		t.Fatal("salts must differ")
	}
	for _, bad := range []string{"", "plain", "$argon2i$v=19$m=1,t=1,p=1$a$b", "$argon2id$v=19$m=x$a$b", "$argon2id$v=19$m=1,t=1,p=1$!!$b"} {
		if ok, err := VerifyPassword(bad, "x"); err == nil || ok {
			t.Errorf("malformed hash %q: %v %v", bad, ok, err)
		}
	}
	if ok, err := VerifyPassword(dummyHash, "anything at all"); err != nil || ok {
		t.Fatalf("dummy hash: %v %v", ok, err)
	}
	VerifyDummy("x")

	for in, ok := range map[string]bool{"123456789": false, "1234567890": true, strings.Repeat("x", 256): true, strings.Repeat("x", 257): false} {
		if err := CheckPassword(in); (err == nil) != ok {
			t.Errorf("CheckPassword(%d bytes) = %v", len(in), err)
		}
	}
}

func TestSecrets(t *testing.T) {
	s1, h1 := NewSecret()
	s2, _ := NewSecret()
	if len(s1) != 43 || strings.ContainsAny(s1, "+/=") || s1 == s2 {
		t.Fatalf("secret: %q %q", s1, s2)
	}
	if len(h1) != 32 || string(h1) != string(HashSecret(s1)) || string(h1) == string(HashSecret(s2)) {
		t.Fatalf("hash: %x", h1)
	}
}

func TestCookies(t *testing.T) {
	c := SessionCookieFor("secret", false, 30*24*time.Hour)
	if c.Name != "wg_session" || c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.MaxAge != 30*24*3600 || c.Domain != "" {
		t.Fatalf("http cookie: %+v", c)
	}
	c = SessionCookieFor("secret", true, time.Hour)
	if c.Name != "__Host-wg_session" || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.MaxAge != 3600 || c.Domain != "" {
		t.Fatalf("https cookie: %+v", c)
	}

	r := httptest.NewRequest("GET", "/", nil)
	if SessionSecret(r, false) != "" || SessionSecret(r, true) != "" {
		t.Fatal("no cookie")
	}
	r.AddCookie(&http.Cookie{Name: "wg_session", Value: "plain"})
	if SessionSecret(r, false) != "plain" {
		t.Fatal("plain cookie")
	}
	if SessionSecret(r, true) != "" {
		t.Fatal("over https only the __Host- cookie counts")
	}
	r.AddCookie(&http.Cookie{Name: "__Host-wg_session", Value: "host"})
	if SessionSecret(r, false) != "host" || SessionSecret(r, true) != "host" {
		t.Fatal("the __Host- cookie wins")
	}

	w := httptest.NewRecorder()
	ClearSessionCookies(w)
	cleared := w.Result().Cookies()
	if len(cleared) != 2 || cleared[0].Name != "__Host-wg_session" || !cleared[0].Secure || cleared[0].MaxAge >= 0 ||
		cleared[1].Name != "wg_session" || cleared[1].Secure || cleared[1].MaxAge >= 0 {
		t.Fatalf("clear: %+v", cleared)
	}
}
