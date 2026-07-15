# Highlights → Quote Cards Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Select text in the reader → it becomes a painted in-article highlight AND a first-class quote card, linked bidirectionally to the source.

**Architecture:** New `highlights` table (migration `0014`) with W3C-style text-quote anchors; three contract-first endpoints (`POST/GET /items/{id}/highlights`, `DELETE /highlights/{id}`); transactional create (quote item + highlight row + link in one tx via sqlc `WithTx`); web reader gains a selection→highlight flow and anchor-based painting via a pure, unit-tested matcher.

**Tech Stack:** Go + sqlc + oapi-codegen, Next.js reader page, vitest for the anchor matcher.

**Spec:** `docs/superpowers/specs/20260715-highlights-design.md`

## Global Constraints

- Contract-first: all three endpoints in `openapi.yaml`, then `task generate`; never hand-edit generated code.
- Capture is sacred: enrichment enqueue for the quote item is best-effort AFTER the tx commits; a failed enqueue is logged, never an error.
- Caps: `exact` ≤ 2000 runes (400 over), `prefix`/`suffix` truncated server-side to 64 runes each; `exact` must be non-empty after trim (400).
- Unknown/cross-tenant ids → 404 everywhere (indistinguishable from missing).
- Transactionality: quote item + highlight row + link row all-or-nothing.
- No AI in the request path; fully functional under `noop`.
- Migration number is `0014_highlights.sql` (0013 is taken); verify the next free number before writing it.
- Go commands from `apps/api` (`env -u GOROOT /opt/homebrew/bin/go` fallback); DB tests need `docker compose up -d db` and `-p 1`.
- No banner-style comment blocks. UK English in docs/copy.

---

### Task 1: Schema + contract + store queries

**Files:**
- Create: `apps/api/internal/store/migrations/0014_highlights.sql`
- Create: `apps/api/internal/store/queries/highlights.sql`
- Modify: `apps/api/internal/store/queries/items.sql` (add `CreateQuoteItem`)
- Modify: `openapi.yaml`
- Regenerate: `task generate` (Go server, TS client, sqlc)

**Interfaces:**
- Consumes: existing schema/codegen toolchain.
- Produces (Task 2 relies on these exact names): sqlc methods `CreateHighlight`, `ListHighlightsBySource`, `GetHighlight`, `DeleteHighlight`, `CreateQuoteItem`; generated handler-interface methods `CreateItemHighlight(w, r, id)`, `ListItemHighlights(w, r, id)`, `DeleteHighlight(w, r, id)`; API types `Highlight`, `CreateHighlightRequest`, `CreateHighlightResponse`.

- [ ] **Step 1: Migration**

`apps/api/internal/store/migrations/0014_highlights.sql` — exactly the spec's SQL:

```sql
CREATE TABLE highlights (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id),
    source_item_id uuid NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    quote_item_id uuid NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    exact text NOT NULL,
    prefix text NOT NULL DEFAULT '',
    suffix text NOT NULL DEFAULT '',
    offset_hint int NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX highlights_source_idx ON highlights (user_id, source_item_id);
```

- [ ] **Step 2: Store queries**

`apps/api/internal/store/queries/highlights.sql`:

```sql
-- name: CreateHighlight :one
INSERT INTO highlights (user_id, source_item_id, quote_item_id, exact, prefix, suffix, offset_hint)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *;

-- name: ListHighlightsBySource :many
SELECT * FROM highlights WHERE user_id = $1 AND source_item_id = $2 ORDER BY created_at ASC;

-- name: GetHighlight :one
SELECT * FROM highlights WHERE user_id = $1 AND id = $2;

-- name: DeleteHighlight :execrows
DELETE FROM highlights WHERE user_id = $1 AND id = $2;
```

Append to `apps/api/internal/store/queries/items.sql`:

```sql
-- name: CreateQuoteItem :one
INSERT INTO items (user_id, body, card_type) VALUES ($1, $2, 'quote') RETURNING *;
```

