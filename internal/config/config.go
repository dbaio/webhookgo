// Package config loads runtime settings from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddr string
	RedisURL   string
	PublicURL  string
	AppName    string

	MaxRequests  int           // requests kept per URL; the oldest are evicted
	MaxStorage   int64         // bytes kept per URL; the oldest requests are evicted (0 disables)
	TokenTTL     time.Duration // idle lifetime of a URL and its requests
	MaxBodySize  int64         // bytes of each request body that are stored
	MaxDelay     int           // maximum response delay a URL may configure, in seconds
	RateLimit    int           // captured requests per minute per IP (0 disables)
	TokenLimit   int           // new URLs per minute per IP (0 disables)
	APILimit     int           // API calls per minute per IP (0 disables)
	ProxyHops    int           // trusted reverse proxies in front (0 = ignore X-Forwarded-*)
	Retention    time.Duration // how long a URL's audit record outlives it (0 disables)
	AbuseContact string        // email or URL shown on the abuse page
	LogLevel     string
	LogFormat    string
	ShutdownWait time.Duration

	Accounts    bool          // user accounts, backed by Postgres
	DatabaseURL string        // Postgres connection string; empty = the PG* variables
	SessionTTL  time.Duration // idle lifetime of a session
	LoginLimit  int           // login attempts per minute per IP (0 disables)
}

func Load() (Config, error) {
	c := Config{
		ListenAddr:   env("LISTEN_ADDR", ":8080"),
		RedisURL:     env("REDIS_URL", "redis://localhost:6379/0"),
		PublicURL:    strings.TrimRight(env("PUBLIC_URL", ""), "/"),
		AppName:      env("APP_NAME", "webhookgo"),
		AbuseContact: env("ABUSE_CONTACT", ""),
		LogLevel:     env("LOG_LEVEL", "info"),
		LogFormat:    env("LOG_FORMAT", "text"),
		ShutdownWait: 10 * time.Second,
		DatabaseURL:  env("DATABASE_URL", ""),
	}

	var err error
	if c.Accounts, err = envBool("ACCOUNTS", false); err != nil {
		return c, err
	}
	if c.MaxRequests, err = envInt("MAX_REQUESTS", 500); err != nil {
		return c, err
	}
	if c.LoginLimit, err = envInt("LOGIN_RATE_LIMIT", 10); err != nil {
		return c, err
	}
	if c.SessionTTL, err = envDuration("SESSION_TTL", 30*24*time.Hour); err != nil {
		return c, err
	}
	if c.MaxDelay, err = envInt("MAX_DELAY", 30); err != nil {
		return c, err
	}
	if c.RateLimit, err = envInt("RATE_LIMIT", 600); err != nil {
		return c, err
	}
	if c.TokenLimit, err = envInt("TOKEN_RATE_LIMIT", 30); err != nil {
		return c, err
	}
	if c.APILimit, err = envInt("API_RATE_LIMIT", 1200); err != nil {
		return c, err
	}
	if c.TokenTTL, err = envDuration("TOKEN_TTL", 7*24*time.Hour); err != nil {
		return c, err
	}
	if c.Retention, err = envDuration("AUDIT_RETENTION", 180*24*time.Hour); err != nil {
		return c, err
	}
	if c.MaxBodySize, err = envSize("MAX_BODY_SIZE", 2<<20); err != nil {
		return c, err
	}
	if c.MaxStorage, err = envSize("MAX_URL_STORAGE", 64<<20); err != nil {
		return c, err
	}
	if c.ProxyHops, err = envHops("TRUST_PROXY"); err != nil {
		return c, err
	}

	switch {
	case c.MaxRequests < 1:
		return c, fmt.Errorf("MAX_REQUESTS must be at least 1")
	case c.TokenTTL < time.Minute:
		return c, fmt.Errorf("TOKEN_TTL must be at least 1m")
	case c.MaxBodySize < 1:
		return c, fmt.Errorf("MAX_BODY_SIZE must be positive")
	case c.MaxDelay < 0:
		return c, fmt.Errorf("MAX_DELAY must not be negative")
	case c.MaxStorage < 0:
		return c, fmt.Errorf("MAX_URL_STORAGE must not be negative")
	case c.Retention < 0:
		return c, fmt.Errorf("AUDIT_RETENTION must not be negative")
	case c.SessionTTL < time.Minute:
		return c, fmt.Errorf("SESSION_TTL must be at least 1m")
	case c.LoginLimit < 0:
		return c, fmt.Errorf("LOGIN_RATE_LIMIT must not be negative")
	}
	return c, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

// envBool accepts on/off as well as true/false and 1/0.
func envBool(key string, def bool) (bool, error) {
	v := strings.ToLower(env(key, ""))
	switch v {
	case "":
		return def, nil
	case "on":
		return true, nil
	case "off":
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: %q is not on/off", key, v)
	}
	return b, nil
}

func envInt(key string, def int) (int, error) {
	v := env(key, "")
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not an integer", key, v)
	}
	return n, nil
}

// envHops reads how many reverse proxies to trust: false/true or a count
// (e.g. 2 for a CDN in front of nginx).
func envHops(key string) (int, error) {
	v := env(key, "false")
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return n, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a boolean or a number of proxies", key, v)
	}
	if b {
		return 1, nil
	}
	return 0, nil
}

// envDuration accepts Go durations ("72h") plus a day suffix ("7d").
func envDuration(key string, def time.Duration) (time.Duration, error) {
	v := env(key, "")
	if v == "" {
		return def, nil
	}
	if days, ok := strings.CutSuffix(v, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil {
			return 0, fmt.Errorf("%s: %q is not a duration", key, v)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a duration", key, v)
	}
	return d, nil
}

// envSize accepts plain byte counts or KB/MB/GB suffixes (binary multiples).
func envSize(key string, def int64) (int64, error) {
	v := strings.ToUpper(env(key, ""))
	if v == "" {
		return def, nil
	}
	mult := int64(1)
	for _, u := range []struct {
		suffix string
		mult   int64
	}{{"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}, {"G", 1 << 30}, {"M", 1 << 20}, {"K", 1 << 10}, {"B", 1}} {
		if s, ok := strings.CutSuffix(v, u.suffix); ok {
			v, mult = strings.TrimSpace(s), u.mult
			break
		}
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a size", key, v)
	}
	return n * mult, nil
}
