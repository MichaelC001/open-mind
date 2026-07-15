# Kindle Follow-ups Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Per-user Kindle address (first user setting), scheduled Lens digests (new-items-only), and better EPUBs (cover page + lead-image chapter heroes).

**Architecture:** Migration 0015 adds `user_settings` + two `lenses` columns. Contract gains `GET/PATCH /settings` and `digestSchedule` on Lens. Recipient resolution becomes setting → env → 409. A new hourly River periodic job `scan_digests` finds due lenses and enqueues `send_kindle` with an explicit item-ID list; the worker gains that payload mode plus cover/hero support in `internal/epub` (images fetched in the job, builder stays network-free).

**Tech Stack:** Go stdlib, sqlc, oapi-codegen, River periodic jobs, existing `enrich.SafeHTTPClient`.

**Spec:** `docs/superpowers/specs/20260715-kindle-followups-design.md`

## Global Constraints

- Contract-first; never hand-edit generated code. Migration number 0015 (verify next free).
- `digest_schedule` enum: `''` | `daily` | `weekly:0`..`weekly:6` (0=Sunday, Go `time.Weekday`); validated on write (400 on junk).
- Recipient chain: user `kindle_email` setting → instance `KINDLE_EMAIL` → 409 with EXACT message: `kindle is not configured — set your Kindle address in Settings, or set KINDLE_EMAIL on the server`.
- Digest due logic: daily → last ≥ 20h ago or never; weekly:<d> → today(UTC) == d AND (last ≥ 6d ago or never).
- Digest content: lens matches with `created_at > last_digest_at` (never-digested → all current), non-empty body only, cap 25 (`kindleDigestCap`); zero items → skip WITHOUT stamping; stamp `last_digest_at` only on successful enqueue.
- `internal/epub` stays stdlib-only and network-free: images arrive as `[]byte`; deterministic zip ordering and filenames (`imageNN.<ext>` by chapter index).
- Image fetching in the job: SafeHTTPClient, 5 MB/image cap, 10s timeout, JPEG/PNG/GIF/WebP by content-type sniff, any failure → no image, never fails the send.
- Jobs carry IDs, not blobs. All queries user-scoped. Capture-is-sacred untouched.
- Go from `apps/api` (`env -u GOROOT /opt/homebrew/bin/go` fallback); DB tests `-p 1` with compose db up. No banner comments. UK English copy.

---

### Task 1: Schema + contract + queries (repo will not build until Task 2)

**Files:**
- Create: `apps/api/internal/store/migrations/0015_user_settings_digests.sql`
- Create: `apps/api/internal/store/queries/settings.sql`
- Modify: `apps/api/internal/store/queries/lenses.sql`
- Modify: `openapi.yaml`; regenerate via `task generate`

**Interfaces → Produces:** sqlc `GetUserSetting(user_id,key)`, `UpsertUserSetting(user_id,key,value)`, `DeleteUserSetting(user_id,key)`, `ListUserSettings(user_id)`; `UpdateLensDigestSchedule(user_id,id,digest_schedule)`, `ListDueDigestLenses()` (cross-user, poller), `StampLensDigest(user_id,id)`; generated handler methods `GetSettings(w,r)`, `PatchSettings(w,r)`; `Lens` gains `digestSchedule`/`lastDigestAt`; `UpdateLensRequest` gains optional `digestSchedule`; new `Settings`/`PatchSettingsRequest` schemas `{kindleEmail?: string}`.

- [ ] **Step 1: Migration**

```sql
CREATE TABLE user_settings (
    user_id uuid NOT NULL REFERENCES users(id),
    key text NOT NULL,
    value text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, key)
);
ALTER TABLE lenses ADD COLUMN digest_schedule text NOT NULL DEFAULT '';
ALTER TABLE lenses ADD COLUMN last_digest_at timestamptz;
```

- [ ] **Step 2: Queries**

`settings.sql`:

```sql
-- name: GetUserSetting :one
SELECT value FROM user_settings WHERE user_id = $1 AND key = $2;

-- name: UpsertUserSetting :exec
INSERT INTO user_settings (user_id, key, value) VALUES ($1, $2, $3)
ON CONFLICT (user_id, key) DO UPDATE SET value = EXCLUDED.value, updated_at = now();

-- name: DeleteUserSetting :execrows
DELETE FROM user_settings WHERE user_id = $1 AND key = $2;

-- name: ListUserSettings :many
SELECT key, value FROM user_settings WHERE user_id = $1;
```

Append to `lenses.sql`:

