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

### Browser extension (`apps/extension`, WXT)

- Part of the pnpm workspace, so `pnpm install` covers it. Standard scripts are in `apps/extension/README.md` (`pnpm --filter extension dev` for HMR; `pnpm --filter extension build` → `.output/chrome-mv3`; lint is `tsc --noEmit`).
- To test in this VM, build then load unpacked (`chrome://extensions` → Developer mode → Load unpacked → `apps/extension/.output/chrome-mv3`), or launch Chrome with `--load-extension=<abs path>/.output/chrome-mv3` to skip the file picker.
- Configure the Options page: **Instance URL = the web app origin** (`http://localhost:3000`, not the Go API `:8080` — the extension calls `{instanceUrl}/api/*`, which only the web proxy serves) and any token (local mode). The extension sends a `Bearer` header and bypasses browser CORS via its `host_permissions`, so it works without any CORS workaround.

### Mobile app (`apps/mobile`, Expo SDK 57)

- **Standalone project — NOT in the pnpm workspace.** It has its own `package-lock.json`; install with `npm install` **inside `apps/mobile`** (the update script does this). Add libs with `./node_modules/.bin/expo install <lib>`.
- Standard run/verify commands are in `apps/mobile/README.md` (`npx expo start --web` → web preview on :8081; `tsc --noEmit`; `expo export --platform web`). Point Settings → Instance URL at the web app origin (`http://localhost:3000`); it calls `{instanceUrl}/api/*` with a Bearer token.
- **Non-obvious caveat:** the `--web` preview's cross-origin calls to the web proxy (:3000) are blocked by **browser CORS** — this is a web-preview-only artifact (the app's README calls web "a preview surface only"). Native builds / Expo Go and the extension are unaffected. To exercise the live capture flow in a browser, launch a throwaway Chrome with `--disable-web-security --user-data-dir=/tmp/<name>` pointed at http://localhost:8081. The **share sheet** requires a native dev build (`expo run:ios|android`) and cannot run on web or in Expo Go.
