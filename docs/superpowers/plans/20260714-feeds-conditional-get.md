# Feeds Conditional GET Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Feed polls send `If-None-Match`/`If-Modified-Since` and treat a 304 as a healthy no-op, so unchanged feeds skip the body download and re-parse.

**Architecture:** Migration adds `etag`/`last_modified` columns to `feeds`; `internal/feeds.Service.fetchAndParse` sends the stored validators and captures fresh ones from 200 responses; `Refresh`/`Add` persist validators alongside the poll status. No API/contract change.

**Tech Stack:** Go stdlib HTTP, sqlc (regenerate), existing `httptest`-based feeds test harness.

**Spec:** `docs/superpowers/specs/20260714-feeds-conditional-get-design.md`

## Global Constraints

- Validators are opaque strings from `ETag`/`Last-Modified` response headers — stored verbatim, never parsed/compared locally; empty string = no validator.
- 304 → status `ok`, `last_polled_at` stamped, stored validators untouched, no body read/parse, `(0, nil)` return.
- Fetch/parse errors preserve stored validators.
- No `openapi.yaml` change; the new columns are not exposed on the Feed API type.
- sqlc regenerated via `task generate` (or `sqlc generate` from `apps/api` if the task runner misbehaves); never hand-edit `internal/store/db`.
- Go commands from `apps/api`; DB-backed tests need `docker compose up -d db` and `-p 1`.
- No banner-style comment blocks.

---

### Task 1: Migration + service conditional-GET + tests

**Files:**
- Create: `apps/api/internal/store/migrations/0012_feed_validators.sql`
- Modify: `apps/api/internal/store/queries/feeds.sql` (SetFeedPolled gains validator params)
- Regenerate: `apps/api/internal/store/db/*` (sqlc)
- Modify: `apps/api/internal/feeds/service.go`
- Test: `apps/api/internal/feeds/service_test.go`

**Interfaces:**
- Consumes: existing `Service` (`Store`, `HTTPClient`, `River`), `fetchAndParse`, `Refresh`, `Add`, `recordStatus`, test harness `testService`/`rssServer`/`newUser` (tests set `s.HTTPClient = srv.Client()`).
- Produces: `fetchAndParse(ctx, feedURL, etag, lastModified string) (Feed, string, string, error)` (parsed feed + fresh validators); package sentinel `errNotModified`; `recordStatus(ctx, feed, status, etag, lastModified string)`.

- [ ] **Step 1: Write the migration and query change**

`apps/api/internal/store/migrations/0012_feed_validators.sql`:

```sql
-- Conditional-GET validators for feed polling. Opaque header values from the
-- feed server (ETag / Last-Modified), sent back verbatim on the next poll;
-- empty means no validator known.
ALTER TABLE feeds ADD COLUMN etag text NOT NULL DEFAULT '';
ALTER TABLE feeds ADD COLUMN last_modified text NOT NULL DEFAULT '';
```

In `apps/api/internal/store/queries/feeds.sql`, replace `SetFeedPolled`:

```sql
-- name: SetFeedPolled :exec
UPDATE feeds SET last_polled_at = $3, last_status = $4, etag = $5, last_modified = $6
WHERE user_id = $1 AND id = $2;
```

Regenerate: `task generate` from the repo root (falls back: `sqlc generate` from `apps/api`). Expect `db.Feed` to gain `Etag`/`LastModified` and `SetFeedPolledParams` the two new fields; compile will now break at the `SetFeedPolled` call sites — fixed in Step 3.

- [ ] **Step 2: Write the failing tests**

Append to `apps/api/internal/feeds/service_test.go`:

```go
// conditionalServer serves an RSS document with ETag/Last-Modified validators
// and honours If-None-Match/If-Modified-Since with a 304 whose body is
// deliberately garbage — parsing it would fail, which is how the tests prove a
// 304 short-circuits before the parser.
func conditionalServer(t *testing.T, etag, lastModified string, entries *atomic.Pointer[[]string], gotConditional *atomic.Bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == etag && etag != "" {
			gotConditional.Store(true)
			w.WriteHeader(http.StatusNotModified)
			_, _ = w.Write([]byte("<<<garbage — must never be parsed>>>"))
			return
		}
		if etag != "" {
			w.Header().Set("ETag", etag)
		}
		if lastModified != "" {
			w.Header().Set("Last-Modified", lastModified)
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		var b strings.Builder
		b.WriteString(`<?xml version="1.0"?><rss version="2.0"><channel>`)
		b.WriteString(`<title>Cond Feed</title><link>https://example.com</link>`)
		for i, u := range *entries.Load() {
			fmt.Fprintf(&b, `<item><title>E%d</title><link>%s</link></item>`, i, u)
		}
		b.WriteString(`</channel></rss>`)
		_, _ = w.Write([]byte(b.String()))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFeedAddStoresValidators(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	uid := newUser(t, s)
	var entries atomic.Pointer[[]string]
	entries.Store(&[]string{"https://example.com/v1"})
	var got atomic.Bool
	srv := conditionalServer(t, `"v1"`, "Mon, 13 Jul 2026 00:00:00 GMT", &entries, &got)
	s.HTTPClient = srv.Client()

	feed, _, err := s.Add(ctx, uid, srv.URL)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if feed.Etag != `"v1"` || feed.LastModified != "Mon, 13 Jul 2026 00:00:00 GMT" {
		t.Errorf("validators = %q / %q, want stored from response", feed.Etag, feed.LastModified)
	}
}

