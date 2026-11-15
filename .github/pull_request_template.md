## Task
<!-- Task ID + one line, e.g. "M1: goose migrations 0001-0006 + ingest migrate" -->

## What changed
-

## How I verified
<!-- Paste real output summaries -->
- [ ] `gofmt -l .` prints nothing
- [ ] `go build ./...` and `go vet ./...` OK
- [ ] `go test ./... -count=1`: __ passed, 0 failed
- [ ] (if DB code) integration tests against Docker Postgres
- [ ] (if shared code touched) `scripts/smoke.sh` against the fixture-mode API: FAIL=0

## Contract / shared files touched
<!-- None, or list them (domain, store.go, openapi, merged migrations, config.go) and say why -->
None

## Safety
- [ ] No secrets, `.env`, raw data, snapshots or raw SAOCOM files committed

## Notes for the reviewer
