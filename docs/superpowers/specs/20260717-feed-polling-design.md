# Adaptive feed polling — per-feed intervals + Cache-Control (Issue #11)

Date: 2026-07-17. Closes GitHub Issue #11. Builds on the existing conditional
GET support (ETag/Last-Modified, migration 0013) and the River periodic
poller (migration 0016 feed-river).

## Goal

Stop polling every feed on a flat 30-minute cycle. Back off feeds that
rarely change, poll active feeds promptly, and never poll faster than a
server's `Cache-Control: max-age` asks.

## Approach

Adaptive backoff floored by server cache hints:
- New items on a refresh → reset interval to the 30-minute floor.
- Unchanged (304, or 200 with no new items) → double the interval, capped at
  24 hours.
- If the response carries `Cache-Control: max-age` (minus `Age`), use that as
  a lower bound on the next interval, so we honour explicit server pacing even
  for an otherwise "active" feed.

## Schema

Migration `0018_feed_polling.sql` (additive, matches the existing additive
migration convention):
- `feeds.next_poll_at timestamptz NOT NULL DEFAULT now()` — when the feed is
  next eligible to poll.
- `feeds.poll_interval_minutes int NOT NULL DEFAULT 30` — current interval,
  the input to the next doubling.

Existing feeds default to `next_poll_at = now()` (eligible immediately) and
the 30-minute floor, so behaviour on first run after deploy is unchanged.

## Behaviour

- `internal/jobs/poll.go`: the 30-minute periodic job still fires globally,
  but selects only feeds with `next_poll_at <= now()` (new sqlc query
  `ListFeedsDueForPoll`). Leader-election and workers-on gating unchanged.
- `internal/feeds/service.go` refresh path: after fetching, compute the next
  interval via a pure helper `nextPollInterval(current time.Duration, hadNewItems bool, cacheMaxAge time.Duration) time.Duration`:
  - `hadNewItems` → floor (30m).
  - else → `min(current*2, 24h)`.
  - then → `max(result, cacheMaxAge)` when `cacheMaxAge > 0`.
  Stamp `next_poll_at = now() + interval`, persist `poll_interval_minutes`.
- Parse `Cache-Control: max-age=N` and subtract the `Age` header when present
  (both integer seconds); ignore `no-cache`/`no-store` for pacing (they don't
  imply an interval).
- Manual refresh and new-subscribe always poll immediately regardless of
  `next_poll_at`, and reset the interval to the floor (user action = signal of
  interest). Unsubscribe path unchanged.

## Testing

- `nextPollInterval` table test: floor on new items; doubling; 24h cap;
  max-age floor raises a would-be-shorter interval; max-age below computed is
  ignored; Age subtraction; zero/absent max-age.
- `parseCacheControlMaxAge(header, ageHeader)` unit test: valid, missing,
  malformed, max-age less than Age → 0.
- Service test (DB-backed, fake fetcher): unchanged feed backs off (interval
  doubles, next_poll_at advances); changed feed resets to 30m; manual refresh
  ignores next_poll_at and resets.
- Poller test: `ListFeedsDueForPoll` returns only due feeds.

## Out of scope

Per-feed user-configurable intervals (UI), jitter/thundering-herd spreading
beyond the natural spread from staggered next_poll_at, `Retry-After` on 429
(feeds rarely rate-limit; revisit if observed).
