# Handoff to Mohammed: M0 files created on Ahmed's side

To compile and test A0 (contract) and A1 (API skeleton) before your M0 landed, Ahmed's side created a minimal
shared skeleton. **These are M0 files. You own them from now on and may change them freely**: just keep
`go build ./... && go test ./...` green.

| File | What it contains now | What you do in M0/M1 |
|---|---|---|
| `go.mod` / `go.sum` | `module github.com/dwts/backend`, `go 1.26.0` (raised by `go mod tidy`: current pgx/chi need Go 1.26+; install Go 1.26 or newer — Ahmed uses 1.27.4); deps chi v5, go-chi/cors, pgx v5, godotenv | Add goose, x/text, x/sync, x/time with `go get` + `go mod tidy` |
| `.gitignore` / `.gitattributes` | bin, .env, data/raw, data/snapshots, data/boundaries; LF line endings; binary images | Extend if needed |
| `.env.example` | All variables of Plan_Detailed §2 (API + chat + ingest block) | Keep the ingest block in sync with your config fields |
| `cmd/ingest/main.go` | Placeholder that prints "not implemented" and exits 2 | Replace with the subcommand dispatcher (M1) |
| `internal/platform/log.go` | `NewLogger(level, format)`, `NewLoggerTo(w, level, format)` | Keep the signatures (cmd/api uses `NewLogger`) |
| `internal/platform/httpx.go` | `NewHTTPClient(timeout)` with UA `dwts-hackathon/1.0` | Keep the signature |
| `internal/config/config.go` | Shared + API block, `Load()`, `FromEnv(getenv)`, `Validate()` | **Add your ingest fields in the marked `--- Ingest fields (Mohammed) ---` block** (RawDir, BoundariesPath, USGSMinMag, IngestWorkers, FIRMSMapKey, FIRMSProduct) using the same `p.getStr/getInt/...` helpers |

**Not created** (still 100% yours): `Makefile`, `docker-compose.yml`, `Dockerfile.ingest`, `migrations/**`,
`testdata/**`, `internal/ingest/**` (including the contract `source.go`), `internal/stories/**`, and the write side of `internal/store/postgres/**`.

**Contract files you must review** (A0, changes only via a `contract/*` PR):
- `internal/domain/*.go`: Detailed §3, plus `TypeInfo`/`TypeCatalog()` (E3) and `cursor.go` (the keyset cursor codec, so both `httpapi` and `store/postgres` can use it).
- `internal/store/store.go`: Detailed §4. `Writer` is your interface.
- `api/openapi.yaml` + `api/examples/*.json`: `go test ./internal/httpapi` checks that every example decodes into its Go type.

Notes:
- `DATABASE_URL` has **no default** in config. An empty value means the API runs without a DB (`/healthz` reports `db:"not_configured"`). `.env.example` sets the docker value.
- `domain.Story` / `StoryLayer` already include the optional per-layer `platform` / `polarization` (SAOCOM story 5). Your `stories.Validate` implements the cross-platform and CONAE-credit rules of Detailed §10.1.
