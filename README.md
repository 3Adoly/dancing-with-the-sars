# Dancing with the SARs: Backend (Go)

Go 1.26+ API (`cmd/api`) and ingest CLI (`cmd/ingest`) for the NASA Space Apps 2026 challenge
"Dancing with the SARs": an interactive Earth map of natural incidents with NISAR / Sentinel-1 radar
before/after views and change-detection Stories.

- **System overview (UML, one page):** [`docs/00-Overview-UML.drawio`](docs/00-Overview-UML.drawio). Open it with draw.io / diagrams.net.
- **Contributing (Mohammed and his AI agent):** [`MOHAMMED_CONTRIBUTING.md`](MOHAMMED_CONTRIBUTING.md) and [`HANDOFF_TO_MOHAMMED.md`](HANDOFF_TO_MOHAMMED.md).
- The detailed team plans are shared privately with the team and are not part of this repo.

- API contract: [`api/openapi.yaml`](api/openapi.yaml). Mock responses for the frontend: [`api/examples/`](api/examples).
- No cgo anywhere (`CGO_ENABLED=0` works). Dependencies: chi v5, go-chi/cors, pgx v5, godotenv.

## Status

| Task | What | Status |
|---|---|---|
| **A0** | Contract: `internal/domain`, `internal/store/store.go`, `api/openapi.yaml`, `api/examples/*.json` (E1–E15) | ✅ done |
| **A1** | `cmd/api` (serve + graceful shutdown, `prewarm-sar` stub), `internal/config` (+ SAR mission table), `internal/httpapi` (router, middleware, errors, params, ETag helper), **E1 `/healthz`** | ✅ done |
| A2 | `store/postgres` read side | ⏳ needs Mohammed's M1–M3 (migrations + seed) |
| **A3** | Cube (prefix sums) + breaks + `StatsService` (reload on version change) + **E2 meta, E3 incident-types, E4 choropleth, E5 timeseries** (ETag/304, Cache-Control); dev fixture reader `internal/store/fixture` (`STATS_FIXTURE`) | ✅ done. Real data after A2 |
| A4 | E7 incidents, E8 detail, E9 search | ⏳ after A2 |
| **A5** | `external/asf` client (verified against live ASF), `SARService` (era rule, per-mission parallel search, split/bestPair, LRU → sar_cache → ASF with singleflight, stale fallback) + **E10 `/incidents/{id}/sar`, E11 `/sar/coverage-timeline`**; dev `INCIDENTS_FIXTURE` + in-memory SAR cache | ✅ done. DB cache + real incidents after A2 |
| **A6** | `StoryService` (List/Get/MediaPath/ForIncident → `storyId` in E10) + **E12 `/stories`, E13 `/stories/{id}`, E14 `/media/stories/{id}/{file}`** (ETag, immutable media, traversal-safe); TEMPORARY loader `cmd/api/stories_temp.go` until M14; placeholder stories in `testdata/stories` | ✅ done. Swap in `stories.Load` when M14 lands |
| **A7** | HTTP hardening: router-level contract test over every route (status, Content-Type, Cache-Control per §8.2, ETag/304, X-Request-Id, error JSON, HEAD), CORS preflight/expose tests, gzip only ≥ 1 KB + `Vary`, HEAD support, 504 on request timeout, query guard (≤ 4 KB, valid UTF-8 → else 400), params validated before readiness; `scripts/smoke.sh` + `scripts/smoke.ps1` | ✅ done |
| A8–A11, A13 | grid, prewarm, deploy, perf, hardening | ⏳ |
| A12 | AI chat (**ADDITIONAL — NOT REQUIRED**) | ⏳ only after S7 go |

Right now E1–E5 and E10–E14 are live (E2/E4/E5 need a data source: `STATS_FIXTURE` today, the DB after A2; otherwise **503 `not_ready`**, while E3 always works). E6–E9 answer **501 `not_implemented`** in the standard error JSON. `POST /api/v1/chat` answers
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

### Try E2–E5 today with the dev fixture (no DB)
`testdata/stats_fixture.json` holds synthetic but plausible counts (12 countries, 8 types, 1950–2025, ocean). **It is not real data.**
```bash
STATS_FIXTURE=testdata/stats_fixture.json go run ./cmd/api        # PowerShell: $env:STATS_FIXTURE="testdata/stats_fixture.json"; go run ./cmd/api
curl -s localhost:8080/api/v1/meta
curl -s localhost:8080/api/v1/incident-types
curl -s "localhost:8080/api/v1/stats/choropleth?types=earthquake,flood&from=1950&to=2000"
curl -s "localhost:8080/api/v1/stats/timeseries?types=storm&from=1980&to=1983&iso3=PHL"
curl -s -i -H 'If-None-Match: <ETag from the previous response>' "localhost:8080/api/v1/stats/choropleth?types=earthquake,flood&from=1950&to=2000"   # → 304
```