func TestFeedRefresh304SkipsParseAndKeepsValidators(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	uid := newUser(t, s)
	var entries atomic.Pointer[[]string]
	entries.Store(&[]string{"https://example.com/c1"})
	var got atomic.Bool
	srv := conditionalServer(t, `"same"`, "", &entries, &got)
	s.HTTPClient = srv.Client()

	feed, _, err := s.Add(ctx, uid, srv.URL)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	itemsBefore := countEnrichJobs(t, s)

	added, err := s.Refresh(ctx, feed)
	if err != nil || added != 0 {
		t.Fatalf("refresh: added=%d err=%v, want 0/nil", added, err)
	}
	if !got.Load() {
		t.Fatal("second poll did not send If-None-Match")
	}
	refetched, err := s.Store.Queries.GetFeed(ctx, db.GetFeedParams{UserID: uid, ID: feed.ID})
	if err != nil {
		t.Fatalf("get feed: %v", err)
	}
	if refetched.LastStatus != "ok" {
		t.Errorf("status = %q, want ok (304 is healthy)", refetched.LastStatus)
	}
	if !refetched.LastPolledAt.Valid || !refetched.LastPolledAt.Time.After(feed.LastPolledAt.Time) {
		t.Error("last_polled_at not stamped on 304")
	}
	if refetched.Etag != `"same"` {
		t.Errorf("etag = %q, want preserved", refetched.Etag)
	}
	if countEnrichJobs(t, s) != itemsBefore {
		t.Error("304 refresh must create no items/jobs")
	}
}

func TestFeedRefreshChangedContentUpdatesValidators(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	uid := newUser(t, s)
	var entries atomic.Pointer[[]string]
	entries.Store(&[]string{"https://example.com/n1"})
	// Server never matches If-None-Match (etag arg differs per request cycle):
	// simulate by using a server whose etag is "v2" while the stored one is
	// different — the conditional never matches, so every poll is a 200.
	var got atomic.Bool
	srv := conditionalServer(t, `"v2"`, "", &entries, &got)
	s.HTTPClient = srv.Client()

	feed, _, err := s.Add(ctx, uid, srv.URL)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	// Force a stale stored validator, then refresh: 200 path must overwrite it.
	if err := s.Store.Queries.SetFeedPolled(ctx, db.SetFeedPolledParams{
		UserID: uid, ID: feed.ID, LastPolledAt: feed.LastPolledAt, LastStatus: "ok",
		Etag: `"stale"`, LastModified: "",
	}); err != nil {
		t.Fatalf("stale validators: %v", err)
	}
	feed.Etag = `"stale"`

	entries.Store(&[]string{"https://example.com/n1", "https://example.com/n2"})
	added, err := s.Refresh(ctx, feed)
	if err != nil || added != 1 {
		t.Fatalf("refresh: added=%d err=%v, want 1/nil", added, err)
	}
	refetched, err := s.Store.Queries.GetFeed(ctx, db.GetFeedParams{UserID: uid, ID: feed.ID})
	if err != nil {
		t.Fatalf("get feed: %v", err)
	}
	if refetched.Etag != `"v2"` {
		t.Errorf("etag = %q, want refreshed to v2", refetched.Etag)
	}
}

func TestFeedNoValidatorServerStillWorks(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	uid := newUser(t, s)
	var entries atomic.Pointer[[]string]
	entries.Store(&[]string{"https://example.com/p1"})
	srv := rssServer(t, &entries) // plain server: no ETag/Last-Modified, no 304
	s.HTTPClient = srv.Client()

	feed, _, err := s.Add(ctx, uid, srv.URL)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if feed.Etag != "" || feed.LastModified != "" {
		t.Errorf("validators = %q/%q, want empty", feed.Etag, feed.LastModified)
	}
	if added, err := s.Refresh(ctx, feed); err != nil || added != 0 {
		t.Fatalf("refresh: %d/%v, want 0/nil", added, err)
	}
}

