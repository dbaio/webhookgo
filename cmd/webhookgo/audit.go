package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/dbaio/webhookgo/internal/config"
	"github.com/dbaio/webhookgo/internal/store"
)

var uuidPattern = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// audit prints the records kept about every URL found in its arguments, or in
// standard input when there are none (an abuse report pasted as is works):
//
//	docker compose exec app webhookgo audit https://hooks.example.com/<uuid>
func audit(args []string) int {
	text := strings.Join(args, "\n")
	if len(args) == 0 {
		if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice == 0 {
			b, err := io.ReadAll(os.Stdin)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			text = string(b)
		}
	}
	var ids []string
	for _, id := range uuidPattern.FindAllString(text, -1) {
		if id = strings.ToLower(id); !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		fmt.Fprintln(os.Stderr, "usage: webhookgo audit <URL or ID>... (or pipe in any text holding them)")
		return 2
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "REDIS_URL:", err)
		return 1
	}
	rdb := redis.NewClient(opts)
	defer rdb.Close()
	st := store.New(rdb, cfg.TokenTTL, cfg.MaxRequests, cfg.MaxStorage, cfg.Retention)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	for _, id := range ids {
		rec, err := st.GetRecord(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			rec = &store.Record{ID: id, Status: "unknown"} // never existed, or its record expired
		} else if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		_ = enc.Encode(rec)
	}
	return 0
}
