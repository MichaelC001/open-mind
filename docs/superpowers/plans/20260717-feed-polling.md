# Adaptive Feed Polling Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Poll each feed on an adaptive interval — reset to 30 min on new items, back off (double, cap 24 h) when unchanged, and never faster than a server's `Cache-Control: max-age`.

**Architecture:** Two new `feeds` columns (`next_poll_at`, `poll_interval_minutes`) drive per-feed scheduling. Two pure helpers compute the next interval and parse cache headers. The feed service stamps the interval after each poll; the periodic job selects only feeds that are due.

**Tech Stack:** Go, sqlc + pgx, River. No new dependencies.

## Global Constraints

- All queries through sqlc in `internal/store`; never hand-edit generated sqlc output. Regenerate with `task generate` (if the local `go` is a goenv shim failing on go.work ≥1.25, use `env -u GOROOT /opt/homebrew/bin/go`).
- The periodic poller (`ListFeedsDue`/`RefreshDue`) is deliberately cross-user; per-feed item creation stays scoped to each feed's `user_id`.
- Errors wrapped `fmt.Errorf("doing x: %w", err)`.
- No banner-style comments (`// ==== X ====`).
- DB-backed store/service tests run against the compose Postgres; `docker compose up -d db` first, `TEST_DATABASE_URL` per `.env.example`. Full suite runs `go test ./... -p 1` (pre-existing shared-DB parallelism flakiness).
- There is NO manual-refresh HTTP endpoint; feeds refresh only via `Service.Add` (subscribe) and `Service.RefreshDue` (periodic). Do not invent one.

---

### Task 1: Pure interval + Cache-Control helpers

**Files:**
- Create: `apps/api/internal/feeds/polling.go`
- Test: `apps/api/internal/feeds/polling_test.go`

**Interfaces:**
- Produces:
  - `func nextPollInterval(current time.Duration, hadNewItems bool, cacheMaxAge time.Duration) time.Duration`
  - `func parseCacheControlMaxAge(cacheControl, age string) time.Duration`
  - constants `pollFloor = 30 * time.Minute`, `pollCap = 24 * time.Hour`

- [ ] **Step 1: Write the failing test**

```go
package feeds

import (
	"testing"
	"time"
)

func TestNextPollInterval(t *testing.T) {
	tests := []struct {
		name        string
		current     time.Duration
		hadNew      bool
		cacheMaxAge time.Duration
		want        time.Duration
	}{
		{"new items reset to floor", 4 * time.Hour, true, 0, pollFloor},
		{"unchanged doubles", 30 * time.Minute, false, 0, time.Hour},
		{"doubling caps at 24h", 20 * time.Hour, false, 0, pollCap},
		{"cache max-age raises a shorter interval", 30 * time.Minute, false, 0, time.Hour},
		{"cache max-age floors even on new items", 0, true, 2 * time.Hour, 2 * time.Hour},
		{"cache max-age below computed is ignored", 30 * time.Minute, false, 10 * time.Minute, time.Hour},
		{"zero current backs off from floor", 0, false, 0, pollFloor},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextPollInterval(tt.current, tt.hadNew, tt.cacheMaxAge); got != tt.want {
				t.Errorf("nextPollInterval(%v,%v,%v) = %v, want %v", tt.current, tt.hadNew, tt.cacheMaxAge, got, tt.want)
			}
		})
	}
}

func TestParseCacheControlMaxAge(t *testing.T) {
	tests := []struct {
		name, cc, age string
		want          time.Duration
	}{
		{"valid", "max-age=3600", "", time.Hour},
		{"with other directives", "public, max-age=600, must-revalidate", "", 10 * time.Minute},
		{"subtracts age", "max-age=3600", "600", 50 * time.Minute},
		{"age exceeds max-age", "max-age=100", "200", 0},
		{"absent", "public", "", 0},
		{"empty", "", "", 0},
		{"malformed", "max-age=abc", "", 0},
		{"no-store ignored for pacing", "no-store", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseCacheControlMaxAge(tt.cc, tt.age); got != tt.want {
				t.Errorf("parseCacheControlMaxAge(%q,%q) = %v, want %v", tt.cc, tt.age, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run from `apps/api`: `go test ./internal/feeds/ -run 'TestNextPollInterval|TestParseCacheControlMaxAge' -v`
Expected: FAIL — undefined `nextPollInterval`, `parseCacheControlMaxAge`, `pollFloor`, `pollCap`.

- [ ] **Step 3: Implement**

Create `apps/api/internal/feeds/polling.go`:

```go
package feeds

