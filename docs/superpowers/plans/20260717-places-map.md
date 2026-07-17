# Places Map Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Surface extracted places as map pins (global map on web + mobile) and as list sections on item detail views.

**Architecture:** Contract-first: new `GET /places` in `openapi.yaml` returning places joined with their item context, implemented as a sqlc query + Go handler. Web adds a `/places` MapLibre GL page and a Places rail section; mobile adds a react-native-maps stack screen and an item-detail places section.

**Tech Stack:** Go (chi, sqlc, oapi-codegen), Next.js, maplibre-gl, Expo + react-native-maps.

## Global Constraints

- `openapi.yaml` is the single source of truth — regenerate with `task generate`, never hand-edit `packages/api-client` or sqlc/oapi-codegen output.
- Every store query is scoped by `user_id`.
- Web styling uses `tokens` from `@openmind/ui` — no hardcoded colours.
- Mobile styling uses `colors, fonts, radius, spacing` from `@/lib/theme`.
- No new required services; maplibre-gl and react-native-maps are client-side only. OSM raster tiles on web.
- Product vocabulary: never mymind's names.
- Commit after each task; Go work runs from `apps/api`.
- `npx` is shimmed on this machine — use `./node_modules/.bin/<tool>` or `pnpm dlx`.

---

### Task 1: API contract + store query + handler (`GET /places`)

**Files:**
- Modify: `openapi.yaml` (path after `/items/{id}/places` block ~line 160; schema after `Place` ~line 636)
- Modify: `apps/api/internal/store/queries/places.sql`
- Create: `apps/api/internal/api/places_list.go`
- Test: `apps/api/internal/api/places_test.go` (append)

**Interfaces:**
- Consumes: existing `Place` schema, `writeJSON`/`writeError`/`userID` helpers in `internal/api`, `s.store.Queries`.
- Produces: `GET /places` → `200 [PlaceWithItem]` where `PlaceWithItem = Place & {itemId: uuid, itemTitle: string, itemCardType: string}`; sqlc method `ListPlaces(ctx, userID uuid.UUID) ([]ListPlacesRow, error)` with row fields `ID, Name, Hint, Address, Lat, Lng, Source, ItemID, ItemTitle, ItemCardType`.

- [ ] **Step 1: Add the contract**

In `openapi.yaml`, after the `/items/{id}/places:` path block, add:

```yaml
  /places:
    get:
      operationId: listPlaces
      summary: All of the user's extracted places
      description: "Every place extracted across the user's items, newest item first, joined with just enough item context to label a map pin. Includes places without coordinates (never geocoded or geocoder miss) — clients list those instead of pinning them."
      responses:
        "200":
          description: all places; empty when none
          content:
            application/json:
              schema:
                type: array
                items: { $ref: "#/components/schemas/PlaceWithItem" }
```

After the `Place:` schema, add:

```yaml
    PlaceWithItem:
      allOf:
        - $ref: "#/components/schemas/Place"
        - type: object
          required: [itemId, itemTitle, itemCardType]
          properties:
            itemId: { type: string, format: uuid }
            itemTitle: { type: string }
            itemCardType: { type: string }
```

- [ ] **Step 2: Add the sqlc query**

Append to `apps/api/internal/store/queries/places.sql`:

```sql
-- name: ListPlaces :many
SELECT p.id, p.name, p.hint, p.address, p.lat, p.lng, p.source,
       i.id AS item_id, i.title AS item_title, i.card_type AS item_card_type
FROM item_places p
JOIN items i ON i.id = p.item_id AND i.user_id = p.user_id
WHERE p.user_id = $1
ORDER BY i.created_at DESC, p.name;
```

- [ ] **Step 3: Regenerate**

Run from repo root: `task generate`
Expected: sqlc emits `ListPlaces` + `ListPlacesRow` in `internal/store/db`; oapi-codegen emits `PlaceWithItem` and a `ListPlaces(w, r)` interface method in `internal/api/gen.go`; TS client regenerates. Build breaks until Step 5 — expected.

- [ ] **Step 4: Write the failing handler test**

Append to `apps/api/internal/api/places_test.go`, following that file's existing setup helpers (read the file first and reuse its server/fixture pattern — it already seeds users and items for `GetItemPlaces` tests):

```go
func TestListPlaces(t *testing.T) {
	// Reuse the existing places_test.go harness: seed two users, one item
	// each, insert one geocoded + one coordinate-less place for user A and
	// one place for user B.
	// Assert for user A:
	//   GET /places → 200, exactly A's 2 places, itemId/itemTitle/itemCardType
	//   populated, geocoded row has lat/lng, other row omits them.
	//   B's place never appears (cross-tenant).
	// Assert empty: a third user with no places → 200 [].
}
```

