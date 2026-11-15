# Targets follow Plan_Detailed_Level.md section 13. `-include` keeps `make test`/`make lint` working before .env exists.
-include .env
export
SOURCE ?= usgs
SNAP ?= data/snapshots/dwts_final.dump
TEST_DATABASE_URL ?= postgres://dwts:dwts@localhost:5432/dwts_test?sslmode=disable

up:               ; docker compose up -d db
down:             ; docker compose down
psql:             ; docker compose exec db psql -U dwts dwts
wait-db:
	@for i in $$(seq 1 60); do docker compose exec -T db pg_isready -U dwts -d dwts >/dev/null 2>&1 && exit 0; sleep 1; done; echo "wait-db: timed out after 60s" >&2; exit 1
migrate: wait-db  ; go run ./cmd/ingest migrate
boundaries:       ; go run ./cmd/ingest boundaries
seed:             ; docker compose exec -T db psql -U dwts -d dwts -v ON_ERROR_STOP=1 < testdata/seed.sql && go run ./cmd/ingest postprocess
ingest:           ; go run ./cmd/ingest run --source $(SOURCE)
# ibtracs: the full run re-reads the cached ALL file, which can overwrite fresher current-season rows, so refresh the last 30d afterwards.
ingest-all:       ; go run ./cmd/ingest run --all && go run ./cmd/ingest run --source ibtracs --since 30d && go run ./cmd/ingest postprocess
ingest-recent:    ; go run ./cmd/ingest run --source usgs,eonet,gdacs,ibtracs --since 30d && go run ./cmd/ingest postprocess
postprocess:      ; go run ./cmd/ingest postprocess
stories-validate: ; go run ./cmd/ingest stories-validate
api:              ; go run ./cmd/api
prewarm-sar:      ; go run ./cmd/api prewarm-sar --top 200
snapshot:         ; mkdir -p data/snapshots && docker compose exec -T db pg_dump -U dwts -Fc dwts > $(SNAP)
restore:          ; docker compose exec -T db pg_restore -U dwts -d dwts --clean --if-exists --no-owner < $(SNAP)
test:             ; go test ./... -count=1
test-int:         ; TEST_DATABASE_URL='$(TEST_DATABASE_URL)' go test ./... -p 1 -count=1 -run Integration
lint:             ; go vet ./... && test -z "$$(gofmt -l .)" && { test ! -d internal/service || { deps="$$(go list -deps ./internal/service)" && ! echo "$$deps" | grep -q /internal/ingest; }; }
smoke:            ; bash scripts/smoke.sh http://localhost:8080
build:            ; CGO_ENABLED=0 go build -trimpath -o bin/ ./cmd/...
docker:           ; docker compose build
reset-db:         ; docker compose down -v && docker compose up -d db

# Create the separate integration-test database `dwts_test` (same container) if missing, then migrate it.
# (0001_extensions.sql creates postgis + pg_trgm.) DATABASE_URL is overridden for this one command only.
test-db:
	docker compose exec -T db psql -U dwts -d dwts -tAc "SELECT 1 FROM pg_database WHERE datname = 'dwts_test'" | grep -q 1 || docker compose exec -T db psql -U dwts -d dwts -v ON_ERROR_STOP=1 -c "CREATE DATABASE dwts_test"
	DATABASE_URL='$(TEST_DATABASE_URL)' go run ./cmd/ingest migrate

.PHONY: up down psql wait-db migrate boundaries seed ingest ingest-all ingest-recent postprocess stories-validate api prewarm-sar snapshot restore test test-int test-db lint smoke build docker reset-db
