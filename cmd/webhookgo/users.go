package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dbaio/webhookgo/internal/accounts"
	"github.com/dbaio/webhookgo/internal/config"
)

const (
	setupLinkTTL  = time.Hour
	inviteLinkTTL = 72 * time.Hour
)

const usersUsage = `usage: webhookgo users add [--admin] <email>   print an invite link
       webhookgo users reset <email>           print a password reset link
       webhookgo users list`

// users manages accounts from the command line, for the first admin and for
// instances without the panel:
//
//	docker compose exec app webhookgo users add --admin you@example.com
func users(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(os.Stderr, usersUsage)
		return 2
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !cfg.Accounts {
		fmt.Fprintln(os.Stderr, "accounts are off: set ACCOUNTS=on (and COMPOSE_PROFILES=accounts with Docker Compose)")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	acc, err := accounts.Open(ctx, cfg.DatabaseURL, 3)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer acc.Close()
	if err := acc.Migrate(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	switch args[0] {
	case "add":
		err = usersAdd(ctx, cfg, acc, args[1:])
	case "reset":
		err = usersReset(ctx, cfg, acc, args[1:])
	case "list":
		err = usersList(ctx, acc)
	default:
		fmt.Fprintln(os.Stderr, usersUsage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func usersAdd(ctx context.Context, cfg config.Config, acc *accounts.Store, args []string) error {
	fs := flag.NewFlagSet("users add", flag.ContinueOnError)
	admin := fs.Bool("admin", false, "invite as an administrator")
	if err := fs.Parse(args); err != nil {
		return err
	}
	email := strings.TrimSpace(fs.Arg(0))
	if email == "" || fs.NArg() != 1 {
		return errors.New(usersUsage)
	}
	if _, err := acc.UserByEmail(ctx, email); err == nil {
		return fmt.Errorf("%s is already registered", email)
	} else if !errors.Is(err, accounts.ErrNotFound) {
		return err
	}
	role := accounts.RoleUser
	if *admin {
		role = accounts.RoleAdmin
	}
	token, err := acc.CreateLink(ctx, accounts.NewLink{Kind: accounts.LinkInvite, Email: email, Role: role, TTL: inviteLinkTTL})
	if err != nil {
		return err
	}
	fmt.Printf("invite for %s (%s): %s (valid 72h)\n", email, role, linkText(cfg.PublicURL, "/invite", token))
	return nil
}

func usersReset(ctx context.Context, cfg config.Config, acc *accounts.Store, args []string) error {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return errors.New(usersUsage)
	}
	u, err := acc.UserByEmail(ctx, strings.TrimSpace(args[0]))
	if errors.Is(err, accounts.ErrNotFound) {
		return fmt.Errorf("no account with the email %s", args[0])
	}
	if err != nil {
		return err
	}
	token, err := acc.CreateLink(ctx, accounts.NewLink{Kind: accounts.LinkReset, UserID: u.ID, TTL: inviteLinkTTL})
	if err != nil {
		return err
	}
	fmt.Printf("password reset for %s: %s (valid 72h)\n", u.Email, linkText(cfg.PublicURL, "/reset", token))
	return nil
}

func usersList(ctx context.Context, acc *accounts.Store) error {
	users, err := acc.ListUsers(ctx)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "EMAIL\tNAME\tROLE\tSTATUS\tCREATED\tLAST LOGIN")
	for _, u := range users {
		status, last := "active", "never"
		if u.Disabled() {
			status = "disabled"
		}
		if u.LastLoginAt != nil {
			last = u.LastLoginAt.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", u.Email, u.Name, u.Role, status, u.CreatedAt.Format("2006-01-02 15:04"), last)
	}
	return w.Flush()
}

// linkText is how a one-time link is shown: a full URL when PUBLIC_URL is
// set, otherwise the path to open on the instance. The token travels in the
// fragment, which browsers never send to the server.
func linkText(publicURL, page, token string) string {
	if publicURL != "" {
		return publicURL + page + "#" + token
	}
	return "open " + page + "#" + token + " on this instance"
}

// bootstrapAdmin logs a one-time setup link while the instance has no
// active admin, replacing any setup link logged before.
func bootstrapAdmin(ctx context.Context, cfg config.Config, acc *accounts.Store) error {
	n, err := acc.ActiveAdmins(ctx)
	if err != nil || n > 0 {
		return err
	}
	if err := acc.DeleteUnusedSetupLinks(ctx); err != nil {
		return err
	}
	token, err := acc.CreateLink(ctx, accounts.NewLink{Kind: accounts.LinkSetup, TTL: setupLinkTTL})
	if err != nil {
		return err
	}
	slog.Warn("create the first admin: " + linkText(cfg.PublicURL, "/setup", token) + " (valid 1h)")
	return nil
}