### Try the SAR drill-down (E10/E11) with dev fixtures (calls the real ASF API)
```bash
STATS_FIXTURE=testdata/stats_fixture.json INCIDENTS_FIXTURE=testdata/incidents_fixture.json go run ./cmd/api
curl -s localhost:8080/api/v1/incidents/500812/sar                    # Türkiye 2023, Sentinel-1 → ok + bestPair
curl -s localhost:8080/api/v1/incidents/700001/sar                    # Aomori 2025-12-08, Sentinel-1 + NISAR GCOV
curl -s localhost:8080/api/v1/incidents/300101/sar                    # Haiti 2010, ALOS
curl -s localhost:8080/api/v1/incidents/400101/sar                    # Bohol 2013 → none (2011–2014 gap, no ASF call)
curl -s localhost:8080/api/v1/incidents/118233/sar                    # Pinatubo 1991 → pre_sar_era (no ASF call)
curl -s "localhost:8080/api/v1/incidents/500812/sar?windowDays=20&refresh=true"
curl -s localhost:8080/api/v1/sar/coverage-timeline
```
Fixture incident ids: 500812, 700001, 700002 (Venezuela 2026), 300101, 400101, 118233, 612004 (Pakistan flood 2022, box search).
Live ASF test: `ASF_LIVE=1 go test ./internal/external/asf -run Live -v`.

### Try the stories (E12–E14) with the dev placeholders
`testdata/stories` holds 2 **PLACEHOLDER** stories with synthetic images (Ibrahim replaces them in `data/stories`).
```bash
STORIES_DIR=testdata/stories STATS_FIXTURE=testdata/stats_fixture.json INCIDENTS_FIXTURE=testdata/incidents_fixture.json go run ./cmd/api
curl -s localhost:8080/api/v1/stories
curl -s localhost:8080/api/v1/stories/demo-two-sensors
curl -s -o thumb.jpg -D - localhost:8080/media/stories/aomori-2025-nisar/thumb.jpg      # image/jpeg, immutable cache
curl -s --path-as-is -o /dev/null -w "%{http_code}
" "localhost:8080/media/stories/aomori-2025-nisar/..%2f..%2f..%2fgo.mod"   # 404
curl -s localhost:8080/api/v1/incidents/700001/sar | grep -o '"storyId":"[^"]*"'                     # linked story
```

### Smoke test (every endpoint, PASS/FAIL/SKIP table)
Start the API (e.g. fixture mode, all three env vars as above), then in another terminal:
```bash
BASE_URL=http://127.0.0.1:8080 bash scripts/smoke.sh                                   # Git Bash / Linux
```
```powershell
powershell -ExecutionPolicy Bypass -File scripts\smoke.ps1 -BaseUrl http://127.0.0.1:8080  # PowerShell
```
Endpoints that still answer 501 (need the DB: E6–E9) and chat when disabled are reported as SKIP; exit code 1 on any FAIL.
Optional: `INCIDENT_ID` (default 500812) for E8/E10 once real DB ids exist.

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

## Live deployment (team server)

The API runs at **http://164.92.235.173:8090** (e.g. [`/healthz`](http://164.92.235.173:8090/healthz),
[`/api/v1/meta`](http://164.92.235.173:8090/api/v1/meta)). For now it runs in **fixture mode**: there's no database, and
it serves the dev data in `testdata/`. ASF radar search is live.

- **How it runs:** a Docker container `dwts-api` built from `Dockerfile.api` (distroless, non-root, read-only), defined by
  [`deploy/compose.server.yml`](deploy/compose.server.yml). Host port **8090** maps to container port 8080, with a 256 MB memory cap
  and `restart: unless-stopped`. The server is shared with other projects: never use ports 80/443/5432/8080/8081, and never
  prune Docker images or caches there.
- **Redeploy** after merging to `main` (needs SSH access as root):
  ```bash
  ssh root@164.92.235.173 'cd /opt/dancing-with-the-sars && git pull --ff-only && VERSION=$(git rev-parse --short HEAD) docker compose -f deploy/compose.server.yml up -d --build'
  BASE_URL=http://164.92.235.173:8090 bash scripts/smoke.sh      # expect FAIL=0
  ```
- **Logs:** `ssh root@164.92.235.173 docker logs --tail 50 dwts-api`
- **Next:** when the DB lands (M1–M3, then A2), add a `postgis` service on a free localhost-only port (5432 and 5435 are taken), set
  `DATABASE_URL`, and remove the `*_FIXTURE` variables.

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
| `INCIDENTS_FIXTURE` | *(empty)* | **DEV-ONLY** incidents for E10 until A2 (e.g. `testdata/incidents_fixture.json`) |
| `ASF_ALOS_LEVEL` | `L1.5` | ALOS processingLevel (verified live) |
| `STATS_FIXTURE` | *(empty)* | **DEV-ONLY** JSON fixture for E2–E5 until A2 (e.g. `testdata/stats_fixture.json`) |
| `STORIES_DIR` | `./data/stories` | Stories manifest + media (dev: `testdata/stories`) |
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
internal/httpapi/                       router, middleware, errors, params, DTOs, /healthz (A1), E2–E5 handlers (A3)
internal/service/                       Cube, Breaks, StatsService (A3)
internal/store/fixture/                 DEV-ONLY StatsReader + IncidentReader(GetIncident) from JSON (A3/A5, replaced by store/postgres in A2)
internal/store/memory/                  in-memory SARCache for no-DB mode (A5)
cmd/api/stories_temp.go                 TEMPORARY story loader (replace with stories.Load from M14)
testdata/stories/                       PLACEHOLDER stories + synthetic images (dev only)
internal/external/asf/                  ASF Search API client (A5)
testdata/fixtures/asf_*.json            REAL trimmed ASF responses (S1 Türkiye, NISAR Aomori, Seasat)
testdata/stats_fixture.json             synthetic stats fixture (not real data)
internal/platform/                      slog + http.Client (M0 file, see HANDOFF_TO_MOHAMMED.md)
```
Import rules R1–R8 (Plan_Middle §4): `httpapi` never imports `store`; `domain` imports nothing internal.
