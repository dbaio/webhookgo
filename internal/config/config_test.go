package config

import (
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != ":8080" || c.MaxRequests != 500 || c.TokenTTL != 7*24*time.Hour || c.MaxBodySize != 2<<20 || c.ProxyHops != 0 || c.MaxStorage != 64<<20 || c.Retention != 180*24*time.Hour {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if c.Accounts || c.DatabaseURL != "" || c.SessionTTL != 30*24*time.Hour || c.LoginLimit != 10 {
		t.Fatalf("unexpected account defaults: %+v", c)
	}
}

func TestAccountsSwitch(t *testing.T) {
	for in, want := range map[string]bool{"on": true, "off": false, "true": true, "false": false, "1": true, "0": false, "ON": true} {
		t.Setenv("ACCOUNTS", in)
		c, err := Load()
		if err != nil || c.Accounts != want {
			t.Errorf("ACCOUNTS=%s: %v, %v; want %v", in, c.Accounts, err, want)
		}
	}
	t.Setenv("ACCOUNTS", "on")
	t.Setenv("DATABASE_URL", "postgres://db/webhookgo")
	t.Setenv("SESSION_TTL", "12h")
	t.Setenv("LOGIN_RATE_LIMIT", "0")
	c, err := Load()
	if err != nil || c.DatabaseURL != "postgres://db/webhookgo" || c.SessionTTL != 12*time.Hour || c.LoginLimit != 0 {
		t.Fatalf("parsed: %+v, %v", c, err)
	}
}

func TestParsing(t *testing.T) {
	t.Setenv("TOKEN_TTL", "30d")
	t.Setenv("MAX_BODY_SIZE", "512KB")
	t.Setenv("MAX_REQUESTS", "100")
	t.Setenv("TRUST_PROXY", "true")
	t.Setenv("PUBLIC_URL", "https://hooks.example.com/")
	t.Setenv("AUDIT_RETENTION", "0")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.TokenTTL != 30*24*time.Hour || c.MaxBodySize != 512<<10 || c.MaxRequests != 100 || c.ProxyHops != 1 || c.Retention != 0 {
		t.Fatalf("parsed: %+v", c)
	}
	if c.PublicURL != "https://hooks.example.com" {
		t.Fatalf("trailing slash must be trimmed: %q", c.PublicURL)
	}

	for in, want := range map[string]int64{"1048576": 1 << 20, "10mb": 10 << 20, "1G": 1 << 30, "64 KB": 64 << 10, "300B": 300} {
		got, err := envSizeOf(t, in)
		if err != nil || got != want {
			t.Errorf("size %q = %d, %v; want %d", in, got, err, want)
		}
	}
}

func TestProxyHops(t *testing.T) {
	for in, want := range map[string]int{"false": 0, "0": 0, "true": 1, "1": 1, "2": 2} {
		t.Setenv("TRUST_PROXY", in)
		c, err := Load()
		if err != nil || c.ProxyHops != want {
			t.Errorf("TRUST_PROXY=%s: hops %d, %v; want %d", in, c.ProxyHops, err, want)
		}
	}
}

func envSizeOf(t *testing.T, v string) (int64, error) {
	t.Setenv("X_SIZE", v)
	return envSize("X_SIZE", 0)
}

func TestInvalid(t *testing.T) {
	for key, value := range map[string]string{
		"MAX_REQUESTS":     "0",
		"TOKEN_TTL":        "soon",
		"MAX_BODY_SIZE":    "big",
		"TRUST_PROXY":      "perhaps",
		"MAX_DELAY":        "-1",
		"AUDIT_RETENTION":  "-1h",
		"ACCOUNTS":         "maybe",
		"SESSION_TTL":      "10s",
		"LOGIN_RATE_LIMIT": "-1",
	} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, value)
			if _, err := Load(); err == nil {
				t.Fatalf("%s=%q must be rejected", key, value)
			}
		})
	}
}