- [ ] **Step 3: Contract**

In `openapi.yaml`, following the existing `/items/{id}/links` block's style:

```yaml
  /items/{id}/highlights:
    post:
      operationId: createItemHighlight
      parameters:
        - {name: id, in: path, required: true, schema: {type: string, format: uuid}}
      requestBody:
        required: true
        content:
          application/json:
            schema: {$ref: '#/components/schemas/CreateHighlightRequest'}
      responses:
        '201':
          description: Highlight created, with its mirrored quote card.
          content:
            application/json:
              schema: {$ref: '#/components/schemas/CreateHighlightResponse'}
        '400': {description: Invalid request (empty exact, over caps).}
        '404': {description: Unknown item.}
    get:
      operationId: listItemHighlights
      parameters:
        - {name: id, in: path, required: true, schema: {type: string, format: uuid}}
      responses:
        '200':
          description: Highlights anchored to this item, oldest first.
          content:
            application/json:
              schema: {type: array, items: {$ref: '#/components/schemas/Highlight'}}
        '404': {description: Unknown item.}
  /highlights/{id}:
    delete:
      operationId: deleteHighlight
      parameters:
        - {name: id, in: path, required: true, schema: {type: string, format: uuid}}
      responses:
        '204': {description: Highlight and its quote card removed.}
        '404': {description: Unknown highlight.}
```

Schemas (components):

```yaml
    Highlight:
      type: object
      required: [id, sourceItemId, quoteItemId, exact, prefix, suffix, offsetHint, createdAt]
      properties:
        id: {type: string, format: uuid}
        sourceItemId: {type: string, format: uuid}
        quoteItemId: {type: string, format: uuid}
        exact: {type: string}
        prefix: {type: string}
        suffix: {type: string}
        offsetHint: {type: integer}
        createdAt: {type: string, format: date-time}
    CreateHighlightRequest:
      type: object
      required: [exact]
      properties:
        exact: {type: string}
        prefix: {type: string}
        suffix: {type: string}
        offsetHint: {type: integer}
    CreateHighlightResponse:
      type: object
      required: [highlight, quoteItem]
      properties:
        highlight: {$ref: '#/components/schemas/Highlight'}
        quoteItem: {$ref: '#/components/schemas/Item'}
```

Match the file's existing indentation/style conventions exactly (read a neighbouring path first).

- [ ] **Step 4: Regenerate and confirm the expected breakage**

Run from repo root: `task generate`
Expected: sqlc + oapi-codegen succeed; `go build ./...` from `apps/api` now FAILS with "*Server does not implement ServerInterface (missing CreateItemHighlight...)" — that is the contract for Task 2. Record the exact error.

- [ ] **Step 5: Commit**

```bash
git add apps/api/internal/store openapi.yaml packages/api-client apps/api/internal/api/gen.go
git commit -m "feat(highlights): schema, contract, store queries (handlers in next commit)"
```

(If the repo convention is that generated `gen.go` lives elsewhere, `git status` after generate and add exactly what changed.)

---

### Task 2: Handlers (transactional create, list, delete) + rate-limit guard

**Files:**
- Create: `apps/api/internal/api/highlights.go`
- Modify: `apps/api/internal/api/ratelimit.go` (guarded() gains the write endpoints)
- Test: `apps/api/internal/api/highlights_test.go` (DB-backed)

**Interfaces:**
- Consumes: Task 1's sqlc methods (`CreateHighlight`, `ListHighlightsBySource`, `GetHighlight`, `DeleteHighlight`, `CreateQuoteItem`, existing `CreateLink`, `DeleteItem`); helpers `ownsItem`, `canonicalPair` (links.go), `userID`, `writeJSON`, `writeError`, `maxBodyBytes`; `s.store.Pool.Begin` + `s.store.Queries.WithTx(tx)`; `jobs.EnrichArgs`.
- Produces: the three ServerInterface methods; repo builds again.