Write it as a real test against the harness (table of rows, like the existing tests) — the comment above is the required coverage, not the implementation.

- [ ] **Step 5: Run to verify it fails**

Run from `apps/api`: `go test ./internal/api/ -run TestListPlaces -v`
Expected: FAIL — `ListPlaces` handler undefined / 404.

- [ ] **Step 6: Implement the handler**

Create `apps/api/internal/api/places_list.go`:

```go
package api

import (
	"log/slog"
	"net/http"
)

// ListPlaces returns every place extracted across the user's items, newest
// item first, with enough item context to label a map pin. Places without
// coordinates are included; clients list rather than pin them.
func (s *Server) ListPlaces(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := s.store.Queries.ListPlaces(ctx, userID(ctx))
	if err != nil {
		slog.Error("querying places", "err", err)
		writeError(w, http.StatusInternalServerError, "could not load places")
		return
	}
	out := make([]PlaceWithItem, 0, len(rows))
	for _, row := range rows {
		p := PlaceWithItem{
			Id:           row.ID,
			Name:         row.Name,
			Hint:         row.Hint,
			Address:      row.Address,
			Source:       row.Source,
			ItemId:       row.ItemID,
			ItemTitle:    row.ItemTitle,
			ItemCardType: row.ItemCardType,
		}
		if row.Lat.Valid {
			p.Lat = &row.Lat.Float64
		}
		if row.Lng.Valid {
			p.Lng = &row.Lng.Float64
		}
		out = append(out, p)
	}
	writeJSON(w, http.StatusOK, out)
}
```

Check the generated `PlaceWithItem` field names in `internal/api/gen.go` after Step 3 and match them exactly (allOf generation may flatten or embed).

- [ ] **Step 7: Run tests to verify they pass**

Run from `apps/api`: `go test ./internal/api/ -run "TestListPlaces|TestGetItemPlaces" -v`
Expected: PASS. Then full: `go test ./...` → PASS.

- [ ] **Step 8: Commit**

```bash
git add openapi.yaml apps/api packages/api-client
git commit -m "feat(api): GET /places — all extracted places with item context"
```

---

### Task 2: Web proxy route + Places map page + nav

**Files:**
- Create: `apps/web/app/api/places/route.ts`
- Create: `apps/web/app/places/page.tsx`
- Create: `apps/web/components/PlacesMap.tsx`
- Modify: `apps/web/components/Shell.tsx` (nav link after Drift, ~line 151; add `activePlaces` prop)
- Modify: `apps/web/package.json` (add `maplibre-gl`)

