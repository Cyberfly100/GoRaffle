# Raffle Rewrite Plan

## Python tkinter → Go + HTMX + PostgreSQL

---

## Current State

A **521-line single-file Python/tkinter desktop GUI** (Windows-only) implementing a **weighted fair raffle/pick-from-a-hat** system. Contestants with the fewest previous picks are always preferred, ensuring everyone is drawn before anyone repeats. Persistence is a flat JSON file (`raffle_memory.txt`).

**Core algorithm**: On each draw, find the minimum pick-count among non-excluded contestants, then randomly select from those tied at the minimum. Increment the winner's count. This guarantees fair distribution.

### Existing Issues to Address

- Windows-only (`ctypes.windll`)
- In-memory state with JSON file persistence (no concurrency safety)
- No web interface
- No database, no migrations
- Dead code (`reset_score()`, `create_suspense_with_dots()`, `remove_empty_lines()`, `exclude_list`)
- GUI-state race condition (closing config popup mid-animation can clobber scores)

---

## Architecture Decisions

| Concern | Choice |
|---|---|
| Language | Go |
| Frontend | Go `html/template` + HTMX |
| Real-time | WebSocket (broadcast picks live) |
| Architecture | Single binary (serves API + templates + static) |
| Database | PostgreSQL (external to Docker for persistence) |
| Migrations | goose (SQL files) |
| Port | **8543** |
| Auth | None (local network) |
| HTTPS | No — local network only, no TLS needed |

---

## Database Schema

```sql
-- migrations/001_create_contestants.sql
CREATE TABLE contestants (
    id         SERIAL PRIMARY KEY,
    name       VARCHAR(255) NOT NULL UNIQUE,
    pick_count INTEGER NOT NULL DEFAULT 0,
    excluded   BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- migrations/002_create_picks.sql
CREATE TABLE picks (
    id             SERIAL PRIMARY KEY,
    contestant_id  INTEGER NOT NULL REFERENCES contestants(id),
    picked_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    pick_number    INTEGER NOT NULL
);

CREATE INDEX idx_contestants_pick_count ON contestants(pick_count) WHERE NOT excluded;

-- migrations/003_seed_defaults.sql
-- Insert 12 default contestants (contestant 1..12) only if table is empty
INSERT INTO contestants (name, pick_count)
SELECT 'contestant ' || i, 0
FROM generate_series(1, 12) AS i
WHERE NOT EXISTS (SELECT 1 FROM contestants LIMIT 1);
```

---

## API Routes

| Method | Path | Description |
|---|---|---|
| `GET /` | Main raffle page |
| `GET /api/contestants` | JSON list of all contestants |
| `POST /api/contestants` | Add contestant `{"name": "..."}` |
| `DELETE /api/contestants/:id` | Remove contestant |
| `PUT /api/contestants/:id` | Update name/count/excluded |
| `POST /api/draw` | Pick winner (+ broadcast via WS) |
| `POST /api/undo` | Undo last pick (+ broadcast via WS) |
| `POST /api/reset` | Reset all scores |
| `GET /api/history` | Full pick history |
| `GET /api/export` | Export contestants as JSON |
| `POST /api/import` | Import contestants from JSON/CSV |
| `GET /ws` | WebSocket upgrade |

---

## Project Structure

```
raffle/
├── cmd/
│   └── raffle/
│       └── main.go
├── internal/
│   ├── db/
│   │   ├── migrate.go
│   │   └── queries.go
│   ├── model/
│   │   └── model.go
│   ├── handler/
│   │   ├── pages.go
│   │   ├── api.go
│   │   └── ws.go
│   ├── raffle/
│   │   └── engine.go
│   └── ws/
│       └── hub.go
├── migrations/
│   ├── 001_create_contestants.sql
│   ├── 002_create_picks.sql
│   └── 003_seed_defaults.sql
├── templates/
│   ├── layout.html
│   ├── index.html
│   └── components/
│       ├── contestant_table.html
│       ├── draw_result.html
│       ├── history.html
│       └── import_export.html
├── static/
│   ├── css/
│   │   └── style.css
│   └── js/
│       └── ws.js
├── Dockerfile
├── docker-compose.yml
├── go.mod
├── go.sum
└── PLAN.md
```

---

## Key Improvements Over Original

