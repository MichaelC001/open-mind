# MCP tools v2 (HTTP) — design

Date: 2026-07-14. Scope: the HTTP-transport portion of the TODO "MCP
fast-follows" bundle — write tools, Desk/Drift read tools, minimal
resources/prompts, per-credential rate limiting for `/mcp`, and the cosmetic
error-handling minors from the M3 branch review. The stdio transport is a
separate spec (`20260714-mcp-stdio-design.md`).

## Goal

Agents connected over MCP can act on the library — tag, pin, delete, manage
Lenses — and read the Desk and Drift candidates, with safety proportional to
blast radius. `/mcp` traffic stops competing with the rest of the app for the
global per-IP rate bucket.

## Decisions (user-confirmed)

- **`delete_item` uses a confirm token**: a call without `confirm: true`
  does not delete; it returns a tool error that echoes the item's title/URL
  and instructs the agent to check with the human and re-call with
  `confirm: true`. Stateless, no expiry.
- **Drift is read-only for agents**: `get_drift` returns candidates; there
  is no MCP way to stamp `last_drifted_at`. An agent that wants to keep an
  item uses `pin_item`. The user's daily Drift ritual is never consumed by
  an agent.
- **`delete_lens` needs no confirm**: a Lens is a small recreatable rule.
  The response echoes the deleted lens (name + rule) so it can be undone by
  `create_lens`.
- **Rate limiting is per-credential**, not per-IP, for `/mcp` (see below).

## Changes

### Backend interface (`internal/mcp/mcp.go`) and adapter (`internal/api/mcp.go`)

`Backend` gains seven methods, implemented by the `mcpBackend` adapter over
the same store queries / helpers the REST handlers use (all user-scoped;
unknown/cross-tenant ids map to `ErrNotFound`):

- `SetUserTags(ctx, uid, id, tags []string) (db.Item, error)` — same
  canonicalisation as `PATCH /items/{id}` (trim, lowercase, dedupe, cap
  30 tags / 50 chars), reusing the existing `canonicalTags` helper.
- `SetPinned(ctx, uid, id, pinned bool) (db.Item, error)`
- `DeleteItem(ctx, uid, id) (db.Item, error)` — returns the item (for the
  confirm echo) then deletes; adapter does get-then-delete.
- `CreateLens(ctx, uid, name string, rule LensRule) (db.Lense, error)` —
  same validation/canonicalisation as `POST /lenses` (≥1 signal, known
  colour/types).
- `DeleteLens(ctx, uid, id) (db.Lense, error)` — returns the deleted lens.
- `GetDesk(ctx, uid) ([]db.Item, error)`
- `GetDrift(ctx, uid) ([]db.Item, int, error)` — candidates + total, same
  query as `GET /drift`, read-only.

### New tools (`internal/mcp/tools.go`)

1. `set_user_tags {id, tags[]}` → updated ItemSummary. Full replace,
   mirroring PATCH semantics; empty array clears.
2. `pin_item {id, pinned}` → updated ItemSummary. Description tells agents
   this is also how you "keep" a Drift candidate.
3. `delete_item {id, confirm?}` → without `confirm:true`: tool error
   `refusing to delete "<title-or-url>" — re-call with confirm:true after
   checking with the user`. With it: `{deleted:true, id, title, url}`.
4. `create_lens {name, rule{q?,color?,types?}}` → LensInfo.
5. `delete_lens {id}` → `{deleted:true, lens: LensInfo}`.
6. `get_desk {}` → itemListOut (pinned items, newest-pinned first).
7. `get_drift {}` → `{items: ItemSummary[], total}` with a description
   noting it is read-only and does not consume the user's Drift.

### Resources and prompts (minimal, `internal/mcp`)

- One resource template `openmind://item/{id}` → the item's archived body
  as `text/plain` (title as the resource name). Unknown id → resource
  not-found error.
- One prompt `find_and_summarise(query)` → messages guiding a client to
  `search_items` then `get_item` and summarise the best match.
- Nothing else (no subscriptions, no list-changed notifications).

### Rate limiting (`internal/api`, middleware)

`/mcp` leaves the global 1 req/s + burst-10 per-IP bucket and gets its own
limiter keyed by SHA-256 of the presented Bearer credential: 5 req/s,
burst 20, per credential. Requests without a credential fall back to the
per-IP key (they 401 at auth anyway, but stay bounded). This also fixes the
web-proxy problem where all proxied MCP traffic bucketed under the web
container's IP. Same LRU/TTL housekeeping as the existing limiter.

### Minors fixed in passing

- Tool-error strings and `internal/api/mcp.go` Backend methods wrap
  underlying errors with `fmt.Errorf("...: %w", err)` instead of string
  concatenation / unwrapped returns.
- `list_lenses` no longer silently drops a malformed stored rule: the lens
  is still listed (id + name) with the zero rule, plus a `ruleError` note
  field so the agent knows the rule failed to parse.

## Out of scope

- stdio transport (own spec), write tools for Drift stamping, MCP
  subscriptions/notifications, per-user Kindle/etc. tools.

## Testing

- Unit tests against the existing fake backend for every new tool: happy
  path, invalid uuid, not-found; `delete_item` both confirm branches;
  `get_drift` asserts nothing on the backend mutates.
- Rate limiter: unit tests for credential-key derivation, bucket isolation
  between two credentials, and IP fallback.
- DB-backed adapter tests for the seven Backend methods (user-scoping /
  cross-tenant 404 parity with REST).
- Compose e2e: JSON-RPC drive of the new tools (incl. confirm refusal, then
  confirmed delete), resource read, prompt get, and a burst test showing
  /mcp no longer 429s under the global bucket.
