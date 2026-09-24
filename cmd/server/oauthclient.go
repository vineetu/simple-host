package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	dbpkg "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/handler"
)

// repeated collects a flag given more than once.
type repeated []string

func (r *repeated) String() string     { return strings.Join(*r, ",") }
func (r *repeated) Set(v string) error { *r = append(*r, v); return nil }

const oauthClientUsage = `usage: simple-host oauth-client <command>

  create --name NAME [--redirect-uri URI]... [--no-pkce]
        Register a confidential client (e.g. a ChatGPT GPT Action). Prints the
        client ID and secret ONCE; only a hash of the secret is stored.
        --no-pkce lets this one client omit PKCE (GPT Actions do not send it).
  set-redirects CLIENT_ID URI...
        Replace the client's redirect URIs (a GPT's callback URL is known only
        after its OAuth settings are first saved).
  list  List hand-registered clients.
  delete CLIENT_ID
        Delete a client and disconnect everyone who connected through it.

Reads DB_DSN from the environment (e.g. set -a; . /etc/simple-host.env).`

func runOAuthClientCommand(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, oauthClientUsage)
		return 2
	}
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "DB_DSN is not set")
		return 2
	}
	database, err := sql.Open("postgres", dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open postgres:", err)
		return 1
	}
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := dbpkg.VerifySchema(ctx, database); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("create", flag.ContinueOnError)
		name := fs.String("name", "", "name shown on the consent screen")
		noPKCE := fs.Bool("no-pkce", false, "allow this client to omit PKCE")
		var uris repeated
		fs.Var(&uris, "redirect-uri", "allowed redirect URI (repeatable)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if strings.TrimSpace(*name) == "" {
			fmt.Fprintln(os.Stderr, "--name is required")
			return 2
		}
		id, secret, err := handler.CreateOperatorClient(ctx, database, *name, uris, !*noPKCE)
		if err != nil {
			fmt.Fprintln(os.Stderr, "create:", err)
			return 1
		}
		fmt.Printf("client_id:     %s\nclient_secret: %s\n", id, secret)
		fmt.Println("The secret is shown only now; store it in the app's settings.")
		if len(uris) == 0 {
			fmt.Printf("No redirect URI yet: add the app's callback URL with\n  simple-host oauth-client set-redirects %s <url>...\n", id)
		}
		return 0
	case "set-redirects":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, oauthClientUsage)
			return 2
		}
		if err := handler.SetOperatorClientRedirects(ctx, database, args[1], args[2:]); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				fmt.Fprintln(os.Stderr, "no hand-registered client with that id")
			} else {
				fmt.Fprintln(os.Stderr, "set-redirects:", err)
			}
			return 1
		}
		fmt.Println("redirect URIs updated")
		return 0
	case "list":
		clients, err := dbpkg.ListOperatorOAuthClients(ctx, database)
		if err != nil {
			fmt.Fprintln(os.Stderr, "list:", err)
			return 1
		}
		for _, c := range clients {
			fmt.Printf("%s  %q  pkce_required=%t  created=%s\n", c.ClientID, c.Name, c.PKCERequired, c.CreatedAt.Format(time.RFC3339))
			for _, u := range c.RedirectURIs {
				fmt.Printf("    %s\n", u)
			}
		}
		return 0
	case "delete":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, oauthClientUsage)
			return 2
		}
		ok, err := dbpkg.DeleteOAuthClient(ctx, database, args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "delete:", err)
			return 1
		}
		if !ok {
			fmt.Fprintln(os.Stderr, "no client with that id")
			return 1
		}
		fmt.Println("deleted")
		return 0
	}
	fmt.Fprintln(os.Stderr, oauthClientUsage)
	return 2
}
