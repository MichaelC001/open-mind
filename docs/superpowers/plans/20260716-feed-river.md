# Feed River Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Feed-originated items get provenance (`feed_id`) and live in a new Feed page; The Mind and Drift show only your saves plus explicitly **kept** feed items.

**Architecture:** Migration adds `items.feed_id` + `items.kept_at`; the feeds service inserts with provenance; the home/drift queries gain the Mind predicate (`feed_id IS NULL OR kept_at IS NOT NULL`); `PATCH /items/{id}` gains `kept`; `GET /feed` lists the river; manual save of an existing unkept feed URL promotes instead of duplicating; web gets a Feed nav page + Keep affordances + a search-result badge.

**Tech Stack:** sqlc, oapi-codegen, existing feeds service, Next.js.

**Spec:** `docs/superpowers/specs/20260716-feed-river-design.md`

## Global Constraints

- Mind predicate everywhere it applies (home `ListItems`, `ListDriftCandidates`): `(feed_id IS NULL OR kept_at IS NOT NULL)`. Search, export, `ListItemURLs`, desk (pinned), lens runs: UNCHANGED (they cover everything).
- Unsubscribe (`DELETE /feeds/{id}`) first stamps `kept_at = now()` on that feed's unkept items, then deletes the feed (FK `ON DELETE SET NULL`) — items stay visible in The Mind.
- Manual `POST /items {url}` where the URL exists as the user's UNKEPT feed item: stamp `kept_at`, return the existing item `201`-equivalent (existing behaviour for duplicates today is a fresh row? NO — capture currently always inserts; feeds/import dedup by URL but the REST save does not. Constraint: only the feed-item case changes — a URL matching an unkept feed item promotes; anything else keeps today's behaviour exactly).
- `kept` PATCH mirrors the `pinned` mechanics (combinable with `pinned`/`userTags`; none present → 400 unchanged).
- Adding columns to `items` moves nothing (ADD COLUMN appends) but **sqlc must be regenerated** and every `SELECT *`-scanning helper recompiled — `task generate` handles it; never hand-edit.
- Migration number: next free (0016 expected — verify).
- Contract-first; jobs unchanged; no MCP changes v1. Go from `apps/api` (`env -u GOROOT` fallback), DB tests `-p 1`; if rtk garbles test output, re-run via `rtk proxy go test ...`. No banner comments; UK English.

---

### Task 1: Schema + queries + contract (build breaks until Task 2)

**Files:**
- Create: `apps/api/internal/store/migrations/0016_feed_river.sql`
- Modify: `apps/api/internal/store/queries/items.sql`, `apps/api/internal/store/queries/feeds.sql`
- Modify: `openapi.yaml`; `task generate`

**Interfaces → Produces:** sqlc `CreateFeedItem(user_id, url, feed_id)`, `ListFeedItems(user_id, feed_id nullable-filter, limit)`, `SetItemKept(user_id, id, kept_at)` (`:execrows`), `KeepFeedItems(user_id, feed_id)` (`:execrows`, stamps unkept), `GetItemByURL(user_id, url)`; `ListItems` + `ListDriftCandidates` gain the Mind predicate; generated handler methods `GetFeedItems(w, r, params)`; `Item`/`ItemDetail` gain nullable `feedId`+`keptAt`; PATCH request gains optional `kept`.

- [ ] **Step 1: Migration**

```sql
ALTER TABLE items ADD COLUMN feed_id uuid REFERENCES feeds(id) ON DELETE SET NULL;
ALTER TABLE items ADD COLUMN kept_at timestamptz;
CREATE INDEX items_feed_idx ON items (user_id, feed_id) WHERE feed_id IS NOT NULL;
```

- [ ] **Step 2: Queries**

`items.sql` — change `ListItems` to:

```sql
-- name: ListItems :many
SELECT * FROM items
WHERE user_id = $1 AND (feed_id IS NULL OR kept_at IS NOT NULL)
ORDER BY created_at DESC LIMIT $2;
```

`ListDriftCandidates` gains ` AND (feed_id IS NULL OR kept_at IS NOT NULL)` in its WHERE. Add:

```sql
-- name: CreateFeedItem :one
INSERT INTO items (user_id, url, feed_id) VALUES ($1, $2, $3) RETURNING *;

-- name: ListFeedItems :many
SELECT * FROM items
WHERE user_id = $1 AND feed_id IS NOT NULL
  AND (sqlc.narg(filter_feed_id)::uuid IS NULL OR feed_id = sqlc.narg(filter_feed_id))
ORDER BY created_at DESC LIMIT sqlc.arg(limit_count);

-- name: SetItemKept :execrows
UPDATE items SET kept_at = $3, updated_at = now() WHERE user_id = $1 AND id = $2;

-- name: GetItemByURL :one
SELECT * FROM items WHERE user_id = $1 AND url = $2 LIMIT 1;
```

