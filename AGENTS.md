# AGENTS.md

Project overview, product principles, repo layout, and coding conventions live in `CLAUDE.md`. Read it first. Task tracker is `TODO.md`.

## Cursor Cloud specific instructions

The dev stack is three services: **Postgres (pgvector)**, the **Go API + River worker** (one binary), and the **Next.js web app**. AI defaults to `noop` and auth defaults to off, so no secrets/API keys are needed for local dev or testing. Standard commands live in `Taskfile.yml`, `docker-compose.yml`, and `docs/self-hosting.md`.

Startup order and non-obvious caveats (the update script only refreshes deps — services must be started manually each session):

- **Docker isn't running at session start.** Start the daemon before anything else: `sudo dockerd > /tmp/dockerd.log 2>&1 &` (then `sudo chmod 666 /var/run/docker.sock` so `docker`/`docker compose` work without sudo). Docker is configured for `fuse-overlayfs` + `iptables-legacy` in `/etc/docker/daemon.json`.
- **Start Postgres:** `docker compose up -d db` (pgvector, host port **5433**, not 5432). It also auto-creates the `openmind_test` database used by the Go tests.
- **The Go binary reads `DATABASE_URL` (and `TEST_DATABASE_URL`) straight from the environment — there is no `.env` auto-loading.** You must export them or every `go run ./cmd/openmind ...` command fails with a unix-socket connection error. Locally:
  - `export DATABASE_URL=postgres://openmind:openmind@localhost:5433/openmind`
  - `export TEST_DATABASE_URL=postgres://openmind:openmind@localhost:5433/openmind_test` (Go test suite only)
- **Run the backend:** `cd apps/api && go run ./cmd/openmind migrate && go run ./cmd/openmind all` (`all` = API on :8080 **and** the enrichment workers in one process; without the workers, saves never leave `pending`). Migrations also run automatically on `all`/`serve` start.
- **Run the web app:** `API_URL=http://localhost:8080 pnpm --filter web dev` (Next.js on :3000). Log in at `/login` with any value (token auth is off in local mode).
- **Never run `pnpm turbo run build` / `next build` while `next dev` is running.** They share `apps/web/.next`; a concurrent build corrupts it and the dev server then throws `TypeError: __webpack_modules__[moduleId] is not a function` / 500s. Fix: stop the dev server, `rm -rf apps/web/.next`, restart.
- **Lint:** `task lint` needs **golangci-lint v2** built with Go ≥ 1.25 (the `.golangci.yml` is `version: "2"`; an older binary refuses to run against the 1.25 module). Note CI (`.github/workflows/ci.yml`) does **not** run golangci-lint — it runs `go build` + `go vet` + `go test` for the API and `turbo run lint`+`build` for web. `task lint` currently reports 5 pre-existing `errcheck` findings that CI does not gate on.
- **Codegen** (`task generate`: oapi-codegen + sqlc + openapi-typescript) output is committed, so it is not needed for a normal run. Re-run it only after editing `openapi.yaml`, `*.sql`, or query files.
- Known pre-existing UI quirk (not an environment issue): saving an item while a search filter (`?q=...`) is active does not refresh the grid until you navigate away and back — the save itself succeeds (`POST /items` → 201).
