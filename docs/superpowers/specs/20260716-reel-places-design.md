# Reel place extraction — design

Date: 2026-07-16. Stretch goal: share an Instagram reel (or TikTok) to
Openmind and the pipeline extracts the cafes, landmarks, and hotels it
mentions — the "Triply / Wander" use case — storing them with coordinates
so a map view can follow. Phased; Phase 1 ships in this branch, later
phases are design-only until scheduled.

## Why this is hard (constraints first)

- **Official Instagram APIs are out.** oEmbed/Graph access needs a Meta
  app + review + business account — a non-starter for self-hosters. All
  reading is anonymous fetching of the public reel page, which Instagram
  rate-limits and sometimes login-walls. The design must degrade
  gracefully at every step, never block the save (capture is sacred), and
  never require a new service (Postgres + one binary stays the whole
  deployment).
- **The share payload is just a URL.** Mobile share-sheet sends
  `POST /items {url}` — nothing else. All intelligence is server-side.
- **The AI adapter is text-only today.** Vision (thumbnail/frames) needs a
  new Provider method; noop must keep the app functional (no places, no
  breakage).

## Signal ladder (cheapest first, stop when confident)

1. **Caption text** — `og:description` on the public reel page HTML.
   Carries the caption for most public reels; captions are where creators
   list "📍 5 cafes in Lisbon…". Cheap text-LLM extraction. *(Phase 1)*
2. **Page metadata** — `og:title` (author + hook line), `og:image`
   (thumbnail). Title/lead image for the card even when extraction finds
   nothing. *(Phase 1)*
3. **Thumbnail vision** — one image → vision-capable cheap model
   (Gemini Flash-Lite is multimodal) reads on-screen text overlays, which
   often name the place when the caption doesn't. *(Phase 2)*
4. **Video frames / transcript** — optional yt-dlp + ffmpeg behind config
   (`REEL_MEDIA=off|thumbnail|video`, default off): sample N frames, batch
   into one vision call; Gemini can also take short video directly.
   Optional binaries, never required. *(Phase 4)*

Anti-hallucination rule at every rung: a candidate must be grounded in
the fetched signal (caption/overlay text), carry the model's confidence,
and survive geocoding sanity (a hint mismatch drops coordinates, not the
place).

## Phase 1 — caption → places (this branch)

### Capture & classify

- `Classify` gains social-video hosts → `video`: `instagram.com`,
  `instagr.am`, `tiktok.com`, `vm.tiktok.com`, `vt.tiktok.com`.
  Exported `IsSocialVideoURL(url)` shared with the job gate.
- New pipeline branch before the generic extractor: social-video URLs go
  to an **OG extractor** (fetch via `SafeHTTPClient`, parse
  `og:title|description|image` with `x/net/html`, 10 MB cap). The caption
  lands in `body` so FTS/summarise/tag/embed all see it.
- **Never `failed`:** a login wall / fetch error degrades to a bare card
  (`title` = "Instagram reel" / "TikTok video", empty body) and the item
  still reaches `enriched`. Re-runs can only improve it.

### AI adapter

- `Provider` gains
  `ExtractPlaces(ctx, title, caption string) ([]Place, error)`;
  `Place{Name, Hint string}` — `Hint` is the disambiguating locality from
  the caption ("Lisbon", "Shibuya"). Shared prompt const; JSON mode on
  Gemini (response schema) and OpenAI-compatible (`json_object`); noop →
  `ErrNotSupported` (no places, app fine); fake → deterministic fixtures;
  chain → `runChain` like every other op. Cheap tier only, per principle 6.

### Geocoding (pluggable, optional)

