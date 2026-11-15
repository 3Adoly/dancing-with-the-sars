# How Mohammed (and his AI agent) contributes code to this repository

> **Audience:** Mohammed and the AI coding agent he uses. Paste the "Standing prompt" below into the agent once per session.
> **Owner of `main`:** Ahmed (lead backend). Every change reaches `main` through a **pull request (PR)** that Ahmed reviews and squash-merges.
> **Your task list:** `Mohammed_Guide.md` (M0 → M18) and `Plan_Detailed_Level.md` (the source of truth for names, SQL and JSON). Ahmed shares them with you privately; they are not in this public repo. Put them **outside** the repo folder (e.g. `..\plans\`) so they are never committed by mistake.

---

## 0. One-time setup

1. **Accept the invitation.** Ahmed has invited your GitHub account **Mohammed19N** as a collaborator. Open the e-mail from GitHub (or https://github.com/notifications) and click **Accept**. Without it you cannot push branches.
2. **Install the tools:**
   - **Git:** Git for Windows. It includes Git Credential Manager: the first `git push` opens a browser window where *you* sign in to GitHub. Never put a password or token in a file or a command.
   - **Go 1.26 or newer:** `go.mod` says `go 1.26.0`. Ahmed uses 1.27.4. On Windows: `winget install GoLang.Go`.
   - **Docker Desktop:** for PostgreSQL 17 + PostGIS (your M0/M1).
3. **Clone and set your identity:**
   ```bash
   git clone https://github.com/3Adoly/dancing-with-the-sars.git
   cd dancing-with-the-sars
   git config user.name  "Mohammed <family name>"
   git config user.email "<your GitHub e-mail or the id+username@users.noreply.github.com address>"
   ```
4. **Read in this order:** `README.md` → `HANDOFF_TO_MOHAMMED.md` (files Ahmed created that you now own, and the stories loader swap) → `api/openapi.yaml` → `internal/domain/` and `internal/store/store.go` (the contract) → your private `Mohammed_Guide.md`.
5. **Check that everything is green before you change anything:**
   ```bash
   go build ./... && go vet ./... && go test ./...
   ```

## 1. The loop for every task (M0, M1, …)

**One task = one branch = one PR.** Keep PRs small (under ~600 changed lines when possible). Merge at least every 3 hours.

```bash
# 1. Start from the latest main
git checkout main
git pull --ff-only origin main

# 2. Create the task branch: mohammed/<TaskID>-<short-slug>
git checkout -b mohammed/M1-migrations

# 3. Work. Commit often; every message starts with the task ID
git add <files you changed>          # prefer explicit paths over "git add -A"
git commit -m "M1: goose migrations 0001-0006 and ingest migrate"

# 4. Before pushing: update and run the full check (section 3)
git fetch origin
git rebase origin/main               # keeps history linear; fix conflicts if asked (section 5)
gofmt -l .                           # must print nothing
go build ./... && go vet ./... && go test ./...

# 5. Push the branch (the first push opens the GitHub sign-in in your browser)
git push -u origin mohammed/M1-migrations
```

### Create the pull request
- **Web (simplest):** after the push, git prints a link like `https://github.com/3Adoly/dancing-with-the-sars/pull/new/mohammed/M1-migrations`. Open it, check that **base = `main`** and **compare = your branch**, fill in the template (it loads automatically from `.github/pull_request_template.md`), and click **Create pull request**.
- **GitHub CLI (optional):** `gh pr create --base main --title "M1: goose migrations 0001-0006" --body-file pr.md`. Sign in first with `gh auth login`, which you do yourself in the browser.
- **Title format:** `M<n>: <what it does>`. One task ID per PR. Contract changes use the title prefix `contract:`.
- **Reviewer:** Ahmed. He squash-merges. After the merge:
  ```bash
  git checkout main && git pull --ff-only origin main
  git branch -d mohammed/M1-migrations
  git push origin --delete mohammed/M1-migrations
  ```
- **If Ahmed requests changes:** commit the fixes on the same branch and `git push`. The PR updates automatically. Do not open a new PR.

## 2. What you may change (file ownership)

| You (Mohammed) own | Ahmed owns: do NOT edit without asking |
|---|---|
| `cmd/ingest/**`, `internal/ingest/**`, `internal/stories/**`, `internal/platform/**`, `migrations/**`, `testdata/seed.sql`, `testdata/fixtures/<your sources>`, `internal/store/postgres/{db,writer,boundaries,postprocess,write_test}.go`, `Makefile`, `docker-compose.yml`, `Dockerfile.ingest`, `go.mod`/`go.sum` (with `go mod tidy`) | `cmd/api/**`, `internal/httpapi/**`, `internal/service/**`, `internal/external/**`, `internal/chat/**`, `internal/store/fixture/**`, `internal/store/memory/**`, `internal/store/postgres/{incidents_read,stats_read,sarcache,read_test}.go`, `scripts/smoke.*`, `testdata/stories/**`, `testdata/*_fixture.json`, `Dockerfile.api`, `api/**` |

- **Shared:** `internal/config/config.go`. Add your fields only inside the block marked `--- Ingest fields (Mohammed) ---`.
- **Contract files** (`internal/domain/*.go`, `internal/store/store.go`, `api/openapi.yaml`, merged migrations): change them only in a separate PR whose branch is `contract/<slug>` and whose title starts with `contract:`. Explain why. Both of you must approve. **Never edit a merged migration**; add a new numbered one (`0007_...sql`).
- **The only exception (the M14 swap):** `HANDOFF_TO_MOHAMMED.md` tells you exactly which two lines of `cmd/api` to change and which temporary file to delete when your `stories.Load` lands. Do that in the M14 PR and mention it in the PR description.

## 3. The checks your PR must pass

Run all of these locally before every push. Put the output summary in the PR description.

```bash
gofmt -l .                                   # prints nothing
go build ./...
go vet ./...
go test ./... -count=1                       # all green
# DB integration tests (after M1), with Docker running:
#   TEST_DATABASE_URL=postgres://dwts:dwts@localhost:5432/dwts_test?sslmode=disable go test ./internal/store/... -count=1
# API smoke test (proves you did not break Ahmed's side). In terminal 1:
#   STATS_FIXTURE=testdata/stats_fixture.json INCIDENTS_FIXTURE=testdata/incidents_fixture.json STORIES_DIR=testdata/stories go run ./cmd/api
# In terminal 2:
#   BASE_URL=http://127.0.0.1:8080 bash scripts/smoke.sh     → FAIL=0
```
- Also check the **import rules** (the compiler catches cycles, but not these): `internal/ingest/**` must never import `internal/service`, `internal/httpapi` or `internal/chat`, and only `cmd/*` may import `internal/store/postgres`.
  ```bash
  go list -deps ./internal/ingest/... | grep -E "dwts/backend/internal/(service|httpapi|chat)"   # must print nothing
  ```

## 4. Never commit

- `.env` or any API key, token or password (FIRMS `MAP_KEY`, Earthdata, CONAE). Use `.env` locally; only `.env.example` with **empty** values is committed.
- `data/raw/**` (download cache), `data/snapshots/**` (`pg_dump` files), `data/boundaries/**`, `bin/`. They are already in `.gitignore`; don't force-add them.
- **Raw SAOCOM files** (CONAE license). Only downscaled PNG overlays with the © CONAE credit belong in a story folder.
- Files over ~5 MB. Share large snapshots through the team drive link instead.
- If you committed a secret by accident: **tell Ahmed at once** and rotate the key. Deleting the file in a later commit does not remove it from history.

## 5. Conflicts

- **`go.mod` / `go.sum`:** keep both sides, then run `go mod tidy` and `go build ./...`, then `git add go.mod go.sum` and `git rebase --continue`.
- **`internal/config/config.go`:** keep Ahmed's API block and your ingest block. Both stay.
- Anything in Ahmed's files: stop and ask Ahmed. Don't resolve it by deleting his code.
- To bail out of a messy rebase: `git rebase --abort`. Your branch is back as it was.

## 6. Pull request description (the template asks for this)

```markdown
## Task
M1: goose migrations 0001-0006 + `ingest migrate`

## What changed
- migrations/0001_extensions.sql … 0006_ops.sql (Plan_Detailed §5)
- cmd/ingest: `migrate` subcommand (goose, embedded FS)

## How I verified
- gofmt clean; go build/vet OK; go test: N passed, 0 failed
- `docker compose up -d db && go run ./cmd/ingest migrate` → 6 migrations applied
- psql: `\dt` shows countries, incidents, …
- scripts/smoke.sh against the API: PASS=16 FAIL=0

## Contract / shared files touched
None   (or: list them and why)

## Notes for Ahmed
…
```

## 7. Standing prompt for Mohammed's AI agent (paste once per session)

```text
You are helping Mohammed contribute to the GitHub repo "dancing-with-the-sars" (Go 1.26+, module github.com/dwts/backend).
Before coding, read: README.md, MOHAMMED_CONTRIBUTING.md, HANDOFF_TO_MOHAMMED.md, internal/domain/, internal/store/store.go,
and Mohammed's private Mohammed_Guide.md + Plan_Detailed_Level.md (outside the repo; do NOT copy them into the repo).
Git workflow rules:
- One task = one branch named mohammed/<TaskID>-<slug>, created from an up-to-date main (git pull --ff-only origin main).
- Commit messages start with the task ID ("M6: usgs adapter ..."). Stage explicit paths; never commit .env, data/raw,
  data/snapshots, data/boundaries, bin/, raw SAOCOM files, or any secret/token/key.
- Only edit files Mohammed owns (section 2 of MOHAMMED_CONTRIBUTING.md). Contract files only via a contract/<slug> branch.
  Never edit a merged migration; add a new numbered one.
- Before pushing: git fetch origin && git rebase origin/main; gofmt -l . (empty); go build ./... ; go vet ./... ;
  go test ./... -count=1; check the import rules; run scripts/smoke.sh against the fixture-mode API if you touched
  shared code. Fix everything until green.
- Push with: git push -u origin <branch>. Never force-push to main. Never push directly to main.
- Then create a PR to base main using .github/pull_request_template.md (or tell Mohammed to open the printed link),
  with a title "M<n>: <summary>" and the real verification output in the description. Never type or store passwords or
  tokens: the GitHub sign-in happens in the browser and Mohammed does it himself.
- If something is unclear or a change would touch Ahmed's files, stop and ask Mohammed to check with Ahmed.
```
