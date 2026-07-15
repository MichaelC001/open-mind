# Highlights → quote cards — design

Date: 2026-07-15. PRD §5.7's deferred feature: select text in the reader,
save it as a highlight that is both painted in the article and mirrored as a
first-class quote card. First of the four-feature run (highlights → Kindle
follow-ups → dock v2 → AI-suggested links).

## Decisions (user-confirmed)

- **Both representations**: a dedicated `highlights` table (in-text
  positions) plus a mirrored quote item per highlight.
- **W3C-style text-quote anchoring**: `exact` + `prefix`/`suffix` context +
  an offset hint — survives re-extraction; raw offsets alone do not.

## Schema

Migration `00xx_highlights.sql` (next free number):

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

- `source_item_id` cascade: painting is meaningless without the article.
- `quote_item_id` cascade: deleting the quote card removes its highlight.
- The quote card survives source-article deletion as an independent item
  (its highlight row goes; the links row already cascades).
- `prefix`/`suffix` are capped at 64 runes server-side; `exact` capped at
  2000 runes (a highlight is a quote, not an archive).

## Contract (openapi.yaml — contract-first, regenerate Go + TS)

- `POST /items/{id}/highlights` `{exact, prefix?, suffix?, offsetHint?}` →
  `201 {highlight, quoteItem}`. Validation: source item exists and is yours
  (404 otherwise), `exact` non-empty after trim (400), caps enforced (400).
- `GET /items/{id}/highlights` → `200 [...]` (bare array, for painting;
  each carries its `quote_item_id`).
- `DELETE /highlights/{id}` → `204`; removes the highlight AND its quote
  card (the FK cascade runs quote→highlight, so the handler deletes the
  quote item and lets the cascade clear the row). 404 unknown/cross-tenant.

## Capture flow (capture is sacred)

`POST` runs in one transaction: create quote item (`card_type: 'quote'`,
`body = exact`, no URL, status `pending`) → insert `highlights` row → insert
the bidirectional `links` row (existing canonicalised-pair machinery)
between quote and source. Any failure rolls the whole thing back. After
commit, best-effort enqueue enrichment for the quote item (same rule as
every save: a failed enqueue is logged, never an error). No AI in the
request path; fully functional under `noop`.

## Web

- **Reader** (`/item/[id]/read`): on text selection inside the body, a small
  floating "Highlight" button appears near the selection; clicking POSTs
  (deriving `prefix`/`suffix` from the surrounding text, `offsetHint` from
  the selection position) and paints immediately.
- **Painting**: on load, fetch `GET /items/{id}/highlights` and mark matches
  — search for `prefix + exact + suffix`, falling back to `exact` alone
  nearest `offset_hint`; no match → not painted (quote card unaffected).
  Painted spans get a warm highlight background (note surface `#FBF4D8`
  family token) and click-through to the quote card.
- **Detail page**: no new section needed — the existing Linked rail already
  shows quote ↔ article. Quote cards render with the existing gold-glyph
  quote treatment.
- New cookie-proxy routes for the three endpoints, per the established
  pattern.

## Degradation

Re-extraction that changes the body only breaks painting for anchors that
no longer match; the highlight stays listed and its quote card lives on.

## Out of scope

Cross-highlight colours, margin notes, PDF-view painting (PDF bodies are
searchable text — painting applies to the reader only), highlight export,
MCP highlight tools.

## Testing

- DB-backed handler tests: create (201, quote item + link exist,
  transactional — induced link failure rolls back the quote), list, delete
  (quote card gone too), cross-tenant/unknown 404s, validation 400s, caps.
- Two identical selections → two distinct highlights (by design).
- Anchor-matcher unit tests (web, vitest or pure TS function): exact match,
  shifted text (prefix/suffix rescue), vanished text (no paint), multiple
  occurrences (nearest to hint wins).
- Web build green; compose e2e: save article → select in reader → highlight
  → re-open reader → painted; quote card in grid + search; delete removes
  both.
