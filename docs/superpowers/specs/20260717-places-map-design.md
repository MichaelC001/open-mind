# Places map — web + mobile surfacing (reel places Phase 3)

Date: 2026-07-17. Follow-on to `20260716-reel-places-design.md` Phase 3,
with the mobile map pulled forward (originally "map later"). Prerequisite
state: place extraction + geocoding are live in production (Google Places
geocoder, PR #27); `GET /items/{id}/places` already exists.

## Goal

Show saved places as pins on a map, and list an item's places on its
detail view — on both web and mobile.

## API (contract-first)

- `openapi.yaml`: new `GET /places` → `200 [PlaceWithItem]`, where
  `PlaceWithItem` = `Place` (id, name, hint, address, lat?, lng?, source)
  plus `item_id`, `item_title`, `item_type` — enough for a pin popup to
  say what the place came from and link to the item. Regenerate Go + TS
  (`task generate`).
- sqlc query `ListPlaces(user_id)` joining `item_places` → `items`,
  ordered `created_at desc`. User-scoped like every store method.
  Coordinate-less places are included; clients render them in a list
  rather than on the map.
- Web proxy: `apps/web/app/api/places/route.ts` streaming to the API,
  following the existing `/api/items/[id]/places` pattern (the Go API is
  never publicly exposed).

## Web

- New `/places` page: client component rendering a full-bleed MapLibre GL
  map (`maplibre-gl` npm dependency; OSM raster tiles, client-side only —
  no new service, per the single-binary principle). One marker per
  geocoded place; popup shows name, address, and a link to
  `/item/{item_id}`. Places without coordinates render in a side/below
  list with OSM + Google Maps search links.
- Navigation: "Places" entry alongside Mind / Desk / Drift.
- Item detail: "Places" section in the rail via the existing
  `GET /items/{id}/places` — name, hint, address, "open in maps" link.
  No per-item mini-map on web; the global map covers it.
- Styling: design tokens from `packages/ui` only; no hardcoded colours.

## Mobile (react-native-maps)

- Add `react-native-maps` via its Expo config plugin. Native module →
  requires a new dev-client build and a TestFlight rebuild. EAS
  credential/auth prompts are handed to the user (standing rule: never
  automate Apple auth).
- Global map: `places.tsx` stack screen (NOT a sixth tab), opened from a
  map icon in the Library header. Pins with callouts; tapping a callout
  navigates to `/item/{id}`.
- Item detail: places list section; each row opens the platform maps app
  (`maps:` on iOS, `geo:` on Android). When the item has ≥1 geocoded
  place, show a small non-interactive `MapView` snippet above the list.
- iOS uses Apple Maps (no key). Android pin support needs a Google Maps
  API key — out of scope; the list + deep links still work there.

## Testing

- Go: handler tests for `GET /places` — 200 with rows, empty list,
  cross-tenant rows excluded.
- Web: page renders markers/list from a mocked api-client response.
- Mobile: existing component-test patterns; no map-rendering assertions
  (native view), just that screens render with fixture data.

## Out of scope

Marker clustering, place editing/dedup, reverse geocoding, Android Google
Maps key setup, PostGIS, per-lens place filtering.
