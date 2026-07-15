# Feeds conditional GET — design

Date: 2026-07-14. Scope: the last Milestone 2 "Next" item — HTTP conditional
requests on RSS/Atom feed polls so an unchanged feed costs one 304 round-trip
instead of a full body download + re-parse. Pure optimisation: dedup already
makes re-polls idempotent, so correctness is unchanged.

## Decision

Validators are stored **in the database**, not in memory: the poller runs
every 30 minutes and the process restarts on every deploy, so an in-memory
cache would forget validators exactly when it matters.

## Changes

### Schema

Migration `0012_feed_validators.sql`:

```sql
ALTER TABLE feeds ADD COLUMN etag text NOT NULL DEFAULT '';
ALTER TABLE feeds ADD COLUMN last_modified text NOT NULL DEFAULT '';
```

Both are opaque strings taken verbatim from response headers (`ETag`,
`Last-Modified`) — never parsed or compared locally. Empty means "no
validator known". sqlc regenerated; no `openapi.yaml` change (the columns
are not exposed on the Feed API type).

### `internal/feeds` service

- `fetchAndParse(ctx, feedURL, etag, lastModified string)` sends
  `If-None-Match: <etag>` and/or `If-Modified-Since: <lastModified>` when
  non-empty. Responses:
  - **304** → return a package-level sentinel `errNotModified` without
    reading or parsing the body.
  - **200** (any 2xx) → parse as today, and also return the response's
    `ETag`/`Last-Modified` header values (empty when absent).
  - other statuses / transport errors → error, as today.
- `Refresh`:
  - on `errNotModified`: `recordStatus(..., "ok")` (stamping
    `last_polled_at` so the feed isn't immediately due again), keep the
    stored validators untouched, return `(0, nil)`.
  - on success: save entries as today, then persist the new validators with
    the status write (extend the `SetFeedPolled` query with `etag` and
    `last_modified` params, or a companion `SetFeedValidators` update —
    implementer's choice, one write preferred).
  - on error: record `error: …` as today and **preserve** the stored
    validators (a transient failure must not force a full re-download next
    poll; a stale validator is harmless — the server just 200s).
- `Add` (first fetch) stores the validators from its initial 200 response.

### Behavioural notes

- A server that ignores conditional headers always 200s → behaviour exactly
  as today.
- A 304 counts as a healthy poll: `last_status = "ok"` (no new status
  string, no UI change).

## Out of scope

Poll-frequency adaptation, per-feed intervals, `Cache-Control`/`Age`
handling, exposing validators in the API.

## Testing

Service tests with the injectable `HTTPClient` (fake round-tripper):

- 200 with `ETag`+`Last-Modified` → validators persisted on the feed row.
- Second poll sends `If-None-Match`/`If-Modified-Since`; a 304 (served with
  a garbage body) creates no items, records `ok`, keeps validators — proving
  the body is never parsed.
- Changed content: 200 with new validators → row updated.
- Server sends neither header → both columns stay empty, polls keep working.
- Fetch error → old validators preserved.

DB-backed (existing feeds test harness): migration round-trip via the
regenerated sqlc types.