import (
	"strconv"
	"strings"
	"time"
)

// Adaptive poll bounds. A feed that keeps producing items is polled every
// pollFloor; a quiet one backs off by doubling up to pollCap.
const (
	pollFloor = 30 * time.Minute
	pollCap   = 24 * time.Hour
)

// nextPollInterval computes when a feed should next be polled. New items reset
// to the floor; an unchanged poll doubles the current interval (capped). A
// server's Cache-Control max-age is a hard lower bound in every case, so we
// never poll faster than the origin asked.
func nextPollInterval(current time.Duration, hadNewItems bool, cacheMaxAge time.Duration) time.Duration {
	var next time.Duration
	if hadNewItems {
		next = pollFloor
	} else {
		next = current * 2
		if next < pollFloor {
			next = pollFloor
		}
		if next > pollCap {
			next = pollCap
		}
	}
	if cacheMaxAge > next {
		next = cacheMaxAge
	}
	return next
}

// parseCacheControlMaxAge extracts max-age (seconds) from a Cache-Control
// header and subtracts the Age header when present, yielding the remaining
// freshness lifetime. Returns 0 when absent, malformed, or already stale.
func parseCacheControlMaxAge(cacheControl, age string) time.Duration {
	var maxAge time.Duration
	found := false
	for _, part := range strings.Split(cacheControl, ",") {
		part = strings.TrimSpace(part)
		if v, ok := strings.CutPrefix(part, "max-age="); ok {
			secs, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil || secs < 0 {
				return 0
			}
			maxAge = time.Duration(secs) * time.Second
			found = true
		}
	}
	if !found {
		return 0
	}
	if age != "" {
		if a, err := strconv.Atoi(strings.TrimSpace(age)); err == nil && a > 0 {
			maxAge -= time.Duration(a) * time.Second
		}
	}
	if maxAge < 0 {
		return 0
	}
	return maxAge
}
```

- [ ] **Step 4: Run to verify it passes**

Run from `apps/api`: `go test ./internal/feeds/ -run 'TestNextPollInterval|TestParseCacheControlMaxAge' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/api/internal/feeds/polling.go apps/api/internal/feeds/polling_test.go
git commit -m "feat(feeds): adaptive poll-interval + Cache-Control helpers"
```

---

### Task 2: Schema + sqlc queries

**Files:**
- Create: `apps/api/internal/store/migrations/0018_feed_polling.sql`
- Modify: `apps/api/internal/store/queries/feeds.sql`
- Regenerates: `apps/api/internal/store/db/*` (do not hand-edit)

**Interfaces:**
- Consumes: nothing from Task 1.
- Produces:
  - `feeds.next_poll_at timestamptz NOT NULL DEFAULT now()`, `feeds.poll_interval_minutes int NOT NULL DEFAULT 30`
  - `SetFeedPolled` gains `NextPollAt` (timestamptz) and `PollIntervalMinutes` (int32) params
  - new `ListFeedsDueForPoll() []Feed` selecting `next_poll_at <= now()`

- [ ] **Step 1: Write the migration**

Create `apps/api/internal/store/migrations/0018_feed_polling.sql`:

```sql
-- Per-feed adaptive polling. next_poll_at gates when the periodic poller may
-- refresh a feed; poll_interval_minutes is the current interval that backs off
-- (doubles) while a feed is quiet and resets to the floor on new items.
-- Defaults make every existing feed eligible immediately at the 30-min floor,
-- so behaviour on first run after deploy is unchanged.
ALTER TABLE feeds ADD COLUMN next_poll_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE feeds ADD COLUMN poll_interval_minutes int NOT NULL DEFAULT 30;
CREATE INDEX feeds_next_poll_idx ON feeds (next_poll_at);
```

- [ ] **Step 2: Update queries**

In `apps/api/internal/store/queries/feeds.sql`, replace the `SetFeedPolled` block with:

```sql
-- name: SetFeedPolled :exec
UPDATE feeds SET last_polled_at = $3, last_status = $4, etag = $5, last_modified = $6,
  next_poll_at = $7, poll_interval_minutes = $8
WHERE user_id = $1 AND id = $2;
```

and append:

```sql
-- name: ListFeedsDueForPoll :many
-- Cross-user by design (system-wide poller). Only feeds whose adaptive
-- schedule has come due; items saved remain scoped to each feed's user_id.
SELECT * FROM feeds WHERE next_poll_at <= now() ORDER BY next_poll_at ASC;
```

Leave `ListFeedsDue` in place for now (removed in Task 3 once unreferenced, or leave if still used — verify with grep in Task 3).

- [ ] **Step 3: Regenerate + build**

Run from repo root: `task generate`
Then from `apps/api`: `go build ./...`
Expected: `SetFeedPolledParams` gains `NextPollAt pgtype.Timestamptz` and `PollIntervalMinutes int32`; `ListFeedsDueForPoll` method generated; build fails only at the now-stale `SetFeedPolled` caller in `service.go` (fixed in Task 3) — note the compiler error location, that is expected.

- [ ] **Step 4: Commit**

```bash
git add apps/api/internal/store/migrations/0018_feed_polling.sql apps/api/internal/store/queries/feeds.sql apps/api/internal/store/db
git commit -m "feat(feeds): schema + queries for per-feed poll scheduling"
```

---

### Task 3: Wire adaptive scheduling into the service + poller

**Files:**
- Modify: `apps/api/internal/feeds/service.go`
- Modify: `apps/api/internal/jobs/poll.go`
- Test: `apps/api/internal/feeds/service_test.go` (append)

**Interfaces:**
- Consumes: `nextPollInterval`/`parseCacheControlMaxAge`/`pollFloor` (Task 1); `SetFeedPolled` new params + `ListFeedsDueForPoll` (Task 2).
- Produces: `Service.RefreshDue(ctx context.Context) error` (drops the `olderThan` arg); `jobs.FeedRefresher` interface updated to match.

- [ ] **Step 1: Write the failing service test**

Append to `apps/api/internal/feeds/service_test.go`, reusing that file's existing harness (read it first for the store setup + fake-server pattern). Cover:
- Unchanged feed (304 or 200 with no new URLs) → after `Refresh`, its `poll_interval_minutes` doubles (30→60) and `next_poll_at` ≈ now + new interval.
- Changed feed (200 with a new entry URL) → `poll_interval_minutes` resets to 30.
- `Add` (subscribe) → new feed row has `poll_interval_minutes` = 30 and `next_poll_at` ≈ now + 30m.
- A feed whose response sends `Cache-Control: max-age=7200` while unchanged → interval is at least 120 min (cache floor beats the 60 min doubling).

Write real assertions (fetch the feed row back via `GetFeed` and compare `PollIntervalMinutes`; assert `NextPollAt` is within a few seconds of the expected wall clock).

- [ ] **Step 2: Run to verify it fails**

Run from `apps/api`: `go test ./internal/feeds/ -run TestRefresh -v` (or the new test names)
Expected: FAIL (build error at the stale `SetFeedPolled` call, then assertion failures).

- [ ] **Step 3: Thread cache max-age through fetchAndParse**

Change `fetchAndParse` to also return `cacheMaxAge time.Duration`, computed via `parseCacheControlMaxAge(resp.Header.Get("Cache-Control"), resp.Header.Get("Age"))`, for BOTH the 304 path and the 200 path:

```go
func (s *Service) fetchAndParse(ctx context.Context, feedURL, etag, lastModified string) (Feed, string, string, time.Duration, error) {
	// ... unchanged setup ...
	if resp.StatusCode == http.StatusNotModified {
		return Feed{}, "", "", parseCacheControlMaxAge(resp.Header.Get("Cache-Control"), resp.Header.Get("Age")), errNotModified
	}
	// ... unchanged status/read/parse ...
	return parsed, resp.Header.Get("ETag"), resp.Header.Get("Last-Modified"),
		parseCacheControlMaxAge(resp.Header.Get("Cache-Control"), resp.Header.Get("Age")), nil
}
```

Update all `fetchAndParse` call sites in `Add` and `Refresh` for the new return arity.

- [ ] **Step 4: Extend recordStatus to stamp the schedule**

Change `recordStatus` to accept the computed next interval and current interval, and write the new columns:

```go
func (s *Service) recordStatus(ctx context.Context, feed db.Feed, status, etag, lastModified string, nextInterval time.Duration) {
	if err := s.Store.Queries.SetFeedPolled(ctx, db.SetFeedPolledParams{
		UserID: feed.UserID, ID: feed.ID, LastPolledAt: nowTS(), LastStatus: status,
		Etag: etag, LastModified: lastModified,
		NextPollAt:          pgtype.Timestamptz{Time: time.Now().Add(nextInterval), Valid: true},
		PollIntervalMinutes: int32(nextInterval / time.Minute),
	}); err != nil {
		slog.Error("recording feed poll status", "feed_id", feed.ID, "err", err)
	}
}
```

- [ ] **Step 5: Compute the interval in Refresh**

In `Refresh`, capture `cacheMaxAge` from `fetchAndParse`, and at each `recordStatus` call compute the interval. Current interval is `time.Duration(feed.PollIntervalMinutes) * time.Minute`:

- 304 (`errNotModified`): `nextPollInterval(current, false, cacheMaxAge)`.
- fetch/parse/save error: keep backing off gently — `nextPollInterval(current, false, 0)` (no cache hint available on error). Do NOT reset to floor on error.
- success: `hadNewItems := added > 0`; `nextPollInterval(current, hadNewItems, cacheMaxAge)`.

- [ ] **Step 6: Reset on subscribe in Add**

In `Add`, after `CreateFeed`, the new feed defaults to 30m/eligible via the migration; make the initial `recordStatus` explicit with `nextPollInterval(pollFloor, added > 0, cacheMaxAge)` so a freshly subscribed active feed is scheduled correctly. Update `Add`'s `fetchAndParse` call for the new arity and thread `cacheMaxAge`.

- [ ] **Step 7: Switch the poller to the due-query**

- In `service.go`, change `RefreshDue` to drop `olderThan` and use the new query:

```go
func (s *Service) RefreshDue(ctx context.Context) error {
	due, err := s.Store.Queries.ListFeedsDueForPoll(ctx)
	if err != nil {
		return fmt.Errorf("listing due feeds: %w", err)
	}
	for _, feed := range due {
		if _, err := s.Refresh(ctx, feed); err != nil {
			slog.Error("refreshing feed", "feed_id", feed.ID, "err", err)
		}
	}
	return nil
}
```

- In `poll.go`, update the interface and call:

```go
type FeedRefresher interface {
	RefreshDue(ctx context.Context) error
}
// ...
func (w *PollFeedsWorker) Work(ctx context.Context, _ *river.Job[PollFeedsArgs]) error {
	return w.Service.RefreshDue(ctx)
}
```

The `pollInterval` const in `poll.go` is now unused — remove it (the schedule lives in the DB). The periodic-registration cadence in `NewRiverClient` (how often `poll_feeds` is enqueued) is unchanged.

If `ListFeedsDue` (old query) is now unreferenced anywhere (grep `ListFeedsDue\b`), remove it from `feeds.sql` and regenerate; if still referenced, leave it.

- [ ] **Step 8: Run tests + full build**

Run from `apps/api`: `go build ./... && go test ./internal/feeds/ ./internal/jobs/ -p 1 -v`
Expected: PASS. Then `go test ./... -p 1` green (modulo known unrelated flakiness).

- [ ] **Step 9: Commit**

```bash
git add apps/api/internal/feeds/service.go apps/api/internal/jobs/poll.go apps/api/internal/feeds/service_test.go
git commit -m "feat(feeds): adaptive per-feed poll scheduling in service + poller"
```

---

### Task 4: Deploy + verify

- [ ] **Step 1:** Merge to main (PR), then deploy per the standing procedure: rsync a clean `git archive main` copy to the box, `docker compose up -d --build api` (api-only — no web/mobile changes), wait for `/proc/loadavg` < 8 first.
- [ ] **Step 2:** Verify migration 0018 applied: `docker exec open-mind-db-1 psql -U openmind -d openmind -c "\d feeds"` shows `next_poll_at` + `poll_interval_minutes`; existing feeds have sane `next_poll_at` values; API `/healthz` 200 and `geocoder ready` / `provider ready` in logs.
- [ ] **Step 3:** Update `TODO.md` / close issue #11. Commit.
