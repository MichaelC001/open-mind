# AI-suggested links (related items) — design

Date: 2026-07-16. Feature 4 of 4: embedding-similarity "Related" suggestions
on the detail page, one tap to accept into the existing bidirectional links.
MCP read tool included (user-confirmed).

## API

- `GET /items/{id}/related` → up to 5 `{item, distance}` rows:
  pgvector cosine distance between this item's embedding and the same
  user's other item embeddings; excludes self, already-linked items (either
  direction via the canonicalised `links` pair), and rows with distance
  above the threshold. Ordered nearest first.
- Threshold: a package constant (`relatedMaxDistance = 0.5`) tuned by the
  golden test; not user-configurable in v1.
- No embedding for the source item (noop provider, pending enrichment, or
  embed skipped) → `200 []`. Unknown/cross-tenant id → `404`.
- Contract-first: `openapi.yaml` gains the path + a `RelatedItem`
  `{item: Item, distance: number}` schema; Go + TS regenerated.
- New sqlc query on `item_embeddings` with `NOT EXISTS` against `links`
  (both orientations) and user scoping on every side.
- Read-only; no schema change; no dismissal persistence (accepting links it
  — which removes it from future suggestions; ignoring leaves it).

## Web

- Detail rail: a "Related" section under "Linked" — up to 5 rows (title +
  type indicator + a subtle similarity hint), each with a one-tap **+ Link**
  that POSTs the existing `/api/items/{id}/links` proxy; on success the row
  moves to the Linked section optimistically (failure restores it with the
  rail's existing error affordances).
- Section hidden entirely when the response is empty.
- New cookie proxy `apps/web/app/api/items/[id]/related` (GET), per the
  established pattern.

## MCP

- New read tool `related_items {id}` → the same rows in compact
  `{item: ItemSummary, distance}` form, via a new `Backend.Related(ctx, uid,
  id) ([]RelatedResult, error)` method on the shared adapter. Registry count
  13 → 14; tool description notes it returns nothing until the item is
  embedded.

## Out of scope

Dismiss/hide persistence, auto-linking, cross-user suggestions, threshold
configuration, backfilling embeddings for noop libraries.

## Testing

- DB-backed (seeded vectors — insert known embeddings directly):
  nearest-first ordering, self exclusion, linked exclusion (both pair
  orientations), threshold cutoff, cross-tenant isolation, no-embedding →
  empty, limit 5.
- MCP: fake-backend tool test + adapter DB test.
- Web build + tsc; compose e2e proves endpoint shape + empty state under
  noop (real similarity is the DB tests' job).