`feeds.sql` add:

```sql
-- name: KeepFeedItems :execrows
-- Unsubscribing keeps the feed's items in the library: stamp everything
-- still unkept so the Mind predicate keeps showing them once feed_id nulls.
UPDATE items SET kept_at = now(), updated_at = now()
WHERE user_id = $1 AND feed_id = $2 AND kept_at IS NULL;
```

- [ ] **Step 3: Contract** — `Item`/`ItemDetail`: nullable `feedId` (uuid) + `keptAt` (date-time), following `pinnedAt`'s exact style. The items-PATCH request schema gains optional `kept: boolean`. New path (match `/desk`'s style):

```yaml
  /feed:
    get:
      operationId: getFeedItems
      parameters:
        - {name: limit, in: query, schema: {type: integer}}
        - {name: feedId, in: query, schema: {type: string, format: uuid}}
      responses:
        '200':
          description: feed-originated items, newest first
          content:
            application/json:
              schema: {type: array, items: {$ref: '#/components/schemas/Item'}}
```

- [ ] **Step 4:** `task generate` → record the expected missing-`GetFeedItems` build error. **Step 5: Commit** `feat(feed-river): schema, queries, contract (handlers next)` (add exactly what changed).

---

### Task 2: API handlers + predicates + promote-on-save (restores build)

**Files:**
- Create: `apps/api/internal/api/feedriver.go` (GetFeedItems handler)
- Modify: `apps/api/internal/api/items_patch.go` (kept), `apps/api/internal/api/server.go` (capture: promote-on-duplicate), `apps/api/internal/api/feeds.go` (unsubscribe stamps first), `toAPIItem` mapper (feedId/keptAt — find it: `grep -n "func toAPIItem" internal/api`), `ratelimit.go` guarded() (`GET /feed` NOT guarded — read path, like /desk… CHECK: /desk IS guarded today; mirror /desk exactly and add `(method == http.MethodGet && path == "/feed") ||`).
- Test: `apps/api/internal/api/feedriver_test.go`, extend `items_patch_test.go`, `feeds_test.go`, `server_test.go` (or wherever POST /items is tested), `ratelimit_internal_test.go`.

**Interfaces:** consumes Task 1's sqlc names exactly; produces a repo that builds.

- [ ] **Step 1: Failing tests** (DB-backed; create feed rows via the store directly or the feeds service test helpers — read feeds_test.go first):

`TestFeedItemsListing` (two feeds × items → GET /feed newest-first; feedId filter; another user sees none; limit cap 200/default 50 — implement caps in handler like defaultListLimit conventions);
`TestFeedItemsExcludedFromHome` (unkept feed item absent from GET /items; kept present);
`TestPatchKept` (kept:true → keptAt set + item appears in home; kept:false clears; combinable `{"kept":true,"pinned":true}`; no-field 400 unchanged);
`TestSaveExistingFeedURLPromotes` (seed unkept feed item with url U → POST /items {url:U} → response id == existing id, keptAt set, item count unchanged; POST /items with a NEW url → fresh row as today; POST /items {url} matching a KEPT feed item → today's behaviour (fresh row) — assert explicitly);
`TestUnsubscribeKeepsItems` (feed with 2 unkept + 1 kept item → DELETE /feeds/{id} → all 3 have keptAt set (2 stamped now, 1 original), feed_id nulled by FK, all appear in home);
`TestDriftExcludesUnkeptFeedItems` (enriched unkept feed item not a drift candidate; kept one is);
guarded() case for GET /feed.

- [ ] **Step 2:** compile-FAIL run. **Step 3: Implement**

`feedriver.go`: mirror `GetDesk`'s shape; parse limit (default 50, max 200) + optional feedId → `ListFeedItems` with `pgtype.UUID`-style narg (check generated param type); map via `toAPIItem`.
`items_patch.go`: `kept` handling mirrors `pinned` (`SetItemKept` with now()/NULL, rows==0 → 404); update the ≥1-field validation to include kept.
`server.go` capture(): before CreateItem on the URL path, `GetItemByURL`; if found AND `FeedID` valid AND `KeptAt` invalid → `SetItemKept(now)` + return that item (no enrichment enqueue — it's already enriched/pending via the feed). Any other found-case falls through to today's insert. Comment the why (feed promotion).
`feeds.go` DeleteFeed: call `KeepFeedItems` before the delete, log stamped count.
`toAPIItem`: map `FeedId`/`KeptAt` like `PinnedAt` (nullable).

- [ ] **Step 4:** full `go test -p 1 ./internal/api/` + vet/build clean (repo builds again). **Step 5: Commit** `feat(feed-river): feed endpoint, kept PATCH, promote-on-save, unsubscribe keep`

---

### Task 3: Feeds service provenance

**Files:**
- Modify: `apps/api/internal/feeds/service.go` (`saveEntries` gains the feed id → `CreateFeedItem`; `Add` passes the created feed's id — NOTE Add currently backfills BEFORE CreateFeed persists the row (deliberate ordering per its comment). Resolve: keep capture-order semantics by restructuring minimally — create the feed row first inside Add ONLY IF that doesn't break its documented retry-clean property… it does. Instead: `saveEntries` accepts `feedID *uuid.UUID`; `Add`'s pre-persist backfill passes nil (items created without provenance would leak into the Mind!) — so Add must stamp them after CreateFeed succeeds: after persisting the feed, run a new `AdoptFeedItems` query assigning `feed_id` to the just-backfilled item ids (collect ids from saveEntries' return — extend it to return []uuid). Failure to adopt → log, items stay Mind-visible (harmless, self-corrects nothing — acceptable, logged). `Refresh` passes the real feed id directly.)
- Add query `apps/api/internal/store/queries/items.sql`:

```sql
-- name: AdoptFeedItems :execrows
UPDATE items SET feed_id = $3 WHERE user_id = $1 AND id = ANY($2::uuid[]) AND feed_id IS NULL;
```

- Test: `apps/api/internal/feeds/service_test.go` — extend the existing Add/Refresh tests: after Add, backfilled items carry the feed's id; after a poll (Refresh), new items carry it; deleted feed → items keep working (FK SET NULL covered in Task 2's test).

