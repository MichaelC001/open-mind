# Kindle follow-ups — design

Date: 2026-07-15. Issue #8, feature 2 of the four-feature run. All four
sub-features in one slice: per-user Kindle address, scheduled Lens digests,
lead images in EPUBs, EPUB covers.

## Decisions (user-confirmed)

- **Generic `user_settings` table** (not a users column) — more per-user
  settings are coming; first key is `kindle_email`.
- **Settings UI both ways**: a "Send to Kindle" section on the Settings page
  AND an inline set-your-address prompt in the KindleButton 409 state.
- **Schedules are a simple enum** (off / daily / weekly-on-day), not cron.
- **Recurring digests send only new-since-last-digest items**; nothing new →
  no e-mail. The on-demand button keeps its current "top 25 now" behaviour.
- **Scope correction (acknowledged)**: archived bodies are plain text, so
  "in-article images" means each item's **lead image** as a chapter hero +
  EPUB cover; full in-article images would need HTML archiving (out of
  scope).

## Schema (migration 0015, verify next free number)

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

`digest_schedule` values: `''` (off), `daily`, `weekly:0`..`weekly:6`
(0 = Sunday, matching Go's `time.Weekday`). Validated on write.

## Contract (openapi.yaml, contract-first)

- `GET /settings` → `200 {kindleEmail?: string}` — extensible settings
  object; only set keys returned.
- `PATCH /settings {kindleEmail?: string}` → `200` with the updated object.
  Empty string clears the key (row deleted). Basic shape validation
  (`x@y.z`); `400` otherwise.
- Lens `PATCH` (`UpdateLens`) gains optional `digestSchedule` (validated
  enum, `400` on junk). `Lens` schema exposes `digestSchedule` and
  `lastDigestAt`.
- Kindle endpoints unchanged in shape. Recipient resolution becomes:
  user's `kindle_email` setting → instance `KINDLE_EMAIL` env → `409`
  ("kindle is not configured — set your Kindle address in Settings, or set
  KINDLE_EMAIL on the server").

## Scheduled digests

- New River periodic job `scan_digests`, hourly, `RunOnStart`,
  leader-elected on the worker (mirror `poll_feeds`' wiring).
- Due logic (unit-tested, table-driven): `daily` → last digest ≥ 20h ago
  (or never); `weekly:<d>` → today is `<d>` (UTC) AND last digest ≥ 6d ago
  (or never). Tolerant windows so hourly jitter can't skip a period.
  Sends land around 06:00 UTC in practice only insofar as the scan runs
  hourly — no per-user timezone (deferred).
- For each due lens: run the lens rule, filter to items with
  `created_at > last_digest_at` (never-digested → all current matches),
  cap 25, only items with a non-empty body. Zero qualifying items → skip
  entirely (no stamp, no send — it stays due until content appears; a
  weekly lens therefore fires the first week a new item exists).
  Otherwise enqueue the existing `send_kindle` job with the explicit item-ID
  list in the payload (IDs only) and stamp `last_digest_at = now()` on
  successful enqueue.
- `send_kindle` gains an optional `ItemIDs []uuid` payload field: when set,
  it builds the digest from exactly those items (fetched fresh, user-scoped)
  instead of re-running the lens; recipient resolved at send time via the
  chain above. Existing retry semantics unchanged (duplicate-delivery caveat
  stands).

## EPUB quality (`internal/epub`, stdlib-only)

- **Cover**: an XHTML title page always (book title, item count, date,
  "from Openmind"). When a cover image is available (first chapter's lead
  image), it is declared as the EPUB `cover-image` manifest item.
- **Chapter heroes**: each chapter with a `lead_image_url` gets the image
  fetched at build time (SSRF-safe client, 5 MB/image cap, 10s timeout,
  JPEG/PNG/GIF/WebP embedded as-is — no re-encoding); any fetch/type
  failure degrades to no image and never fails the build or send.
- Image fetching happens in the job (`internal/jobs/kindle.go`), passed to
  the epub builder as bytes — `internal/epub` stays network-free and
  deterministic (zip ordering preserved; image filenames derived from
  chapter index, not URLs).

## Web

- **Settings page**: "Send to Kindle" section — address input, save via
  `PATCH /settings` proxy, Amazon approve-sender + find-your-@kindle.com
  reminder copy.
- **KindleButton**: the 409 state renders "Set your Kindle address" linking
  to Settings instead of a bare error.
- **Lens header**: schedule picker (Off / Daily / Weekly + day dropdown)
  writing `digestSchedule` via the existing lens PATCH proxy; shows
  "last sent <relative time>" when `lastDigestAt` is set.
- New `/api/settings` cookie proxy.

## Out of scope

Per-user timezones, full in-article images (needs HTML archiving), digest
e-mail preview, per-item send scheduling, image re-encoding/downscaling.

## Testing

- Unit: schedule-due table (every enum value × never/recent/old last-digest ×
  weekday boundaries); epub cover page + image embedding (deterministic
  output, image-failure degradation); settings validation.
- DB-backed: settings CRUD + cross-tenant scoping; digest scan (due lens
  enqueues with correct item IDs, not-due skipped, empty-new skipped without
  stamping, stamp advances); recipient chain (setting beats env, env
  fallback, neither → 409 with the new message); lens PATCH schedule
  validation.
- Idempotency: running `scan_digests` twice in the same hour double-sends
  nothing (stamp check).
- Compose e2e with `SMTP_HOST=smtp.invalid`: set address via web settings,
  on-demand send uses it (river job attempts SMTP), schedule a lens daily,
  force `last_digest_at` back >20h via psql, restart api → scan enqueues,
  `river_job` shows the attempt; empty lens skipped.