**Interfaces:**
- Consumes: `GET /places` from Task 1; `apiFetch` from `apps/web/lib/api`; `tokens` from `@openmind/ui`.
- Produces: `/places` page; `PlacesMap({ places })` client component where `places: PlaceWithItem[]` (type imported from `@openmind/api-client` — check the exact export name in `packages/api-client` after Task 1's regen).

- [ ] **Step 1: Add dependency**

```bash
pnpm --filter web add maplibre-gl
```

- [ ] **Step 2: Proxy route**

Create `apps/web/app/api/places/route.ts` (mirror of the item-places proxy):

```ts
import { NextResponse } from "next/server";
import { apiFetch } from "../../../lib/api";

export async function GET(req: Request) {
  try {
    const res = await apiFetch("/places", undefined, req);
    return new NextResponse(res.body, {
      status: res.status,
      headers: { "content-type": res.headers.get("content-type") ?? "application/json" },
    });
  } catch (err) {
    console.error("places GET proxy failed", { err });
    return NextResponse.json({ error: "could not reach API" }, { status: 502 });
  }
}
```

- [ ] **Step 3: Map client component**

Create `apps/web/components/PlacesMap.tsx`:

```tsx
"use client";
// Full-bleed MapLibre map of every geocoded place. Client-side only — OSM
// raster tiles, no server component. Pins open a popup linking to the item.
import { useEffect, useRef } from "react";
import maplibregl from "maplibre-gl";
import "maplibre-gl/dist/maplibre-gl.css";
import { tokens } from "@openmind/ui";

export type MapPlace = {
  id: string;
  name: string;
  address: string;
  lat?: number;
  lng?: number;
  itemId: string;
  itemTitle: string;
};

const OSM_STYLE = {
  version: 8 as const,
  sources: {
    osm: {
      type: "raster" as const,
      tiles: ["https://tile.openstreetmap.org/{z}/{x}/{y}.png"],
      tileSize: 256,
      attribution: "© OpenStreetMap contributors",
    },
  },
  layers: [{ id: "osm", type: "raster" as const, source: "osm" }],
};

export function PlacesMap({ places }: { places: MapPlace[] }) {
  const container = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!container.current) return;
    const pinned = places.filter(
      (p): p is MapPlace & { lat: number; lng: number } => p.lat != null && p.lng != null,
    );
    const map = new maplibregl.Map({
      container: container.current,
      style: OSM_STYLE,
      center: pinned.length ? [pinned[0].lng, pinned[0].lat] : [0, 20],
      zoom: pinned.length ? 11 : 1.5,
    });
    map.addControl(new maplibregl.NavigationControl(), "top-right");
    const bounds = new maplibregl.LngLatBounds();
    for (const p of pinned) {
      const popup = new maplibregl.Popup({ offset: 18 }).setHTML(
        `<strong>${escapeHtml(p.name)}</strong><br/>${escapeHtml(p.address)}<br/><a href="/item/${p.itemId}">${escapeHtml(p.itemTitle || "View item")}</a>`,
      );
      new maplibregl.Marker({ color: tokens.color.cobalt }).setLngLat([p.lng, p.lat]).setPopup(popup).addTo(map);
      bounds.extend([p.lng, p.lat]);
    }
    if (pinned.length > 1) map.fitBounds(bounds, { padding: 64, maxZoom: 13 });
    return () => map.remove();
  }, [places]);

  return <div ref={container} style={{ position: "absolute", inset: 0 }} />;
}

function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`);
}
```

- [ ] **Step 4: Page**

Create `apps/web/app/places/page.tsx` — server component fetching via `apiFetch`, wrapped in `Shell` with `activePlaces`, map on top, coordinate-less places listed under a "Not on the map" heading with OSM + Google Maps search links (`https://www.openstreetmap.org/search?query=` / `https://www.google.com/maps/search/` + `encodeURIComponent(name + " " + hint)`). Follow the styling idiom of `apps/web/app/desk/page.tsx` (tokens, meta headings). Places with coordinates go to `<PlacesMap places={...} />` inside a `position: relative; flex: 1` container.

- [ ] **Step 5: Nav entry**

In `apps/web/components/Shell.tsx`: add `activePlaces?: boolean` to props, include it in the `mindActive` negation, and add after the Drift link:

```tsx
        {/* Places — every spot your saves mention, pinned on a map. */}
        <Link
          href="/places"
          style={{
            ...navBase,
            textDecoration: "none",
            background: activePlaces ? "rgba(27,63,209,.1)" : "transparent",
            color: activePlaces ? tokens.color.cobalt : tokens.color.ink,
          }}
        >
          <span style={{ fontSize: 15, width: 16 }}>⌖</span> Places
        </Link>
```

- [ ] **Step 6: Verify**

```bash
pnpm turbo run lint --filter=web && pnpm turbo run build --filter=web
```
Expected: clean. Then manually: `/places` renders the map with the Hyderabad pins against the dev stack (ask the user before starting a dev server — one is usually already running).

- [ ] **Step 7: Commit**

```bash
git add apps/web pnpm-lock.yaml
git commit -m "feat(web): Places map page, nav entry, and places proxy route"
```

---

### Task 3: Web item-detail Places rail section

**Files:**
- Modify: `apps/web/app/item/[id]/page.tsx` (Rail component ~line 184; page loader ~line 343)

**Interfaces:**
- Consumes: existing `GET /items/{id}/places` via `apiFetch`; `Place` TS type from `@openmind/api-client`.
- Produces: places section rendered in the rail when non-empty.

- [ ] **Step 1: Fetch places alongside the item**

In `ItemPage`, after loading the item:

```tsx
  const placesRes = await apiFetch(`/items/${id}/places`);
  const places: Place[] = placesRes.ok ? await placesRes.json() : [];
```

Pass `places` into `<Rail item={item} places={places} />`.

- [ ] **Step 2: Render the section**

In `Rail`, after the Provenance block (before the TagEditor divider), when `places.length > 0`:

```tsx
        <>
          {divider}
          <div className="meta" style={{ color: color.inkFaintAlt }}>Places</div>
          {places.map((p) => (
            <div key={p.id} style={{ marginTop: 9 }}>
              <div style={{ fontFamily: font.sans, fontSize: 13, fontWeight: 600, color: color.ink }}>
                {p.name}
              </div>
              {p.address ? (
                <div style={{ fontFamily: font.sans, fontSize: 12, lineHeight: 1.4, color: color.inkMuted, marginTop: 2 }}>
                  {p.address}
                </div>
              ) : null}
              <a
                href={
                  p.lat != null && p.lng != null
                    ? `https://www.google.com/maps/search/?api=1&query=${p.lat},${p.lng}`
                    : `https://www.google.com/maps/search/?api=1&query=${encodeURIComponent(`${p.name} ${p.hint}`.trim())}`
                }
                target="_blank"
                rel="noreferrer"
                style={{ fontFamily: font.mono, fontSize: 11, color: color.cobalt, textDecoration: "none" }}
              >
                Open in maps ↗
              </a>
            </div>
          ))}
        </>