- [ ] Steps: failing tests → implement → `go test -p 1 ./internal/feeds/ ./internal/store/` + full suite → commit `feat(feeds): item provenance (feed_id) on backfill and polls`.

---

### Task 4: Web — Feed page, nav, Keep, badge

**Files:**
- Create: `apps/web/app/feed/page.tsx` (server component fetching via cookie like `/desk` — read `app/desk/page.tsx` first and mirror), `apps/web/components/FeedRiver.tsx` (client: rows with feed-title kicker (needs the feeds list — fetch `/api/feeds` client-side for chips + id→title map), title→detail link, summary line, relative time via `apps/web/lib/relative-time.ts`, Keep/Kept toggle per row → PATCH proxy), `apps/web/app/api/feed/route.ts` (GET proxy w/ 502 wrapper — copy the desk proxy + add the wrapper if it lacks one).
- Modify: sidebar nav (find the Desk/Drift links: `grep -rn '"/desk"' apps/web`) — add **Feed** between The Mind and Desk; search-result cards show a small mono "via <feed>" badge when `feedId` set (grep where cards render `pinnedAt`/meta rows — keep it subtle, tokens only); detail-page rail shows "From <feed title> · kept <relative>|not kept" line + Keep toggle when `feedId` set.
- Empty state: no feed items → line + link to `/feeds` ("Subscribe to something worth reading").

- [ ] Steps: implement → `pnpm turbo run build --filter=web` + tsc clean → commit `feat(web): feed river page, keep affordances, provenance badge`.

---

### Task 5: E2e + docs + prod cleanup script

- [ ] **Step 1: Compose e2e** (python3 urllib; rebuild api+web): subscribe the HN fixture (`news.ycombinator.com/rss`) → items appear in `GET /feed`, NOT in `GET /items`; keep one via PATCH → appears in `GET /items`; unkeep → gone; POST /items with an unkept feed item's URL → promotes (same id, keptAt set); DELETE the feed → its items now all in `GET /items` with keptAt; drift excludes unkept (enriched feed item not in GET /drift). Unsubscribe cleanup at the end.
- [ ] **Step 2: Docs** — self-hosting Feeds section: provenance, the Feed page, Keep semantics, unsubscribe-keeps-items, promote-on-save.
- [ ] **Step 3: Prod cleanup script** — write `scripts/one-off/20260716-feedriver-adopt.sql` (tracked, commented as one-off): for the pre-provenance backfill, ADOPT rather than delete (better than the spec's delete-and-repoll — zero data loss): `UPDATE items i SET feed_id = f.id FROM feeds f WHERE i.user_id = f.user_id AND i.feed_id IS NULL AND i.kept_at IS NULL AND i.created_at >= f.created_at - interval '5 minutes' AND position(regexp_replace(f.site_url, '^https?://(www\.)?', '') in i.url) > 0 AND f.site_url <> '';` — plus a SELECT preview variant above it. The controller runs the SELECT on prod, shows the user the count, and applies only on explicit confirmation at deploy time. (If site_url is empty for some feeds, note which in the preview.)
- [ ] **Step 4:** TODO/commit `docs: feed river — self-hosting, adopt script`.
