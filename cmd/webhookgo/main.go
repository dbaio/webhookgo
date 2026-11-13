// Command webhooks runs the webhook inspector: it captures HTTP requests sent
// to unique URLs and shows them live in the browser.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/dbaio/webhookgo/internal/accounts"
	"github.com/dbaio/webhookgo/internal/config"
	"github.com/dbaio/webhookgo/internal/hub"
	"github.com/dbaio/webhookgo/internal/server"
	"github.com/dbaio/webhookgo/internal/store"
	"github.com/dbaio/webhookgo/web"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "healthcheck": // used by the Docker HEALTHCHECK (no shell in the image)
			os.Exit(healthcheck())
		case "audit":
			os.Exit(audit(os.Args[2:]))
		case "users":
			os.Exit(users(os.Args[2:]))
		case "version", "-v", "--version":
			fmt.Println(version)
			return
		}
	}
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	setupLogging(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("REDIS_URL: %w", err)
	}
	rdb := redis.NewClient(opts)
	defer rdb.Close()
	if err := waitForRedis(ctx, rdb); err != nil {
		return err
	}

	st := store.New(rdb, cfg.TokenTTL, cfg.MaxRequests, cfg.MaxStorage, cfg.Retention)
	h := hub.New(store.EventChannelPrefix)
	hubCtx, stopHub := context.WithCancel(context.Background())
	defer stopHub()
	go h.Run(hubCtx, st.Subscribe(hubCtx))

	var acc *accounts.Store
	if cfg.Accounts {
		if acc, err = accounts.Open(ctx, cfg.DatabaseURL, 30); err != nil {
			return err
		}
		defer acc.Close()
		if err := acc.Migrate(ctx); err != nil {
			return err
		}
		go deleteExpired(ctx, acc)
		if err := bootstrapAdmin(ctx, cfg, acc); err != nil {
			return err
		}
		go keepAnonymousIndex(ctx, st)
		if cfg.GoogleClientID != "" {
			join := "any Gmail or Workspace address"
			if len(cfg.GoogleDomains) > 0 {
				join = strings.Join(cfg.GoogleDomains, ", ")
			}
			base := cfg.PublicURL
			if base == "" {
				base = "<the app's address; set PUBLIC_URL>"
			}
			slog.Info("sign-in with Google is on", "join", join, "redirect_uri", base+"/api/auth/google/callback")
		}
	} else if cfg.GoogleClientID != "" {
		slog.Warn("GOOGLE_CLIENT_ID is set but ACCOUNTS is off: sign-in with Google stays off")
	}

	srv, err := server.New(cfg, st, acc, h, web.FS(), version)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      time.Duration(cfg.MaxDelay)*time.Second + time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
	httpServer.RegisterOnShutdown(srv.Close)

	errc := make(chan error, 1)
	go func() {
		slog.Info("listening",
			"addr", cfg.ListenAddr, "version", version, "redis", opts.Addr, "accounts", cfg.Accounts,
			"max_requests", cfg.MaxRequests, "ttl", cfg.TokenTTL.String(), "max_body", cfg.MaxBodySize)
		errc <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownWait)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func waitForRedis(ctx context.Context, rdb *redis.Client) error {
	for attempt := 1; ; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := rdb.Ping(pingCtx).Err()
		cancel()
		if err == nil {
			return nil
		}
		if attempt >= 30 {
			return fmt.Errorf("redis unreachable: %w", err)
		}
		slog.Warn("waiting for redis", "attempt", attempt, "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func setupLogging(cfg config.Config) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler = slog.NewTextHandler(os.Stdout, opts)
	if strings.EqualFold(cfg.LogFormat, "json") {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(handler))
}

func healthcheck() int {
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "status", resp.StatusCode)
		return 1
	}
	return 0
}
