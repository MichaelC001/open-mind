# Related Items (AI-Suggested Links) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `GET /items/{id}/related` surfaces up to 5 embedding-similar items (excluding self and already-linked), the detail rail shows them with one-tap + Link, and MCP gains a `related_items` tool.

**Architecture:** One new sqlc query over `item_embeddings` (`vector(768)`, cosine `<=>`) with a `NOT EXISTS` on the canonicalised `links` pair; a read-only contract-first endpoint; a `Backend.Related` method reused by REST and the MCP tool; a web rail section + proxy.

**Tech Stack:** pgvector (existing), sqlc, oapi-codegen, MCP go-sdk registry, Next.js rail component.

**Spec:** `docs/superpowers/specs/20260716-related-items-design.md`

## Global Constraints

- Threshold constant `relatedMaxDistance = 0.5` (cosine distance; rows with distance > threshold dropped); limit 5 (`relatedLimit`).
- Excludes: self; items linked to the source in either orientation (links stores the canonical a<b pair — the NOT EXISTS must check `(a_item, b_item) = (least, greatest)` of the pair or both orderings explicitly).
- Source item has no embedding → `200 []`; unknown/cross-tenant id → `404` (indistinguishable).
- Every query user-scoped on BOTH the source ownership and the candidate embeddings.
- Contract-first; `task generate`; never hand-edit generated code. No schema change, no migration.
- Read-only feature: no writes anywhere in the endpoint/tool.
- MCP tool count goes 13 → 14 (update any test asserting the count).
- Go from `apps/api` (`env -u GOROOT /opt/homebrew/bin/go` fallback); DB tests `-p 1`, compose db up. No banner comments; UK English copy.

---

### Task 1: Query + contract + REST handler

**Files:**
- Modify: `apps/api/internal/store/queries/search.sql` (add `RelatedByEmbedding`)
- Modify: `openapi.yaml` (path `/items/{id}/related`, schema `RelatedItem`)
- Create: `apps/api/internal/api/related.go`
- Test: `apps/api/internal/api/related_test.go` (DB-backed, seeded vectors)
- Regenerate: `task generate`

**Interfaces:**
- Consumes: `item_embeddings(item_id, user_id, embedding vector(768))`; `SearchVector`'s query style; `ownsItem`, `toAPIItem`, `writeJSON/writeError` helpers; `canonicalPair` convention (links stores a_item < b_item).
- Produces (Task 2 depends on these): sqlc `RelatedByEmbedding(ctx, RelatedByEmbeddingParams{UserID, ItemID, MaxDistance float64, LimitCount int32}) ([]RelatedByEmbeddingRow)` where the row carries `i.*` + `Distance float64`; handler `GetRelatedItems(w, r, id)`; API schema `RelatedItem {item: Item, distance: number}`; package consts `relatedMaxDistance = 0.5`, `relatedLimit = 5` in related.go.

- [ ] **Step 1: Query**

Append to `apps/api/internal/store/queries/search.sql`:

```sql
-- name: RelatedByEmbedding :many
-- Nearest unlinked items to the given item's embedding, same user only.
-- The links table stores one canonicalised row per pair (a_item < b_item),
-- so the exclusion checks the pair in canonical order.
SELECT i.*, (e.embedding <=> src.embedding)::float8 AS distance
FROM item_embeddings src
JOIN item_embeddings e ON e.user_id = src.user_id AND e.item_id <> src.item_id
JOIN items i ON i.id = e.item_id
WHERE src.user_id = $1 AND src.item_id = $2
  AND (e.embedding <=> src.embedding) <= $3
  AND NOT EXISTS (
    SELECT 1 FROM links l
    WHERE l.user_id = src.user_id
      AND l.a_item = LEAST(src.item_id, e.item_id)
      AND l.b_item = GREATEST(src.item_id, e.item_id)
  )
ORDER BY e.embedding <=> src.embedding
LIMIT $4;
```

- [ ] **Step 2: Contract**

