# AGENTS.md

Go raffle web app: Postgres-backed, server-rendered templates + vanilla JS,
static assets embedded in the binary (changes to `static/` or `templates/`
require a rebuild — including the docker image — to be visible).

## Commands

- Build: `go build ./...`
- Test: `go test ./...`
- Lint: `go vet ./... && gofmt -l .` (must report nothing; fix with `gofmt -w`)
- JS syntax check: `node --check static/js/app.js static/js/ws.js`
- E2E smoke test: `bash scripts/e2e.sh`

## E2E notes

- Requires the compose Postgres reachable at `localhost:5432` (`sudo docker compose up -d postgres` suffices).
- The script builds its own binary and serves it on port **8599** so it never
  tests a deployed instance (e.g. the compose app on 8543).
- It resets/reseeds DB state and restores the list name it found; don't run it
  against a database whose contents you care about.