- [ ] **Step 1: Write the failing DB-backed tests**

`apps/api/internal/api/highlights_test.go`, following the fixture pattern used by `links_test.go` (read it first; reuse its server/store test harness):

```go
func TestCreateHighlightCreatesQuoteAndLink(t *testing.T)
// seed an enriched article item; POST /items/{id}/highlights {"exact":"the selected text","prefix":"before ","suffix":" after","offsetHint":42}
// → 201; response.highlight.exact == "the selected text"; response.quoteItem.cardType == "quote", body == exact, status "pending";
// GET /items/{id}/links (or ListLinkedItems query) shows the quote item; highlights row exists with offset_hint 42.

func TestCreateHighlightValidation(t *testing.T)
// empty/whitespace exact → 400; exact of 2001 runes → 400; prefix of 200 runes → 201 but stored prefix is 64 runes (truncated, not rejected).

func TestCreateHighlightUnknownItem(t *testing.T)
// random uuid → 404; another user's item → 404.

func TestListItemHighlights(t *testing.T)
// two highlights on one article → GET returns both oldest-first; unknown item → 404; other user sees 404 not empty list.

func TestDeleteHighlightRemovesQuote(t *testing.T)
// create → DELETE /highlights/{id} → 204; highlights row gone; quote ITEM gone (handler deletes the quote item; cascade clears the row); link gone (items cascade); re-delete → 404; cross-tenant delete → 404 and nothing deleted.

func TestDeleteQuoteItemCascadesHighlight(t *testing.T)
// create highlight; DELETE the quote item via the normal items DELETE → highlights row gone (FK cascade); source article unaffected.

func TestTwoIdenticalHighlightsAllowed(t *testing.T)
// same POST twice → two distinct highlight rows + two quote items (by design).
```

Write full bodies with real HTTP requests against the test server, per the file conventions in `links_test.go`.

- [ ] **Step 2: Run to verify failure**