`openapi.yaml` (match the `/items/{id}/links` GET block's style):

```yaml
  /items/{id}/related:
    get:
      operationId: getRelatedItems
      parameters:
        - {name: id, in: path, required: true, schema: {type: string, format: uuid}}
      responses:
        '200':
          description: up to 5 embedding-similar items, nearest first; empty when the item has no embedding
          content:
            application/json:
              schema: {type: array, items: {$ref: '#/components/schemas/RelatedItem'}}
        '404': {description: unknown item}
```

Schema:

```yaml
    RelatedItem:
      type: object
      required: [item, distance]
      properties:
        item: {$ref: '#/components/schemas/Item'}
        distance: {type: number, format: double}
```

Run `task generate`; expect the missing-`GetRelatedItems` build break until Step 4.

- [ ] **Step 3: Failing DB tests**

`related_test.go` (reuse the package's DB fixture; seed embeddings by inserting into `item_embeddings` directly with literal vectors — build a helper `seedEmbedding(t, s, uid, itemID, v []float32)` writing via `s.Pool.Exec` with the pgvector text format `'[0.1,0.2,...]'`; 768 dims — use a helper that makes a mostly-zero vector with a few distinguishing components):

```go
func TestRelatedOrdersByDistance(t *testing.T)      // three candidates at increasing distance → nearest first, all ≤ threshold
func TestRelatedExcludesSelfAndLinked(t *testing.T) // linked candidate (insert canonical links row both directions of id ordering across two subtests) absent; self absent
func TestRelatedThresholdCutoff(t *testing.T)       // orthogonal vector (distance ~1.0) → excluded
func TestRelatedLimit(t *testing.T)                 // 7 close candidates → 5 returned
func TestRelatedNoEmbeddingEmpty(t *testing.T)      // source item without embedding → 200 []
func TestRelatedScoping(t *testing.T)               // other user's near-identical embedding never appears; other user GETs the source id → 404
```

Full bodies with real HTTP requests per the package conventions.

- [ ] **Step 4: Handler**

`apps/api/internal/api/related.go`:

```go
package api

import (
	"log/slog"
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

const (
	// relatedMaxDistance is the cosine-distance ceiling for a suggestion —
	// beyond this the pair isn't meaningfully related. Tuned by the DB tests.
	relatedMaxDistance = 0.5
	relatedLimit       = 5
)

// GetRelatedItems returns up to relatedLimit embedding-similar items for one
// item, nearest first, excluding itself and anything already linked. An item
// with no embedding (noop provider, pending enrichment) yields an empty list
// rather than an error — the UI hides the section.
func (s *Server) GetRelatedItems(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	ctx := r.Context()
	uid := userID(ctx)

	owned, err := s.ownsItem(ctx, uid, id)
	if err != nil {
		slog.Error("checking item ownership", "err", err)
		writeError(w, http.StatusInternalServerError, "could not load related items")
		return
	}
	if !owned {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}

	rows, err := s.store.Queries.RelatedByEmbedding(ctx, db.RelatedByEmbeddingParams{
		UserID: uid, ItemID: id, MaxDistance: relatedMaxDistance, LimitCount: relatedLimit,
	})
	if err != nil {
		slog.Error("querying related items", "err", err)
		writeError(w, http.StatusInternalServerError, "could not load related items")
		return
	}
	out := make([]RelatedItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, RelatedItem{Item: relatedRowToAPIItem(row), Distance: row.Distance})
	}
	writeJSON(w, http.StatusOK, out)
}
```

`relatedRowToAPIItem` maps the sqlc row's item columns through the same shape `toAPIItem` uses (check the generated row struct's field names — if sqlc embeds the item columns flat, write the small mapper; if a shared helper is extractable without contortions, prefer that). Adjust sqlc param names to what codegen actually produced.

- [ ] **Step 5: Run** `go test -p 1 ./internal/api/ -run TestRelated -v` → PASS; full api suite + vet/build clean.

- [ ] **Step 6: Commit** `feat(related): embedding-similarity endpoint`

---

### Task 2: MCP tool `related_items`

**Files:**
- Modify: `apps/api/internal/mcp/mcp.go` (Backend gains `Related`; DTO), `apps/api/internal/mcp/tools.go` (tool), `apps/api/internal/api/mcp.go` (adapter method)
- Test: `apps/api/internal/mcp/mcp_test.go` (fake backend + tool test; bump any 13-count assertion to 14), `apps/api/internal/api/mcp_backend_test.go` (adapter DB test)

**Interfaces:**
- Consumes: Task 1's `RelatedByEmbedding` + consts; existing `Backend`, `ItemSummary`, `toSummary`, `toolErr/ok/isNotFound`, fake-backend pattern.
- Produces:

```go
// mcp.go
Related(ctx context.Context, uid, id uuid.UUID) ([]RelatedResult, error) // Backend method
type RelatedResult struct { Item db.Item; Distance float64 }
type relatedOut struct { Results []RelatedHit `json:"results"` }
type RelatedHit struct { Item ItemSummary `json:"item"`; Distance float64 `json:"distance"` }
```

- [ ] **Step 1: Failing tests** — fake backend `Related` returning two seeded rows (notFound flag → ErrNotFound); tool test `TestRelatedItemsTool`: valid id → 2 hits with distances; bad uuid → tool error; notFound → "item not found". Bump the tools/list count assertion 13→14. Adapter DB test `TestMCPBackendRelated`: seeded embeddings → rows; cross-tenant → ErrNotFound.

- [ ] **Step 2: Implement** — tool `related_items {id}` with description: "Find items similar to the given item by embedding distance (nearest first, max 5). Returns an empty list until the item has been embedded; suggestions exclude items already linked." Adapter method mirrors the handler: ownsItem-equivalent via GetItem (ErrNoRows → appmcp.ErrNotFound), then `RelatedByEmbedding` with the Task 1 consts (export them within the api package or duplicate the two consts in the adapter with a comment — prefer referencing the api consts since the adapter lives in package api).

- [ ] **Step 3: Run** `go test ./internal/mcp/ -v` + `go test -p 1 ./internal/api/ -run TestMCPBackend` → PASS; vet/build clean.

- [ ] **Step 4: Commit** `feat(mcp): related_items tool`

---

### Task 3: Web — rail section + proxy

**Files:**
- Create: `apps/web/app/api/items/[id]/related/route.ts` (GET proxy — copy the highlights GET proxy incl. its 502 wrapper)
- Create: `apps/web/components/RelatedRail.tsx` (client component)
- Modify: the detail page's rail (`apps/web/app/item/[id]/page.tsx` — find where the Linked section renders) to mount `<RelatedRail itemId={...}/>` beneath it.

**Interfaces:**
- Consumes: Task 1's `RelatedItem` TS type via `packages/api-client`; existing `/api/items/[id]/links` POST proxy for the one-tap link; the Linked section's row styling conventions (read the existing component first).

- [ ] **Step 1: Implement**

- Proxy: GET pass-through with try/catch → console.error → 502 JSON (match the highlights route file exactly in shape).
- `RelatedRail`: fetch on mount (loadFailed state with retry pill per house pattern); render nothing when empty; rows = title + card-type indicator + subtle distance-derived hint (e.g. "close match" ≤0.25 else "related"); **+ Link** button per row → POST `/api/items/{itemId}/links {toId}` → on success remove the row and emit the same optimistic update the Linked section uses (if the Linked list is server-rendered, trigger `router.refresh()` after success — read how the existing link-picker updates and match it); failure → restore row + inline error.
- UK English, tokens only.

- [ ] **Step 2: Verify** `pnpm turbo run build --filter=web` + tsc clean.

- [ ] **Step 3: Commit** `feat(web): related items rail with one-tap linking`

---

### Task 4: E2e + docs

- [ ] **Step 1: Compose e2e** (python3 urllib; rebuild api+web): under noop, `GET /items/{id}/related` on an enriched item → `200 []` (no embeddings — expected); unknown id → 404; seed two embeddings directly via psql (`INSERT INTO item_embeddings ... '[...768-dim literal...]'`) for two real items → related returns the counterpart with a sane distance; POST the link via the API → related now returns `[]` (linked exclusion live); MCP `related_items` via JSON-RPC returns the same before/after. Detail page HTML contains the Related section only in the has-suggestions state.
- [ ] **Step 2: Docs** — `docs/self-hosting.md`: one paragraph under the linking/Tags area: Related suggestions appear on the detail page once items are embedded (needs an AI provider with embeddings; hidden under noop); accepting creates a normal link. Close issue if one exists (none — this was TODO-only).
- [ ] **Step 3: Commit** `docs: related items — self-hosting note`
