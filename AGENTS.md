# AGENTS.md

Go raffle web app: Postgres-backed, server-rendered templates + vanilla JS,
static assets embedded in the binary (changes to `static/` or `templates/`
require a rebuild — including the docker image — to be visible).

## Commands

- Build: `go build ./...`
- Test: `go test ./...`
- Lint: `go vet ./... && gofmt -l .` (must report nothing; fix with `gofmt -w`)
- JS syntax check: `for f in static/js/*.js; do node --check "$f"; done` (native ES modules, no build step)
- JS unit tests (pure modules, no dependencies): `node --test 'js-tests/*.test.js'`
- E2E smoke test: `bash scripts/e2e.sh`
- Browser tests: `npm install && npx playwright install chromium` once, then `npx playwright test`
  (one file: `npx playwright test browser-tests/import.spec.js`; one test: `-g "<title>"`)

## Browser code

- `static/js/` is native ES modules loaded by `<script type="module" src="/static/js/app.js">`;
  no bundler. `app.js` is page wiring; `reel.js` is the Reel (its interface is
  documented at the top of the file); `reel-plan.js` is its pure landing
  planner; `sound.js`, `confetti.js`, `ws.js` are what they say.
- Page regions refresh through `regions.js` (interface at the top of the file).
  Each region is declared once in `app.js` (`regions.define(...)`) with what it
  keeps across a refresh; the server's `changed` event names stale regions
  (Go constants `Region*` in `internal/handler/helpers.go`, same names).
- `collapsible.js` is the one show/hide toggle (History, Entries); its state is
  the class `no-<name>` on `<html>`, set before first paint by an inline script
  in `layout.html`.
- Only `reel.js` reads or writes Reel state, Reel classes or Reel CSS variables.
  Ask `reel.busy()` instead of checking phases; react to `reel.onIdle`.
- Pure modules get `node --test` tests in `js-tests/` (outside `static/`, so
  they are not embedded in the binary).

## Browser test notes

- Playwright drives headless Chromium against a freshly built binary
  (`browser-tests/serve.sh`, started by `playwright.config.js`) on port **8598**,
  using its own database `raffle_browser_test`, created on first use. It never
  reuses a server that's already running.
- Requires the compose Postgres at `localhost:5432`, like the E2E test.
- Import `test`/`expect` from `browser-tests/fixtures.js`, not `@playwright/test`.
  The fixture resets the app over HTTP before each test and fails a test on any
  uncaught page error.
- Set up state through the HTTP API (`seedEntries`, `setListName`, `resetApp` in
  fixtures.js), never SQL. Host actions and reads live in `browser-tests/helpers.js`.
- Assert what the host sees (toasts, field values, links, Reel cells). Never call
  app functions or read private state.
- Tests run one at a time (`workers: 1`), because they share a database.
- The tooling (`package.json`, `node_modules/`, `browser-tests/`) stays out of the
  Go build and the docker image.

## E2E notes

- Requires the compose Postgres reachable at `localhost:5432` (`sudo docker compose up -d postgres` suffices).
- The script builds its own binary and serves it on port **8599** so it never
  tests a deployed instance (e.g. the compose app on 8543).
- It resets/reseeds DB state and restores the list name it found; don't run it
  against a database whose contents you care about.

## Agent skills

### Issue tracker

Issues and specs live in GitHub Issues for Cyberfly100/GoRaffle (via `gh`). See `docs/agents/issue-tracker.md`.

### Triage labels

Default vocabulary: needs-triage, needs-info, ready-for-agent, ready-for-human, wontfix. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `GLOSSARY.md` + `docs/adr/` at the repo root (created lazily). See `docs/agents/domain.md`.