Run: `go test -p 1 ./internal/api/ -run TestCreateHighlight -v 2>&1 | head -5`
Expected: compile FAIL (handlers missing — repo currently doesn't build, from Task 1).

- [ ] **Step 3: Implement `apps/api/internal/api/highlights.go`**

```go
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/rohithgilla12/openmind/api/internal/jobs"
	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

const (
	maxHighlightExactRunes   = 2000
	maxHighlightContextRunes = 64
)

// truncRunes caps s at n runes without splitting a rune.
func truncRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

// CreateItemHighlight saves a text selection on an item as a highlight plus a
// mirrored quote card, linked to the source — all in one transaction, so a
// failure anywhere leaves nothing behind. Enrichment for the quote card is
// enqueued best-effort after commit (capture is sacred).
func (s *Server) CreateItemHighlight(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req CreateHighlightRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	exact := strings.TrimSpace(req.Exact)
	if exact == "" {
		writeError(w, http.StatusBadRequest, "exact must not be empty")
		return
	}
	if utf8.RuneCountInString(exact) > maxHighlightExactRunes {
		writeError(w, http.StatusBadRequest, "exact too long (max 2000 chars)")
		return
	}
	prefix, suffix := "", ""
	if req.Prefix != nil {
		prefix = truncRunes(*req.Prefix, maxHighlightContextRunes)
	}
	if req.Suffix != nil {
		suffix = truncRunes(*req.Suffix, maxHighlightContextRunes)
	}
	offsetHint := 0
	if req.OffsetHint != nil {
		offsetHint = *req.OffsetHint
	}

	ctx := r.Context()
	uid := userID(ctx)
	owned, err := s.ownsItem(ctx, uid, id)
	if err != nil {
		slog.Error("checking item ownership", "err", err)
		writeError(w, http.StatusInternalServerError, "could not create highlight")
		return
	}
	if !owned {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}

	tx, err := s.store.Pool.Begin(ctx)
	if err != nil {
		slog.Error("beginning highlight tx", "err", err)
		writeError(w, http.StatusInternalServerError, "could not create highlight")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.store.Queries.WithTx(tx)

	quote, err := q.CreateQuoteItem(ctx, db.CreateQuoteItemParams{UserID: uid, Body: exact})
	if err != nil {
		slog.Error("creating quote item", "err", err)
		writeError(w, http.StatusInternalServerError, "could not create highlight")
		return
	}
	hl, err := q.CreateHighlight(ctx, db.CreateHighlightParams{
		UserID: uid, SourceItemID: id, QuoteItemID: quote.ID,
		Exact: exact, Prefix: prefix, Suffix: suffix, OffsetHint: int32(offsetHint),
	})
	if err != nil {
		slog.Error("creating highlight row", "err", err)
		writeError(w, http.StatusInternalServerError, "could not create highlight")
		return
	}
	a, b := canonicalPair(id, quote.ID)
	if _, err := q.CreateLink(ctx, db.CreateLinkParams{UserID: uid, AItem: a, BItem: b}); err != nil {
		slog.Error("linking quote to source", "err", err)
		writeError(w, http.StatusInternalServerError, "could not create highlight")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Error("committing highlight tx", "err", err)
		writeError(w, http.StatusInternalServerError, "could not create highlight")
		return
	}

	if _, err := s.riverClient.Insert(ctx, jobs.EnrichArgs{UserID: uid, ItemID: quote.ID}, nil); err != nil {
		slog.Error("enqueueing enrichment for quote item", "item_id", quote.ID, "err", err)
	}
	writeJSON(w, http.StatusCreated, CreateHighlightResponse{Highlight: toAPIHighlight(hl), QuoteItem: toAPIItem(quote)})
}

// ListItemHighlights returns the highlights anchored to an item, oldest first.
func (s *Server) ListItemHighlights(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	ctx := r.Context()
	uid := userID(ctx)
	owned, err := s.ownsItem(ctx, uid, id)
	if err != nil {
		slog.Error("checking item ownership", "err", err)
		writeError(w, http.StatusInternalServerError, "could not list highlights")
		return
	}
	if !owned {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}
	rows, err := s.store.Queries.ListHighlightsBySource(ctx, db.ListHighlightsBySourceParams{UserID: uid, SourceItemID: id})
	if err != nil {
		slog.Error("listing highlights", "err", err)
		writeError(w, http.StatusInternalServerError, "could not list highlights")
		return
	}
	out := make([]Highlight, 0, len(rows))
	for _, h := range rows {
		out = append(out, toAPIHighlight(h))
	}
	writeJSON(w, http.StatusOK, out)
}

// DeleteHighlight removes a highlight and its quote card. Deleting the quote
// item is the single mutation: the highlights row goes via ON DELETE CASCADE,
// and the quote↔source link goes via the links cascade.
func (s *Server) DeleteHighlight(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	ctx := r.Context()
	uid := userID(ctx)
	hl, err := s.store.Queries.GetHighlight(ctx, db.GetHighlightParams{UserID: uid, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "highlight not found")
		return
	}
	if err != nil {
		slog.Error("fetching highlight", "err", err)
		writeError(w, http.StatusInternalServerError, "could not delete highlight")
		return
	}
	if _, err := s.store.Queries.DeleteItem(ctx, db.DeleteItemParams{UserID: uid, ID: hl.QuoteItemID}); err != nil {
		slog.Error("deleting quote item", "err", err)
		writeError(w, http.StatusInternalServerError, "could not delete highlight")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toAPIHighlight(h db.Highlight) Highlight {
	return Highlight{
		Id:           h.ID,
		SourceItemId: h.SourceItemID,
		QuoteItemId:  h.QuoteItemID,
		Exact:        h.Exact,
		Prefix:       h.Prefix,
		Suffix:       h.Suffix,
		OffsetHint:   int(h.OffsetHint),
		CreatedAt:    h.CreatedAt.Time,
	}
}
```

Adjust generated field names mechanically if codegen differs (check `gen.go`/`db` package). In `ratelimit.go`'s `guarded()`, add:

```go
		(method == http.MethodPost && strings.HasPrefix(path, "/items/") && strings.HasSuffix(path, "/highlights")) ||
		(method == http.MethodDelete && strings.HasPrefix(path, "/highlights/")) ||
```

- [ ] **Step 4: Run tests**

Run: `go test -p 1 ./internal/api/ -run "Highlight" -v 2>&1 | tail -12` then the full package.
Expected: PASS; `go vet ./... && go build ./...` clean (repo builds again).

- [ ] **Step 5: Commit**

```bash
git add internal/api
git commit -m "feat(highlights): transactional create/list/delete handlers"
```

---

### Task 3: Web — anchor matcher + reader selection UI + painting

**Files:**
- Create: `apps/web/lib/anchors.ts` (pure matcher)
- Create: `apps/web/lib/anchors.test.ts` (vitest — check `apps/web/package.json` for the test runner; if the web app has none, add the matcher test under the dock's vitest setup pattern or run via `pnpm dlx vitest run` with a minimal config, matching whatever the repo already does — do NOT introduce a new test framework if one exists)
- Modify: `apps/web/app/item/[id]/read/page.tsx` (+ a client component `apps/web/components/HighlightableBody.tsx`)
- Create: `apps/web/app/api/items/[id]/highlights/route.ts`, `apps/web/app/api/highlights/[id]/route.ts` (cookie proxies, copy the links proxy pattern)

**Interfaces:**
- Consumes: Task 1's TS client types (`Highlight`, `CreateHighlightRequest`) via `packages/api-client`; existing proxy conventions.
- Produces: `findAnchor(body: string, h: {exact: string; prefix: string; suffix: string; offsetHint: number}): {start: number; end: number} | null`.

- [ ] **Step 1: Write the failing matcher tests**

`apps/web/lib/anchors.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { findAnchor } from "./anchors";

const body = "One two three four five six seven eight nine ten.";

describe("findAnchor", () => {
  it("matches prefix+exact+suffix exactly", () => {
    expect(findAnchor(body, { exact: "three four", prefix: "two ", suffix: " five", offsetHint: 8 }))
      .toEqual({ start: 8, end: 18 });
  });
  it("falls back to exact-only when context drifted", () => {
    expect(findAnchor(body, { exact: "three four", prefix: "CHANGED ", suffix: " NOPE", offsetHint: 8 }))
      .toEqual({ start: 8, end: 18 });
  });
  it("returns null when the text vanished", () => {
    expect(findAnchor(body, { exact: "gone completely", prefix: "", suffix: "", offsetHint: 0 })).toBeNull();
  });
  it("picks the occurrence nearest the hint when exact repeats", () => {
    const b = "alpha X beta X gamma";
    expect(findAnchor(b, { exact: "X", prefix: "", suffix: "", offsetHint: 12 })).toEqual({ start: 13, end: 14 });
    expect(findAnchor(b, { exact: "X", prefix: "", suffix: "", offsetHint: 0 })).toEqual({ start: 6, end: 7 });
  });
});
```

- [ ] **Step 2: Run to verify failure**

Run (from apps/web, using the repo's existing runner setup): `pnpm vitest run lib/anchors.test.ts` (or the dock-style equivalent).
Expected: FAIL — module not found.

- [ ] **Step 3: Implement the matcher**

`apps/web/lib/anchors.ts`:

```ts
// findAnchor locates a highlight's text-quote anchor in the article body.
// Strategy: prefix+exact+suffix as one string first (strongest signal); then
// exact alone, choosing the occurrence nearest offsetHint. Null means the
// text no longer exists in the body — the highlight simply isn't painted.
export function findAnchor(
  body: string,
  h: { exact: string; prefix: string; suffix: string; offsetHint: number },
): { start: number; end: number } | null {
  if (!h.exact) return null;
  const full = h.prefix + h.exact + h.suffix;
  const fullIdx = body.indexOf(full);
  if (fullIdx >= 0) {
    const start = fullIdx + h.prefix.length;
    return { start, end: start + h.exact.length };
  }
  let best: number | null = null;
  let from = 0;
  for (;;) {
    const idx = body.indexOf(h.exact, from);
    if (idx < 0) break;
    if (best === null || Math.abs(idx - h.offsetHint) < Math.abs(best - h.offsetHint)) best = idx;
    from = idx + 1;
  }
  if (best === null) return null;
  return { start: best, end: best + h.exact.length };
}
```

- [ ] **Step 4: Run matcher tests → PASS, commit the matcher**

```bash
git add apps/web/lib/anchors.ts apps/web/lib/anchors.test.ts
git commit -m "feat(web): highlight anchor matcher"
```

- [ ] **Step 5: Proxies + reader UI**

Cookie proxies copy the `apps/web/app/api/items/[id]/links` pattern (GET+POST for `/items/[id]/highlights`, DELETE for `/highlights/[id]`).

`HighlightableBody.tsx` (client component) receives the plain-text body and the item id:
- Renders paragraphs as the reader does today, but with painted spans: fetch highlights on mount (`GET /api/items/{id}/highlights`), run `findAnchor` per highlight against the full body string, convert matches to per-paragraph span ranges, and wrap them in `<mark>` styled with the note-surface token (`#FBF4D8` family from `@openmind/ui` tokens — do not hardcode a new colour) plus a click → `router.push('/item/'+quoteItemId)`.
- Selection flow: `onMouseUp`, if `window.getSelection()` is non-empty and inside the body container, show a small floating "Highlight" button at the selection; on click, compute `exact` (selection text), `prefix`/`suffix` (64 chars of surrounding body text via the selection's offsets into the body string), `offsetHint` (selection start index), POST, append the returned highlight to state (paints immediately), clear selection.
- Reader page swaps its body rendering to `<HighlightableBody body={...} itemId={...}/>` for text-forward types only (same condition as the Read affordance).

- [ ] **Step 6: Build + commit**

Run: `pnpm turbo run build --filter=web` — green; `tsc` clean.

```bash
git add apps/web
git commit -m "feat(web): reader selection → highlight, painted anchors, proxies"
```

---

### Task 4: E2e + docs + wrap-up

**Files:**
- Modify: `docs/self-hosting.md` (short Highlights note in the features docs)

- [ ] **Step 1: Compose e2e** (python3 urllib, never curl; `docker compose up -d --build api web`; check port 8080 health first)

1. `POST /items {"url": "https://danluu.com/why-benchmark/"}` (or reuse an enriched item) → wait enriched.
2. `POST /items/{id}/highlights {"exact": "<a sentence from the body>", "prefix": "...", "suffix": "...", "offsetHint": N}` → 201; response quoteItem.cardType == "quote".
3. `GET /items/{id}/highlights` → the highlight; `GET /items/{quoteId}` → quote card; `GET /items/{id}/links` → contains the quote.
4. `GET /search?q=<distinctive words from the exact>` → quote card surfaces once enriched.
5. `DELETE /highlights/{hlId}` → 204; quote item 404s; highlights list empty.
6. Browser check (headless Chromium or user click-test): reader shows the painted highlight for a fresh one created via API.

- [ ] **Step 2: Docs + TODO**

`docs/self-hosting.md`: one short section — highlights are saved from the reader by selecting text; each becomes a quote card linked to the article; deleting the quote removes the highlight. Update TODO.md's Later list if it references highlights.

```bash
git add docs/self-hosting.md TODO.md
git commit -m "docs: highlights — self-hosting note"
```