| Area | Current | Proposed |
|---|---|---|
| **Pick logic** | In-memory filter, `secrets.choice` | SQL `ORDER BY pick_count ASC` + Go `math/rand/v2` |
| **Undo** | Pops from history list | Transactional (atomic decrement + delete) |
| **Reset** | Dead code in Python | Wired-up endpoint: truncate picks, zero counts |
| **Concurrency** | None (tkinter single-threaded) | All DB ops in transactions; WS hub goroutine-safe |
| **Exclusion** | Toggle per-name (transient) | Persisted to DB |
| **Edge case** | All excluded → crash | Return clear error: "No eligible contestants" |
| **Name normalization** | `.lower()` in Python | PostgreSQL `citext` extension for case-insensitive unique names |
| **Export/Import** | None | JSON + CSV support |
| **History** | In-memory list | Full persisted pick history displayed in UI |
| **Real-time** | None | WebSocket broadcast on every draw/undo |

---

## Implementation Order

### Phase 1: Go Backend — Core + Database
1. `go mod init`, goose dependency, `lib/pq` driver
2. Write migration SQL files (`001`, `002`, `003`)
3. `internal/model/model.go` — Go structs
4. `internal/db/queries.go` — all SQL operations (transactional)
5. `internal/db/migrate.go` — goose embed + run on startup
6. `internal/raffle/engine.go` — core pick algorithm with tests

### Phase 2: WebSocket Hub
7. `internal/ws/hub.go` — goroutine-safe connection manager + broadcast

### Phase 3: HTTP Handlers
8. `internal/handler/api.go` — REST endpoints (JSON)
9. `internal/handler/pages.go` — template rendering
10. `internal/handler/ws.go` — WebSocket upgrade handler
11. `cmd/raffle/main.go` — wire everything together, start server

### Phase 4: Frontend
12. `templates/layout.html` — base HTML with HTMX CDN + CSS
13. `templates/index.html` — main raffle page
14. `templates/components/contestant_table.html` — htmx-swapable table
15. `templates/components/draw_result.html` — winner announcement
16. `templates/components/history.html` — full pick history
17. `templates/components/import_export.html` — import/export UI
18. `static/css/style.css` — styling
19. `static/js/ws.js` — minimal WebSocket client (~20 lines)

### Phase 5: Export/Import
20. JSON export/import endpoints + handlers
21. CSV export/import endpoints + handlers

### Phase 6: Docker
22. `Dockerfile` — multi-stage build (golang:1.22-alpine → alpine:3.19)
23. `docker-compose.yml` — raffle service + external PG config

### Phase 7: Verification
24. `go vet ./...`
25. `go test ./...`
26. Docker image build
27. End-to-end manual test

---

## Docker Configuration

### Dockerfile (multi-stage)

```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o raffle ./cmd/raffle

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/raffle /usr/local/bin/raffle
EXPOSE 8543
CMD ["raffle"]
```

### docker-compose.yml

```yaml
version: "3.8"
services:
  raffle:
    build: .
    ports:
      - "8543:8543"
    environment:
      - DATABASE_URL=postgresql://user:pass@pg-host:5432/raffle?sslmode=disable
      - LISTEN_ADDR=:8543
    restart: unless-stopped

  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: raffle
      POSTGRES_USER: user
      POSTGRES_PASSWORD: pass
    volumes:
      - pgdata:/var/lib/postgresql/data
    ports:
      - "5432:5432"

volumes:
  pgdata:
```

**Note**: For production with external PG, set `DATABASE_URL` to the external instance and remove the `postgres` service.

---

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `DATABASE_URL` | — | PostgreSQL connection string (required) |
| `LISTEN_ADDR` | `:8543` | HTTP listen address |
| `LOG_LEVEL` | `info` | Logging verbosity |

---

## Dependencies

| Package | Purpose |
|---|---|
| `github.com/lib/pq` | PostgreSQL driver |
| `github.com/pressly/goose/v3` | Database migrations |
| `github.com/gorilla/websocket` | WebSocket support |
| Standard library | `net/http`, `html/template`, `encoding/json`, `encoding/csv`, `math/rand/v2`, `database/sql`, `log/slog` |

---

## Builder Agent Brief

Create a new Go project implementing a raffle/pick-from-a-hat web application. Follow the structure, schema, routes, and implementation order defined above. Use Go 1.22+, `html/template` + HTMX for the frontend, PostgreSQL for persistence, goose for migrations, WebSocket for live updates, and Docker for deployment. The server listens on port 8543. All DB operations must be transactional. The core algorithm picks from the pool of non-excluded contestants with the minimum `pick_count`, selected randomly among ties. Include export/import (JSON + CSV), full pick history, and the 12-placeholder default contestants seeded on first run.