```sql
-- name: UpdateLensDigestSchedule :one
UPDATE lenses SET digest_schedule = $3, updated_at = now()
WHERE user_id = $1 AND id = $2 RETURNING *;

-- name: ListDueDigestLenses :many
-- Cross-user by design: the digest scanner runs system-wide, like the feed
-- poller. Due-ness is refined in Go; SQL just prefilters scheduled lenses.
SELECT * FROM lenses WHERE digest_schedule <> '';

-- name: StampLensDigest :execrows
UPDATE lenses SET last_digest_at = now() WHERE user_id = $1 AND id = $2;
```

- [ ] **Step 3: Contract** — read the existing `/lenses/{id}` and `Lens` blocks first and match style. Add `/settings` (GET → `Settings`, PATCH body `PatchSettingsRequest` → `Settings`, 400 on invalid e-mail); add `digestSchedule` (string) + `lastDigestAt` (nullable date-time) to `Lens`; add optional `digestSchedule` to the lens-update request schema. `Settings`/`PatchSettingsRequest`: `{kindleEmail?: string}`.

- [ ] **Step 4: `task generate`**; expect Go build to fail with missing `GetSettings`/`PatchSettings` on *Server (record the exact error — Task 2's contract).

- [ ] **Step 5: Commit** `feat(kindle): settings + digest schema, contract, queries (handlers next)` — add exactly what `git status` shows changed.

---

### Task 2: Settings handlers + recipient chain + lens schedule PATCH

**Files:**
- Create: `apps/api/internal/api/settings.go`
- Modify: `apps/api/internal/api/kindle.go` (recipient chain — both handlers' 409 gate), `apps/api/internal/api/lenses.go` (UpdateLens accepts digestSchedule), `apps/api/internal/api/ratelimit.go` (guard PATCH /settings)
- Test: `apps/api/internal/api/settings_test.go`, extend `apps/api/internal/api/kindle_test.go`, `apps/api/internal/api/lenses_test.go`, `ratelimit_internal_test.go`

**Interfaces:**
- Consumes: Task 1 queries; existing `userID`, `writeJSON`, `writeError`, `maxBodyBytes`; `kindleSettingKey = "kindle_email"` (define in settings.go, exported within package).
- Produces: `GetSettings`/`PatchSettings` handlers; helper `(s *Server) kindleRecipient(ctx, uid) (string, bool)` returning (address, configured) via chain setting→`s.kindleConfigured`+env... NOTE: the env address lives in `jobs.KindleDeps.To` (worker side); the handler only knows `s.kindleConfigured`. Change the gate semantics: configured = user setting exists OR `s.kindleConfigured`. The WORKER resolves the actual address (Task 3 wires the setting lookup there too — the worker has store access). Also produces `validDigestSchedule(v string) bool` and `validKindleEmail(v string) bool` helpers used by tests.

- [ ] **Step 1: Failing tests**

`settings_test.go` (DB-backed, reuse the package's HTTP-test fixture):
`TestSettingsRoundTrip` (GET empty `{}` → PATCH `{"kindleEmail":"me@kindle.com"}` → 200 echo → GET shows it → PATCH `{"kindleEmail":""}` clears → GET empty); `TestSettingsValidation` (`"notanemail"` → 400; missing field → 200 no-op); `TestSettingsScoped` (user A's setting invisible to user B).
`kindle_test.go` additions: with NO env kindle config, a user WITH a `kindle_email` setting gets 202 on `POST /items/{id}/kindle` (was 409); a user without stays 409 with the EXACT new message.
`lenses_test.go` additions: PATCH lens `{"digestSchedule":"weekly:3"}` → 200 echo, persisted; `"weekly:7"`/`"hourly"` → 400; `""` clears.
`ratelimit_internal_test.go`: `PATCH /settings` guarded true; `GET /settings` false.

- [ ] **Step 2: Run** — compile FAIL (handlers missing).

- [ ] **Step 3: Implement**

`settings.go`: `GetSettings` lists via `ListUserSettings`, maps `kindle_email` → `kindleEmail`. `PatchSettings` (MaxBytesReader): if `KindleEmail != nil` — empty → `DeleteUserSetting`; else `validKindleEmail` (regexp `^[^@\s]+@[^@\s]+\.[^@\s]+$`, ≤254 runes) → 400 fail or `UpsertUserSetting`. Respond with the updated settings object.
`kindle.go`: both handlers replace the bare `s.kindleConfigured` check with: `configured := s.kindleConfigured; if !configured { if _, err := s.store.Queries.GetUserSetting(ctx, db.GetUserSettingParams{UserID: uid, Key: kindleSettingKey}); err == nil { configured = true } }` and the 409 uses the new exact message. (Order of ownership→configured→body checks unchanged.)
`lenses.go` UpdateLens: when request `DigestSchedule != nil`, `validDigestSchedule` (`""`, `"daily"`, or `weekly:d` with d 0-6) → 400 or persist via `UpdateLensDigestSchedule` after the existing name/rule update (or fold into one flow matching the handler's current shape); `toAPILens` maps the two new columns.
`ratelimit.go` guarded(): add `(method == http.MethodPatch && path == "/settings") ||`.

- [ ] **Step 4: Run** — all new + existing api tests pass (`go test -p 1 ./internal/api/`), vet/build clean (repo builds again).

- [ ] **Step 5: Commit** `feat(kindle): per-user address setting, recipient chain, lens digest schedule`

---

### Task 3: Digest scan job + send_kindle ItemIDs mode

**Files:**
- Create: `apps/api/internal/jobs/digest.go`
- Modify: `apps/api/internal/jobs/kindle.go` (SendKindleArgs gains `ItemIDs []uuid.UUID`; worker resolves recipient via setting first; ItemIDs build path), `apps/api/internal/jobs/enrich.go` (register worker + hourly periodic job next to poll_feeds — workersOn only)
- Test: `apps/api/internal/jobs/digest_test.go` (due-logic unit tests + DB-backed scan test), extend `apps/api/internal/jobs/kindle_test.go`

**Interfaces:**
- Consumes: Task 1 queries (`ListDueDigestLenses`, `StampLensDigest`, `GetUserSetting`); existing `runLensRule` equivalent — the jobs package already runs lens rules for digests via `search`(see kindle.go's LensID path — reuse its item-fetch helper); `kindleDigestCap`.
- Produces: `ScanDigestsArgs{}` (`Kind() "scan_digests"`), `ScanDigestsWorker`; `digestDue(schedule string, last pgtype.Timestamptz, now time.Time) bool` (pure, exported within package for tests).

- [ ] **Step 1: Failing tests**

Unit `TestDigestDue` — table: daily never→true; daily 21h→true; daily 3h→false; weekly:2 on a Tuesday(UTC) never→true; weekly:2 on Tuesday last-5d→false; weekly:2 on Tuesday last-7d→true; weekly:2 on Wednesday→false; ''→false; junk→false. (Construct `now` with fixed `time.Date` values — find a Tuesday, e.g. 2026-07-14 is a Tuesday.)
DB-backed `TestScanDigestsEnqueuesNewItemsOnly`: seed lens (schedule daily, last_digest_at 2 days ago) matching two items — one created before last stamp, one after → run worker → exactly one `send_kindle` river job whose args contain only the new item's ID; `last_digest_at` advanced. `TestScanDigestsSkipsEmptyWithoutStamp`: lens due but no new items → no job, stamp unchanged. `TestScanDigestsIdempotentWithinHour`: run twice → still one job.
`kindle_test.go` addition: worker with `ItemIDs` set builds from exactly those items (fake mailer records one message; body-less IDs skipped) and resolves the recipient from the user's `kindle_email` setting when `Deps.To` is empty.

- [ ] **Step 2: Run** — compile FAIL.

- [ ] **Step 3: Implement**

`digest.go`: worker lists `ListDueDigestLenses`, for each `digestDue(...)`: run the lens rule (reuse the same path kindle.go's LensID branch uses to materialise items), filter `created_at.Time.After(last_digest_at.Time)` when last valid + non-empty body, cap `kindleDigestCap`; empty → continue; else Insert `SendKindleArgs{UserID, LensID, ItemIDs}` and `StampLensDigest`. Errors per-lens: log + continue (poller must survive one bad lens).
`kindle.go`: `ItemIDs` non-empty → fetch each via `GetItem` (user-scoped; skip missing), build digest doc titled from the lens name; recipient: `GetUserSetting(kindle_email)` → fallback `Deps.To` → if neither, return error (River retries; mirrors the Configured-flip comment).
`enrich.go`: register `ScanDigestsWorker` and an hourly `river.NewPeriodicJob` with `RunOnStart: true` beside poll_feeds (workersOn branch only).

- [ ] **Step 4: Run** `go test -p 1 ./internal/jobs/` + full suite; vet/build clean.

- [ ] **Step 5: Commit** `feat(kindle): scan_digests periodic job, new-items-only digests, per-user recipient in worker`

---

### Task 4: EPUB cover + chapter hero images

**Files:**
- Modify: `apps/api/internal/epub/epub.go` (`Chapter` gains `Image []byte` + `ImageType string`; `Document` gains `CoverNote string`; title/cover XHTML page; manifest cover-image; hero `<img>` at chapter top), `apps/api/internal/jobs/kindle.go` (fetch lead images)
- Test: extend `apps/api/internal/epub/epub_test.go`, `apps/api/internal/jobs/kindle_test.go`

**Interfaces:**
- Consumes: `Document{Title, Author, Chapters}` (existing), `enrich.SafeHTTPClient`.
- Produces: `Chapter{Title, Body string; Image []byte; ImageType string}` (ImageType one of `image/jpeg|png|gif|webp`); builder emits `cover.xhtml` first in spine, `imageNN.<ext>` entries (NN = 1-based chapter index, ext from type), first chapter image doubles as manifest `cover-image`; `fetchLeadImage(ctx, client, url) ([]byte, string)` in jobs (nil on any failure).

- [ ] **Step 1: Failing tests**

epub: `TestBuild_CoverPageFirstInSpine` (cover.xhtml exists, first spine itemref, contains title + item count text); `TestBuild_ChapterImageEmbedded` (chapter with 1-px PNG bytes → `image01.png` entry present byte-identical, chapter XHTML references it, manifest lists correct media-type, `cover-image` property on it); `TestBuild_NoImagesStillValid` (existing round-trip untouched — strict parser checks stay green); determinism: build twice → identical bytes.
jobs: `TestFetchLeadImage` — httptest server serving a PNG → bytes + `image/png`; 404 → nil; >5 MB → nil; `text/html` → nil.

- [ ] **Step 2: Run** — FAIL.

- [ ] **Step 3: Implement** — keep the raw-xml-prolog pattern and deterministic write order (mimetype, container, opf, cover.xhtml, chapters interleaved with their images by index). Media types mapped from ImageType; unknown type → image dropped. `fetchLeadImage`: GET with 10s ctx timeout, `io.LimitReader(body, 5<<20+1)` (over-cap → nil), `http.DetectContentType` sniff against the allowlist. Wire in the worker: for each digest/item chapter with a `lead_image_url`, best-effort fetch; failures logged at debug, never fatal.

- [ ] **Step 4: Run** epub + jobs suites; vet/build.

- [ ] **Step 5: Commit** `feat(epub): cover page + lead-image chapter heroes (network-free builder)`

---

### Task 5: Web — settings section, KindleButton prompt, lens schedule picker

**Files:**
- Create: `apps/web/app/api/settings/route.ts` (GET+PATCH proxy — copy the feeds proxy pattern)
- Modify: the Settings/Devices page (find it: `grep -rn "Devices" apps/web/app` — add a "Send to Kindle" section), `apps/web/components/KindleButton.tsx` (409 state → "Set your Kindle address" link to the settings page), the Lens header component (schedule picker: select Off/Daily/Weekly + weekday dropdown when weekly; writes `digestSchedule` via the existing lens PATCH proxy; shows "digest last sent <relative>" when `lastDigestAt` set — reuse the existing relative-time helper if one exists, else copy the feeds page's)
- Test: none beyond build/tsc (thin client); the section copy includes the Amazon approve-sender + find-your-@kindle.com reminder (mirror docs/self-hosting.md's Kindle section wording).

- [ ] **Steps:** implement → `pnpm turbo run build --filter=web` green + tsc clean → commit `feat(web): kindle settings section, 409 prompt, lens digest schedule picker`.

---

### Task 6: E2e + docs

- [ ] **Step 1: Compose e2e** (python3 urllib; `docker compose up -d --build api web`): PATCH /settings sets address (via web proxy with cookie, or API direct) → on-demand `POST /items/{id}/kindle` with NO env SMTP → still 409 (SMTP unconfigured) BUT after restarting api with `SMTP_HOST=smtp.invalid SMTP_FROM=x@y.z` (no KINDLE_EMAIL) → 202 for the user with a setting, 409 for the exact new message without; `river_job` shows send attempt + retry (smtp.invalid). Lens: set `digestSchedule=daily` via PATCH, backdate `last_digest_at` 2 days via psql, restart api (RunOnStart) → `scan_digests` completed row + a `send_kindle` job with itemIDs; empty lens skipped, stamp unchanged.
- [ ] **Step 2: Docs** — `docs/self-hosting.md` Kindle section: per-user address (Settings page), env fallback, schedule picker + new-items-only semantics + UTC hour caveat; `.env.example` note that KINDLE_EMAIL is now the fallback. Close issue #8 on merge (`gh issue close 8 --comment ...`). Commit `docs: kindle follow-ups — per-user address, digests`.
