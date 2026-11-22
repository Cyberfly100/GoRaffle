// Command ensuredb creates the browser tests' dedicated database on first use.
//
// It lives under testdata/ so `go build ./...`, `go test ./...` and the docker
// image never see it; browser-tests/serve.sh runs it explicitly. It refuses any
// database other than "raffle_browser_test", so the browser suite can never
// point at the app's live "raffle" database or the Go tests' databases.
package main

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"

	_ "github.com/lib/pq"
)

const dbName = "raffle_browser_test"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ensuredb:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: ensuredb <database url>")
	}
	u, err := url.Parse(os.Args[1])
	if err != nil {
		return fmt.Errorf("parse database url: %w", err)
	}
	if got := strings.TrimPrefix(u.Path, "/"); got != dbName {
		return fmt.Errorf("browser tests must use %q, not %q", dbName, got)
	}
	admin := *u
	admin.Path = "/postgres"
	conn, err := sql.Open("postgres", admin.String())
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.Ping(); err != nil {
		return fmt.Errorf("postgres not reachable (sudo docker compose up -d postgres): %w", err)
	}
	var exists bool
	if err := conn.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, dbName).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = conn.Exec("CREATE DATABASE " + dbName)
	return err
}
