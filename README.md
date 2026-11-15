# Dancing with the SARs: Backend (Go)

Go 1.26+ API (`cmd/api`) and ingest CLI (`cmd/ingest`) for the NASA Space Apps 2026 challenge
"Dancing with the SARs". The plans live one folder up: `../Plan_High_Level.md`, `../Plan_Middle_Level.md`,
`../Plan_Detailed_Level.md` (source of truth), `../Plan_Collaboration.md`, `../Mohammed_Guide.md`.

- API contract: [`api/openapi.yaml`](api/openapi.yaml). Mock responses for the frontend: [`api/examples/`](api/examples).
- No cgo anywhere (`CGO_ENABLED=0` works). Dependencies: chi v5, go-chi/cors, pgx v5, godotenv.

## Status

| Task | What | Status |
|---|---|---|
| **A0** | Contract: `internal/domain`, `internal/store/store.go`, `api/openapi.yaml`, `api/examples/*.json` (E1–E15) | ✅ done (awaiting review) |
| **A1** | `cmd/api` (serve + graceful shutdown, `prewarm-sar` stub), `internal/config` (+ SAR mission table), `internal/httpapi` (router, middleware, errors, params, ETag helper), **E1 `/healthz`** | ✅ done (awaiting review) |
| A2 | `store/postgres` read side | ⏳ needs Mohammed's M1–M3 (migrations + seed) |
| A3 | Cube + E2 meta, E3 types, E4 choropleth, E5 timeseries | ⏳ after A2 |
| A4 | E7 incidents, E8 detail, E9 search | ⏳ after A2 |
| A5 | ASF client + E10 sar, E11 coverage-timeline | ⏳ |
| A6 | E12/E13 stories, E14 media | ⏳ needs M14 (stories loader) |
| A7–A11, A13 | ETag wiring, grid, prewarm, deploy, perf, hardening | ⏳ |
| A12 | AI chat (**ADDITIONAL — NOT REQUIRED**) | ⏳ only after S7 go |

Right now `/healthz` is live. E2–E14 answer **501 `not_implemented`** in the standard error JSON. `POST /api/v1/chat` answers
**503 `chat_disabled`**. Use `api/examples/*.json` as mocks for the real shapes.

## Run the API locally (Windows)

No database is needed for A1. Without `DATABASE_URL`, `/healthz` reports `"status":"degraded","db":"not_configured"`.

**PowerShell**
```powershell
cd "D:\Project\Mine\NASA HACKATHON\backend"
go mod download
go run ./cmd/api                      # listens on :8080
# in another terminal:
Invoke-RestMethod http://localhost:8080/healthz
# with a database (after Mohammed's `docker compose up -d db` + migrations):
$env:DATABASE_URL = "postgres://dwts:dwts@localhost:5432/dwts?sslmode=disable"; go run ./cmd/api
```

**Git Bash**
```bash
cd "/d/Project/Mine/NASA HACKATHON/backend"
go run ./cmd/api
curl -s localhost:8080/healthz
DATABASE_URL="postgres://dwts:dwts@localhost:5432/dwts?sslmode=disable" go run ./cmd/api
```

Or copy `.env.example` to `.env`. It is loaded automatically, and real environment variables win.

### Build, vet, test (no `make` needed)
```bash
go build ./...
go vet ./...
go test ./...            # add -race only where cgo/gcc is available (Linux VM / CI)
gofmt -l .               # must print nothing
CGO_ENABLED=0 go build -trimpath -o bin/ ./cmd/...     # static binaries (PowerShell: $env:CGO_ENABLED=0; go build ...)
```

### Subcommands
```
api            # = api serve
api serve
api prewarm-sar --top 200     # task A9, currently prints "not implemented yet"
```

## Environment variables (API)

| Var | Default | Notes |
|---|---|---|
| `DATABASE_URL` | *(empty)* | Empty = run without DB (degraded) |
| `HTTP_ADDR` | `:8080` | |
| `CORS_ORIGINS` | `http://localhost:5173,http://localhost:3000` | Comma-separated |
| `REQUEST_TIMEOUT` | `10s` | Per-request context deadline |
| `DB_MAX_CONNS` | `10` | |
| `LOG_LEVEL` / `LOG_FORMAT` | `info` / `text` | `json` in Docker |
| `MIN_YEAR` | `1900` | |
| `CUBE_RELOAD_INTERVAL` | `60s` | Used from A3 |
| `STATIC_DIR` | *(empty)* | Serve the frontend build (later) |
| `STORIES_DIR` | `./data/stories` | Used from A6 |
| `ASF_BASE_URL` | `https://api.daac.asf.alaska.edu/services/search/param` | Used from A5 |
| `ASF_TIMEOUT` / `SAR_WINDOW_DAYS` / `SAR_CACHE_TTL` / `SAR_DB_FRESH` | `8s` / `45` / `24h` / `168h` | Used from A5 |
| `CHAT_ENABLED`, `LLM_PROVIDERS`, `*_API_KEY`, `*_MODEL`, `CHAT_RATE_PER_MIN` | `false`, … | ADDITIONAL — NOT REQUIRED |

The full list (including ingest vars) is in `.env.example` and Plan_Detailed §2.

## Layout (so far)
```
api/openapi.yaml, api/examples/*.json   contract + mocks (A0)
cmd/api/main.go                         serve | prewarm-sar (A1)
cmd/ingest/main.go                      placeholder (Mohammed)
internal/domain/                        shared types, errors, cursor codec (A0, contract)
internal/store/store.go                 persistence interfaces (A0, contract)
internal/config/                        env config + SAR mission era table (A1)
internal/httpapi/                       router, middleware, errors, params, DTOs, /healthz (A1)
internal/platform/                      slog + http.Client (M0 file, see HANDOFF_TO_MOHAMMED.md)
```
Import rules R1–R8 (Plan_Middle §4): `httpapi` never imports `store`; `domain` imports nothing internal.