```

- [ ] **Step 3: Verify + commit**

`pnpm turbo run lint --filter=web && pnpm turbo run build --filter=web` → clean; visually check the reel item's detail page shows the three restaurants.

```bash
git add apps/web/app/item
git commit -m "feat(web): places section on the item detail rail"
```

---

### Task 4: Mobile API helpers

**Files:**
- Modify: `apps/mobile/lib/api.ts`
- Modify: `apps/mobile/lib/query.tsx` (add query keys)

**Interfaces:**
- Consumes: `authHeaders`/`resolveSettings` pattern already in `api.ts`.
- Produces: `type Place = {id, name, hint, address, lat?, lng?, source}`; `type PlaceWithItem = Place & {itemId, itemTitle, itemCardType}`; `getItemPlaces(id, override?)` and `listPlaces(override?)` returning `{ok, status, places}`; query keys `queryKeys.itemPlaces(id)` and `queryKeys.places`.

- [ ] **Step 1: Add types + helpers to `api.ts`**

```ts
/** A place the pipeline extracted from an item (see GET /items/{id}/places). */
export type Place = {
  id: string;
  name: string;
  hint: string;
  address: string;
  lat?: number;
  lng?: number;
  source: string;
};

export type PlaceWithItem = Place & {
  itemId: string;
  itemTitle: string;
  itemCardType: string;
};

/** Places extracted from one item via GET {instanceUrl}/api/items/{id}/places. */
export async function getItemPlaces(
  id: string,
  override?: Settings,
): Promise<{ ok: boolean; status: number; places: Place[] }> {
  const settings = await resolveSettings(override);
  if (!settings) return { ok: false, status: 0, places: [] };
  try {
    const res = await fetch(`${settings.instanceUrl}/api/items/${id}/places`, {
      method: "GET",
      headers: authHeaders(settings.token),
    });
    let places: Place[] = [];
    if (res.ok) {
      try {
        const data = (await res.json()) as unknown;
        if (Array.isArray(data)) places = data as Place[];
      } catch {
        places = [];
      }
    }
    return { ok: res.ok, status: res.status, places };
  } catch {
    return { ok: false, status: 0, places: [] };
  }
}

/** All of the user's places via GET {instanceUrl}/api/places. */
export async function listPlaces(
  override?: Settings,
): Promise<{ ok: boolean; status: number; places: PlaceWithItem[] }> {
  const settings = await resolveSettings(override);
  if (!settings) return { ok: false, status: 0, places: [] };
  try {
    const res = await fetch(`${settings.instanceUrl}/api/places`, {
      method: "GET",
      headers: authHeaders(settings.token),
    });
    let places: PlaceWithItem[] = [];
    if (res.ok) {
      try {
        const data = (await res.json()) as unknown;
        if (Array.isArray(data)) places = data as PlaceWithItem[];
      } catch {
        places = [];
      }
    }
    return { ok: res.ok, status: res.status, places };
  } catch {
    return { ok: false, status: 0, places: [] };
  }
}
```

- [ ] **Step 2: Query keys**

In `apps/mobile/lib/query.tsx`, extend `queryKeys` with `itemPlaces: (id: string) => ["item", id, "places"] as const` and `places: ["places"] as const` (match the file's existing key style exactly).

- [ ] **Step 3: Verify + commit**

`cd apps/mobile && ./node_modules/.bin/tsc --noEmit` → clean.

```bash
git add apps/mobile/lib
git commit -m "feat(mobile): places API helpers and query keys"
```

---

### Task 5: react-native-maps + mobile Places screen + item-detail section

**Files:**
- Modify: `apps/mobile/package.json` (react-native-maps via expo install)
- Create: `apps/mobile/app/places.tsx`
- Modify: `apps/mobile/app/_layout.tsx` (register the stack screen if the layout enumerates screens; expo-router file routing may make this unnecessary — check)
- Modify: `apps/mobile/app/(tabs)/index.tsx` (Library header: map icon → `router.push("/places")`)
- Modify: `apps/mobile/app/item/[id].tsx` (places section)

**Interfaces:**
- Consumes: `listPlaces`/`getItemPlaces`/`PlaceWithItem`/`Place` from Task 4; `queryKeys` from Task 4; theme from `@/lib/theme`.
- Produces: `/places` route; places UI on item detail.

- [ ] **Step 1: Install the native module**

```bash
cd apps/mobile && ./node_modules/.bin/expo install react-native-maps
```

iOS uses Apple Maps — no key, no config-plugin entry needed. Note in the commit body that a new dev-client build is required.

- [ ] **Step 2: Places screen**

Create `apps/mobile/app/places.tsx`: a stack screen (`Stack.Screen options={{ title: "Places" }}`) with `useQuery({ queryKey: queryKeys.places, queryFn })` over `listPlaces()`; render `MapView` (from `react-native-maps`) filling the screen with a `Marker` per place with coordinates (`coordinate={{ latitude: p.lat, longitude: p.lng }}`, `title={p.name}`, `description={p.itemTitle}`, `onCalloutPress={() => router.push(`/item/${p.itemId}`)}`). Compute an initial region from the first pinned place (`latitudeDelta: 0.3, longitudeDelta: 0.3`) or a world view when none. Below the map (or as a bottom sheet-style `ScrollView` max-height ~35%), list coordinate-less places, each row opening `https://www.google.com/maps/search/?api=1&query=<encoded name + hint>` via `Linking.openURL`. Pending/error states follow the `feed.tsx` pattern (ActivityIndicator / message).

