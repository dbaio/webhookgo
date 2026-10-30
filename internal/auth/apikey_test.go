package auth

import (
	"bytes"
	"crypto/sha256"
	"net/http"
	"strings"
	"testing"
)

func TestNewAPIKey(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		key, hash := NewAPIKey()
		if !strings.HasPrefix(key, "wgo_") || len(key) != 47 {
			t.Fatalf("key format: %q", key)
		}
		if strings.Trim(key[4:], "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
			t.Fatalf("not base62: %q", key)
		}
		if sum := sha256.Sum256([]byte(key)); !bytes.Equal(hash, sum[:]) {
			t.Fatal("the hash must be the SHA-256 of the whole key")
		}
		if seen[key] {
			t.Fatal("repeated key")
		}
		seen[key] = true
	}
}

func TestAPIKeyHeader(t *testing.T) {
	for _, tc := range []struct {
		name, value, want string
	}{
		{"Api-Key", "wgo_abc", "wgo_abc"},
		{"api-key", " wgo_abc ", "wgo_abc"},
		{"Authorization", "Bearer wgo_abc", "wgo_abc"},
		{"Authorization", "bearer wgo_abc", "wgo_abc"},
		{"Authorization", "Basic d2dvX2FiYw==", ""},
		{"Authorization", "Bearer", ""},
		{"X-Api-Key", "wgo_abc", ""},
	} {
		r, _ := http.NewRequest("GET", "/token", nil)
		r.Header.Set(tc.name, tc.value)
		if got := APIKey(r); got != tc.want {
			t.Errorf("%s: %q: got %q, want %q", tc.name, tc.value, got, tc.want)
		}
	}
	r, _ := http.NewRequest("GET", "/token", nil)
	r.Header.Set("Api-Key", "wgo_one")
	r.Header.Set("Authorization", "Bearer wgo_two")
	if got := APIKey(r); got != "wgo_one" {
		t.Errorf("Api-Key must win over Authorization: %q", got)
	}
	if got := APIKey(&http.Request{Header: http.Header{}}); got != "" {
		t.Errorf("no header: %q", got)
	}
}