- `internal/geo`: `Geocoder{ Geocode(ctx, query) (Result, bool, error) }`,
  `Result{Lat, Lng, Address}`. `GEOCODER=nominatim` enables OSM Nominatim
  (`NOMINATIM_URL` for self-hosted instances, `NOMINATIM_EMAIL` for the
  public endpoint's UA policy, hard 1 rps client limiter). Unset → no
  geocoding: places persist with NULL lat/lng and the UI lists them by
  name. Geocode failure never fails the job.

### Storage

- Migration `0017_places.sql`:
  `item_places(id, user_id → users, item_id → items ON DELETE CASCADE,
  name, hint, address, lat/lng double precision NULL, source,
  created_at, UNIQUE(item_id, name))` + index `(user_id, item_id)`.
  No PostGIS — plain doubles; radius search can add `earthdistance`
  later without a rewrite.
- sqlc: `DeleteItemPlaces` + `InsertItemPlace` + `ListItemPlaces`, all
  user-scoped.

### Job

- New River job `extract_places {user_id, item_id}` in `internal/jobs`,
  enqueued by `EnrichWorker` after a successful pipeline run when
  `IsSocialVideoURL(item.url)` (client back-reference set post-construction,
  same pattern as `ScanDigestsWorker.River`). Separate job so a slow
  geocoder or model never blocks/retries core enrichment.
- Worker: refetch item → `ExtractPlaces(title, body)` →
  `ErrNotSupported`/empty → done; else geocode each (sequential, limiter)
  → delete + insert rows. **Idempotent:** re-run reproduces the same rows.

### API (contract-first)

- `openapi.yaml`: `Place{id, name, hint, address, lat?, lng?, source}`
  schema + `GET /items/{id}/places` → `200 [Place]` (empty when none),
  `404` unknown/cross-tenant. Go + TS regenerated. No `Item` shape change.

## Phase 2 — thumbnail vision (shipped)

- Provider gains
  `ExtractPlacesVision(ctx, title, caption string, image []byte)`;
  Gemini implements via multimodal parts, others return `ErrNotSupported`.
  Job fetches lead-image (`og:image`) bytes via the existing size-capped
  `fetchLeadImage`, merges vision candidates with caption candidates by
  normalised name (higher confidence wins; tie → caption), and stores
  `source` as `caption` or `vision`. Vision failures are best-effort
  (warn + keep caption results); empty caption + thumbnail still runs
  vision alone.

## Phase 3 — surfacing

- Web: "Places" section on the item detail rail (name + hint + open in
  OSM/Google Maps link); map view over `GET /places` (all of a user's
  places) with MapLibre GL + OSM raster tiles — client-side lib only, no
  new service. Design tokens from `packages/ui`; no hardcoded colours.
- Mobile: places list on item detail; `geo:`/maps deep links. Map later.
- MCP: read tool `item_places {id}` via the shared backend adapter.
- Search: place names already land in FTS via a tags/body pathway —
  evaluate adding `name || hint` to the item's searchable text.

## Phase 4 — deep media (optional, behind config)

- `REEL_MEDIA=off|thumbnail|video` (default `off`). `video` shells out to
  a user-installed yt-dlp (+ ffmpeg) to pull the mp4, samples ≤8 frames,
  one batched vision call. Binaries detected at startup; absence logs and
  falls back to `thumbnail`. Never a compose requirement.
- Location tag: reels sometimes embed a tagged location in inline JSON —
  parse opportunistically when present; it outranks caption candidates.

## Out of scope (all phases)

Logged-in Instagram scraping / cookie import, third-party scraper APIs
(Apify etc.), TikTok/Instagram official APIs, itinerary building, place
dedup across items, PostGIS, reverse geocoding, YouTube (different
extraction economics — revisit separately).

## Risks

- **Instagram blocking anonymous fetches** is the load-bearing risk: OG
  tags are served to most anonymous UAs today, but that can change. The
  degrade path (bare video card, zero places) is the product floor; the
  ladder's upper rungs (user-supplied yt-dlp) are the self-hoster escape
  hatch. Fetch-on-save (once, seconds after share) keeps volume trivially
  low for personal use.
- **Hallucinated places**: grounded-candidate prompt + confidence +
  geocode sanity; places are display metadata, never destructive.
- **Nominatim public endpoint policy**: 1 rps limiter + identifying UA
  with contact email; self-hosted URL supported for heavy users.

## Testing

- Classify: table rows for every social host + negative cases.
- OG extractor: `httptest` fixtures (full tags, missing description,
  login-wall HTML, non-HTML, oversized) → extraction + degrade paths.
- Job: DB-backed with fake provider — happy path, idempotency (run twice,
  same rows), noop provider → no rows + no error, geocoder-off → NULL
  coords, cross-tenant scoping.
- Geocoder: `httptest` Nominatim stub (hit, miss, 429).
- Handler: 200 list, empty list, 404 cross-tenant.
- Compose e2e: share a fixture URL, assert places appear (noop → none).