- [ ] **Step 3: Entry point from Library**

In `apps/mobile/app/(tabs)/index.tsx`, add a `Pressable` in the header row (next to the existing header controls — read the file and match its layout):

```tsx
<Pressable onPress={() => router.push("/places")} hitSlop={8}>
  <Text style={{ fontFamily: fonts.sansSemiBold, fontSize: 15, color: colors.cobalt }}>⌖ Map</Text>
</Pressable>
```

- [ ] **Step 4: Item detail places section**

In `apps/mobile/app/item/[id].tsx`:
- Add `const placesQuery = useQuery({ queryKey: queryKeys.itemPlaces(itemId), enabled: itemId.length > 0, queryFn: async () => { const res = await getItemPlaces(itemId); if (!res.ok) throw new ApiError(res.status); return res.places; }, staleTime: 30_000 });`
- Pass `places={placesQuery.data ?? []}` into `Body` and render after `TagsRow`, only when non-empty:
  - When ≥1 place has coordinates: a small non-interactive map snippet:
    ```tsx
    <MapView
      style={{ height: 160, borderRadius: radius.card, marginBottom: spacing.md }}
      initialRegion={{ latitude: first.lat, longitude: first.lng, latitudeDelta: 0.08, longitudeDelta: 0.08 }}
      scrollEnabled={false} zoomEnabled={false} pitchEnabled={false} rotateEnabled={false}
    >
      {pinned.map((p) => (
        <Marker key={p.id} coordinate={{ latitude: p.lat, longitude: p.lng }} title={p.name} />
      ))}
    </MapView>
    ```
  - Then one row per place (name semibold, address muted, matching the tag row typography), tappable → `Linking.openURL(Platform.OS === "ios" ? `maps:0,0?q=${encodeURIComponent(p.name)}@${p.lat},${p.lng}` : `geo:${p.lat},${p.lng}?q=${encodeURIComponent(p.name)}`)`; coordinate-less rows fall back to the Google Maps search URL from Step 2.

- [ ] **Step 5: Verify**

`cd apps/mobile && ./node_modules/.bin/tsc --noEmit && npm run lint --if-present` → clean.
Native module means the JS-only dev client will NOT load the map. Tell the user a new dev build is needed: `eas build --profile development --platform ios` — hand all Apple credential prompts to the user (never automate Apple auth).

- [ ] **Step 6: Commit**

```bash
git add apps/mobile
git commit -m "feat(mobile): Places map screen and item-detail places section

Adds react-native-maps (native module): requires a new dev-client /
TestFlight build."
```

---

### Task 6: Deploy + live verify

- [ ] **Step 1:** Merge to main (PR per repo habit), then deploy per the standing procedure: rsync a clean `git archive main` copy to the box, `docker compose up -d --build api` then `--build web` (sequentially, never combined), `docker restart cloudflared`.
- [ ] **Step 2:** Verify live: `openmind.gilla.fun/places` shows three Hyderabad pins; the reel item's detail rail lists the restaurants; `GET /api/places` returns 200 via the web proxy.
- [ ] **Step 3:** Update `TODO.md` (Later: remove the "web/mobile Places UI + map view" line; note Android Maps key + clustering as follow-ups). Commit.
