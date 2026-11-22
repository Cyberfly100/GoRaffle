#!/usr/bin/env bash
# Starts a freshly built raffle binary for the browser tests (Playwright's
# webServer runs this). Own port and own database, so it never tests a deployed
# instance (compose app on 8543, e2e.sh on 8599) nor touches the live "raffle" DB.
set -euo pipefail
cd "$(dirname "$0")/.."

: "${BROWSER_TEST_PORT:=8598}"
DATABASE_URL="postgresql://user:pass@localhost:5432/raffle_browser_test?sslmode=disable"

go run ./browser-tests/testdata/ensuredb "$DATABASE_URL"
go build -o browser-tests/.bin/raffle ./cmd/raffle

DATABASE_URL="$DATABASE_URL" LISTEN_ADDR=":$BROWSER_TEST_PORT" exec browser-tests/.bin/raffle