func TestFeedFetchErrorPreservesValidators(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	uid := newUser(t, s)
	var entries atomic.Pointer[[]string]
	entries.Store(&[]string{"https://example.com/e1"})
	var got atomic.Bool
	srv := conditionalServer(t, `"keep"`, "", &entries, &got)
	s.HTTPClient = srv.Client()

	feed, _, err := s.Add(ctx, uid, srv.URL)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	srv.Close() // next poll fails at transport level

	if added, err := s.Refresh(ctx, feed); err != nil || added != 0 {
		t.Fatalf("refresh after close: %d/%v, want 0/nil (error recorded, not returned)", added, err)
	}
	refetched, err := s.Store.Queries.GetFeed(ctx, db.GetFeedParams{UserID: uid, ID: feed.ID})
	if err != nil {
		t.Fatalf("get feed: %v", err)
	}
	if !strings.HasPrefix(refetched.LastStatus, "error:") {
		t.Errorf("status = %q, want error:…", refetched.LastStatus)
	}
	if refetched.Etag != `"keep"` {
		t.Errorf("etag = %q, want preserved through error", refetched.Etag)
	}
}
```

Add `"sync/atomic"` to the imports if not present (it is — `atomic.Pointer` is already used; `atomic.Bool` comes with it).

- [ ] **Step 3: Run tests to verify they fail**

Run (from `apps/api`, with `docker compose up -d db` from the repo root): `go test -p 1 ./internal/feeds/ 2>&1 | head -10`
Expected: compile FAIL — `SetFeedPolledParams` missing new fields at old call sites, `db.Feed` lacking `Etag` until Step 1's regen is applied, `conditionalServer` referencing behaviours not yet implemented. (If Step 1 was completed, failure shifts to the service call sites not compiling — either way it must not pass.)

- [ ] **Step 4: Implement the service changes**

In `apps/api/internal/feeds/service.go`:

Add the sentinel near `ErrAlreadySubscribed`:

```go
// errNotModified signals a 304 from a conditional poll: the feed body was
// neither downloaded nor parsed; there is nothing new by definition.
var errNotModified = errors.New("feed not modified")
```

Change `fetchAndParse` to take and return validators:

```go
// fetchAndParse fetches feedURL with the SSRF-safe client and parses the body.
// When etag/lastModified are non-empty they are sent as If-None-Match /
// If-Modified-Since; a 304 returns errNotModified without reading the body.
// On success it also returns the response's validators (empty when absent).
func (s *Service) fetchAndParse(ctx context.Context, feedURL, etag, lastModified string) (Feed, string, string, error) {
	client := s.HTTPClient
	if client == nil {
		client = enrich.SafeHTTPClient(fetchTimeout)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return Feed{}, "", "", fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml;q=0.9, */*;q=0.8")
	req.Header.Set("User-Agent", "openmind-feeds/1.0")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}

	resp, err := client.Do(req)
	if err != nil {
		return Feed{}, "", "", fmt.Errorf("requesting feed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified {
		return Feed{}, "", "", errNotModified
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Feed{}, "", "", fmt.Errorf("feed returned status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedBytes))
	if err != nil {
		return Feed{}, "", "", fmt.Errorf("reading feed body: %w", err)
	}
	parsed, err := Parse(data, feedURL)
	if err != nil {
		return Feed{}, "", "", fmt.Errorf("parsing feed: %w", err)
	}
	return parsed, resp.Header.Get("ETag"), resp.Header.Get("Last-Modified"), nil
}
```

Change `recordStatus` to carry validators:

```go
// recordStatus stamps last_polled_at (now), last_status, and the conditional-GET
// validators on a feed. A write failure is logged, not returned — the poll loop
// must keep going.
func (s *Service) recordStatus(ctx context.Context, feed db.Feed, status, etag, lastModified string) {
	if err := s.Store.Queries.SetFeedPolled(ctx, db.SetFeedPolledParams{
		UserID: feed.UserID, ID: feed.ID, LastPolledAt: nowTS(), LastStatus: status,
		Etag: etag, LastModified: lastModified,
	}); err != nil {
		slog.Error("recording feed poll status", "feed_id", feed.ID, "err", err)
	}
}
```

Update `Refresh` (error paths pass the feed's EXISTING validators so they are preserved; the 304 path likewise):

```go
func (s *Service) Refresh(ctx context.Context, feed db.Feed) (int, error) {
	parsed, etag, lastModified, err := s.fetchAndParse(ctx, feed.Url, feed.Etag, feed.LastModified)
	if errors.Is(err, errNotModified) {
		// Healthy no-op: the server confirmed nothing changed, so skip parsing
		// and item creation entirely, keeping the stored validators.
		s.recordStatus(ctx, feed, "ok", feed.Etag, feed.LastModified)
		return 0, nil
	}
	if err != nil {
		s.recordStatus(ctx, feed, "error: "+shortErr(err), feed.Etag, feed.LastModified)
		slog.Warn("feed refresh failed", "feed_id", feed.ID, "url", feed.Url, "err", err)
		return 0, nil
	}
	added, err := s.saveEntries(ctx, feed.UserID, parsed.Entries)
	if err != nil {
		s.recordStatus(ctx, feed, "error: "+shortErr(err), feed.Etag, feed.LastModified)
		slog.Warn("feed refresh failed", "feed_id", feed.ID, "url", feed.Url, "err", err)
		return 0, nil
	}
	s.recordStatus(ctx, feed, "ok", etag, lastModified)
	return added, nil
}
```

Update `Add`'s fetch call to `s.fetchAndParse(ctx, feedURL, "", "")` (no stored validators yet), keep its existing flow, and where it currently records/returns the persisted feed with `polled`/`"ok"`, persist the fresh validators too — `Add` calls `SetFeedPolled` (or sets fields at insert) via the same `recordStatus(ctx, feed, "ok", etag, lastModified)` helper after `CreateFeed`, then sets `feed.Etag, feed.LastModified = etag, lastModified` alongside the existing `feed.LastPolledAt, feed.LastStatus = polled, "ok"` line so the returned value matches the DB. Read the current `Add` body (service.go:70-114) and keep its ordering (backfill before CreateFeed) intact.

- [ ] **Step 5: Run the tests**

Run: `go test -p 1 ./internal/feeds/ -v 2>&1 | tail -12`
Expected: all PASS (5 new + 4 existing).

- [ ] **Step 6: Full suite, vet, build, commit**

```bash
go test -p 1 ./... 2>&1 | tail -3   # sqlc regen touches db package — full check
go vet ./... && go build ./...
git add internal/store/migrations/0012_feed_validators.sql internal/store/queries/feeds.sql internal/store/db internal/feeds
git commit -m "feat(feeds): conditional GET — etag/last-modified validators, 304 skips re-parse"
```

---

### Task 2: Compose smoke + docs + TODO

**Files:**
- Modify: `docs/self-hosting.md` (Feeds section, one paragraph)
- Modify: `TODO.md`

- [ ] **Step 1: Compose smoke**

`docker compose up -d --build api` (repo root; db/web already up — check `docker ps` first). Subscribe to a feed that serves validators and confirm two polls: use python3 urllib (NOT curl) against `localhost:8080`:

1. `POST /feeds {"url": "https://hnrss.org/frontpage"}` (hnrss serves ETag) → 201.
2. `psql` into the compose db (`docker compose exec -T db psql -U openmind -c "SELECT etag, last_modified, last_status FROM feeds"`) → non-empty `etag`, status `ok`.
3. Force a re-poll: `docker compose exec -T db psql -U openmind -c "UPDATE feeds SET last_polled_at = now() - interval '1 hour'"`, then wait for the periodic job or trigger via a second manual `Refresh` path — simplest: restart the api container (`docker compose restart api`; `poll_feeds` has RunOnStart) and re-query: `last_polled_at` advanced, `last_status` still `ok`, item count unchanged (SELECT count(*) FROM items).
4. `DELETE /feeds/{id}` to clean up.

If hnrss omits validators that day, any feed with `ETag` works (e.g. `https://github.blog/feed/`); if none available, rely on the unit tests (they are the authority for the 304 path) and note it.

- [ ] **Step 2: Docs**

In `docs/self-hosting.md`'s Feeds section, append:

```markdown
Feed polls use HTTP conditional requests: the poller stores each feed's `ETag`/`Last-Modified` and sends `If-None-Match`/`If-Modified-Since` on the next poll, so an unchanged feed answers with a cheap `304` and no re-parsing happens. Servers that don't support validators simply always return the full feed — behaviour is unchanged.
```

- [ ] **Step 3: TODO + commit**

Move the feeds conditional-GET line from Next to a dated Done entry (under a "### Milestone 2 — feeds conditional GET" heading above the MCP stdio Done entry), with the verification evidence. Next becomes empty — note that in the entry.

```bash
git add docs/self-hosting.md TODO.md
git commit -m "docs: feeds conditional GET shipped — self-hosting note, TODO"
```
