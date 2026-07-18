# Colour-Search Discovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Teach colour search in-app by making the palette dots users already see clickable, plus a one-time hint and colour-aware empty copy.

**Architecture:** Colour search is an existing server-side ΔE palette-proximity match reached via `?color=<term>` (web) or `searchItems({color})` (mobile) — no AI. This plan adds discovery affordances only; it does not touch search or extraction. Web cards are wrapped in a full-card `<Link>`, so clickable colour dots use the stretched-link pattern (overlay anchor + dots raised above it) to avoid illegal nested anchors. Mobile uses nested `Pressable` (valid in RN).

**Tech Stack:** Next.js (App Router, server + client components), `@openmind/ui` tokens, vitest (web lib tests), Expo/React Native.

## Global Constraints

- Web styling via `tokens` from `@openmind/ui`; no hardcoded colours in app code (dynamic palette hex values are data, not theme — those are fine as inline `background`).
- Mobile styling via `colors, fonts, radius, spacing` from `@/lib/theme`.
- No banner-style comments (`// ==== X ====` or `// --- X ---`) — the maintainer forbids them.
- Colour search must work end-to-end under the `noop` AI provider (the tap-a-colour path is pure-Go ΔE; never depend on AI).
- No modal, no multi-step tour — inline affordances only.
- Do not change the ΔE search algorithm, the palette-extraction pipeline, or the API contract.
- Web verification: `pnpm turbo run lint --filter=web` (tsc) + `pnpm turbo run build --filter=web`; `pnpm --filter web test` for lib units. Do NOT start a dev server without asking (the user usually has one running); a fresh `next start` on an alt port is fine for manual checks.
- Mobile verification: `cd apps/mobile && ./node_modules/.bin/tsc --noEmit`. `npx` is shimmed — use `./node_modules/.bin`.

---

### Task 1: Pure colour-search href helper (web)

**Files:**
- Modify: `apps/web/lib/colors.ts`
- Test: `apps/web/lib/colors.test.ts` (create)

**Interfaces:**
- Produces: `export function colorSearchHref(term: string): string` — returns `/?color=<encoded term>`, used by every clickable colour affordance so URL construction is DRY and tested.

- [ ] **Step 1: Write the failing test**

Create `apps/web/lib/colors.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { colorSearchHref } from "./colors";

describe("colorSearchHref", () => {
  it("encodes a hex term with its leading #", () => {
    expect(colorSearchHref("#1B3FD1")).toBe("/?color=%231B3FD1");
  });
  it("passes a named colour through", () => {
    expect(colorSearchHref("cobalt")).toBe("/?color=cobalt");
  });
  it("encodes spaces in a named colour", () => {
    expect(colorSearchHref("dark blue")).toBe("/?color=dark%20blue");
  });
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `pnpm --filter web test -- colors.test.ts`
Expected: FAIL — `colorSearchHref` not exported.

- [ ] **Step 3: Implement**

Append to `apps/web/lib/colors.ts`:

```ts
// Home href that runs a colour search for the given term (hex like "#1B3FD1"
// or a named colour). Encoded so "#" and spaces survive the query string.
export function colorSearchHref(term: string): string {
  return `/?color=${encodeURIComponent(term)}`;
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `pnpm --filter web test -- colors.test.ts`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add apps/web/lib/colors.ts apps/web/lib/colors.test.ts
git commit -m "feat(web): colorSearchHref helper for clickable colour affordances"
```

---

### Task 2: Clickable palette dots on cards via stretched-link (web)

**Files:**
- Modify: `apps/web/app/globals.css` (add `.card-link` + `.dot-link`)
- Modify: `apps/web/components/Palette.tsx` (optional colour-link mode)
- Modify: `apps/web/components/Grid.tsx` (stretched-link wrapper)

**Interfaces:**
- Consumes: `colorSearchHref` (Task 1).
- Produces: `Palette` gains `colorLinks?: boolean`; when true each dot is a `<Link>` to `colorSearchHref(hex)` carrying `.dot-link`. Grid wraps each card in a positioned `<article>` with a `.card-link` overlay anchor.

- [ ] **Step 1: Add CSS**

In `apps/web/app/globals.css`, before the paper-texture rule, add:

```css
/* Stretched-link card: a transparent overlay anchor covers the whole card for
   navigation, while interactive children (colour dots) are raised above it. */
.card-link{position:absolute;inset:0;z-index:0}
.dot-link{position:relative;z-index:1;cursor:pointer;text-decoration:none;transition:transform .12s ease}
.dot-link:hover{transform:scale(1.35)}
.dot-link:focus-visible{outline:2px solid #1B3FD1;outline-offset:2px}
```

- [ ] **Step 2: Add colour-link mode to Palette**

Replace `apps/web/components/Palette.tsx` with (keeps the plain-span default, adds an opt-in link mode):

```tsx
import type { CSSProperties } from "react";
import Link from "next/link";
import { colorSearchHref } from "../lib/colors";

/**
 * Palette renders a row of colour dots — the signature brand detail. Each hex in
 * `colors` becomes one `.dot` span (styled in globals.css). Pass `size` to scale
 * the dots for detail-page swatches. Pass `colorLinks` to make each dot a colour
 * search link (used on cards + the detail rail so the dots teach colour search).
 */
export function Palette({
  colors,
  size,
  colorLinks,
}: {
  colors: string[];
  size?: number;
  colorLinks?: boolean;
}) {
  const style = (c: string): CSSProperties =>
    size !== undefined
      ? { background: c, width: size, height: size, borderRadius: size / 2 }
      : { background: c };
  if (colorLinks) {
    return (
      <>
        {colors.map((c, i) => (
          <Link
            key={`${c}-${i}`}
            href={colorSearchHref(c)}
            className="dot dot-link"
            style={style(c)}
            title="Find saves this colour"
            aria-label={`Find saves matching ${c}`}
          />
        ))}
      </>
    );
  }
  return (
    <>
      {colors.map((c, i) => (
        <span key={`${c}-${i}`} className="dot" style={style(c)} />
      ))}
    </>
  );
}
```

- [ ] **Step 3: Stretched-link wrapper in Grid**

In `apps/web/components/Grid.tsx`, replace the per-item `<Link>…<ItemCard/></Link>` with a positioned wrapper whose overlay anchor handles navigation, so the dots (Task 4 makes them links inside ItemCard) sit above it:

```tsx
      {items.map((item) => (
        <article key={item.id} style={{ position: "relative" }}>
          {item.pinnedAt ? (
            <span
              aria-label="On desk"
              title="On desk"
              style={{
                position: "absolute",
                top: 10,
                right: 10,
                zIndex: 1,
                width: 8,
                height: 8,
                borderRadius: "50%",
                background: tokens.color.gold,
                boxShadow: `0 0 0 2px ${tokens.color.cardSurface}`,
              }}
            />
          ) : null}
          <ItemCard item={item} />
          <Link
            href={`/item/${item.id}`}
            aria-label={item.title ?? "Open item"}
            className="card-link"
            style={{ color: "inherit", textDecoration: "none" }}
          />
        </article>
      ))}
```

Keep the empty-state branch as-is for now (Task 5 revisits it).

- [ ] **Step 4: Make ItemCard footer dots colour links**

In `apps/web/components/ItemCard.tsx`, pass `colorLinks` on every `<Palette colors={dots} />` usage (the `Footer` component's `<Palette colors={dots} />`, and the inline ones in the `quote`, `image`, and `note` branches). Example for `Footer`:

```tsx
      <Palette colors={dots} colorLinks />
```

Apply the same `colorLinks` prop to the four `<Palette colors={dots} …/>` sites. Do not change anything else.

- [ ] **Step 5: Verify**

```bash
pnpm turbo run lint --filter=web && pnpm turbo run build --filter=web
```
Expected: clean. Then manually (fresh build on an alt port, or the user's dev server): on The Mind, clicking a card's body navigates to the item; clicking a palette dot navigates to `/?color=<hex>` and shows colour results. Confirm no hydration/nested-anchor warning in the console.

- [ ] **Step 6: Commit**

```bash
git add apps/web/app/globals.css apps/web/components/Palette.tsx apps/web/components/Grid.tsx apps/web/components/ItemCard.tsx
git commit -m "feat(web): clickable palette dots on cards run a colour search"
```

---

### Task 3: Item-detail rail palette → colour links + hint (web)

**Files:**
- Modify: `apps/web/app/item/[id]/page.tsx` (the `Rail` component's Palette block)

**Interfaces:**
- Consumes: `Palette` `colorLinks` (Task 2).

- [ ] **Step 1: Make the rail swatches colour links with a hint**

In `apps/web/app/item/[id]/page.tsx`, in the `Rail` component, the Palette block currently is:

```tsx
      <div className="meta" style={{ color: color.inkFaintAlt }}>
        Palette
      </div>
      <div style={{ display: "flex", gap: 6, marginTop: 9, flexWrap: "wrap" }}>
        <Palette colors={colors} size={24} />
      </div>
```

Replace with:

```tsx
      <div className="meta" style={{ color: color.inkFaintAlt }}>
        Palette
      </div>
      <div style={{ display: "flex", gap: 6, marginTop: 9, flexWrap: "wrap" }}>
        <Palette colors={colors} size={24} colorLinks />
      </div>
      <p
        style={{
          fontFamily: font.sans,
          fontSize: 11,
          lineHeight: 1.4,
          color: color.inkFaint,
          margin: "8px 0 0",
        }}
      >
        Tap a colour to find matches.
      </p>
```

- [ ] **Step 2: Verify + commit**

```bash
pnpm turbo run lint --filter=web && pnpm turbo run build --filter=web
```
Expected: clean; the item detail rail swatches now link to `/?color=<hex>` with the hint beneath.

```bash
git add apps/web/app/item/[id]/page.tsx
git commit -m "feat(web): item-detail palette swatches run a colour search"
```

---

### Task 4: One-time dismissible colour hint (web)

**Files:**
- Create: `apps/web/components/ColourHint.tsx`
- Modify: `apps/web/components/SearchContext.tsx` (render the hint beside the "Search by colour" label)

**Interfaces:**
- Produces: `ColourHint` — a client component that shows the hint once, dismissed via `localStorage` key `openmind.colourHintDismissed` on an explicit ✕ or when a colour search is active.

- [ ] **Step 1: Create the hint component**

Create `apps/web/components/ColourHint.tsx`:

```tsx
"use client";

import { useEffect, useState } from "react";
import { tokens } from "@openmind/ui";

const KEY = "openmind.colourHintDismissed";

/**
 * One-time inline hint teaching colour search. Renders nothing on the server
 * and until mount (avoids a flash for returning users), then shows only when
 * the localStorage flag is absent. `dismissedBySearch` is passed true when a
 * colour search is already active, which also permanently dismisses it.
 */
export function ColourHint({ dismissedBySearch }: { dismissedBySearch: boolean }) {
  const [show, setShow] = useState(false);

  useEffect(() => {
    if (dismissedBySearch) {
      localStorage.setItem(KEY, "1");
      return;
    }
    if (localStorage.getItem(KEY) !== "1") setShow(true);
  }, [dismissedBySearch]);

  if (!show) return null;

  const dismiss = () => {
    localStorage.setItem(KEY, "1");
    setShow(false);
  };

  return (
    <span
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 8,
        fontFamily: tokens.font.sans,
        fontSize: 12,
        color: tokens.color.inkMuted,
      }}
    >
      New — every save keeps its colours. Tap one to find things that match.
      <button
        type="button"
        onClick={dismiss}
        aria-label="Dismiss colour hint"
        style={{
          border: "none",
          background: "transparent",
          color: tokens.color.inkFaint,
          cursor: "pointer",
          fontSize: 14,
          lineHeight: 1,
          padding: 0,
        }}
      >
        ×
      </button>
    </span>
  );
}
```

- [ ] **Step 2: Render it in SearchContext**

In `apps/web/components/SearchContext.tsx`, import the hint and render it right after the `meta` label span (only in the no-echo "Search by colour" state, so it appears beside the swatch strip on a fresh library). Pass `dismissedBySearch={Boolean(colorParam)}`:

```tsx
import { ColourHint } from "./ColourHint";
```

After the `<span className="meta">…</span>` block:

```tsx
      {!hasEcho && <ColourHint dismissedBySearch={Boolean(colorParam)} />}
```

- [ ] **Step 3: Verify + commit**

```bash
pnpm turbo run lint --filter=web && pnpm turbo run build --filter=web
```
Expected: clean. Manually: on a library with no active search, the hint shows once; clicking ✕ hides it and it stays hidden on reload (localStorage); running a colour search also dismisses it permanently.

```bash
git add apps/web/components/ColourHint.tsx apps/web/components/SearchContext.tsx
git commit -m "feat(web): one-time colour-search hint on the search-by-colour strip"
```

---

### Task 5: Colour-aware empty-state copy (web)

**Files:**
- Modify: `apps/web/components/Grid.tsx` (empty branch)
- Modify: `apps/web/app/page.tsx` (pass whether a colour filter is active)

**Interfaces:**
- Consumes: nothing new.
- Produces: `Grid` gains `colorActive?: boolean`; when true and there are no items, the empty copy teaches the colour mechanic.

- [ ] **Step 1: Vary the empty copy**

In `apps/web/components/Grid.tsx`, change the signature to `export function Grid({ items, colorActive }: { items: Item[]; colorActive?: boolean })` and in the empty branch return colour-aware copy when `colorActive`:

```tsx
  if (items.length === 0) {
    return (
      <p
        style={{
          fontFamily: tokens.font.quote,
          fontStyle: "italic",
          fontSize: "1.25rem",
          color: tokens.color.inkMuted,
          marginTop: "2rem",
        }}
      >
        {colorActive
          ? "No saves match that colour yet — colours come from each save's palette, so try a warmer or cooler shade."
          : "Nothing gathered yet — drop a link or a thought above."}
      </p>
    );
  }
```

- [ ] **Step 2: Pass colorActive from the page**

In `apps/web/app/page.tsx`, where `<Grid items={…} />` is rendered, pass `colorActive={Boolean(color)}` (the `color` search param is already in scope — it is passed to `SearchContext` as `colorParam={color}`). If the grid render site can't see `color`, thread the existing `color` param variable through.

- [ ] **Step 3: Verify + commit**

```bash
pnpm turbo run lint --filter=web && pnpm turbo run build --filter=web
```
Expected: clean. Manually: a colour search with no matches shows the colour-aware copy; the plain empty library still shows the original line.

```bash
git add apps/web/components/Grid.tsx apps/web/app/page.tsx
git commit -m "feat(web): colour-aware empty state for colour searches"
```

---

### Task 6: Mobile clickable palette dots → Library colour filter

**Files:**
- Modify: `apps/mobile/components/ItemCard.tsx` (`PaletteDots` gains an optional colour-pick handler)
- Modify: `apps/mobile/app/(tabs)/index.tsx` (Library colour-filter state + chip)

**Interfaces:**
- Consumes: `searchItems({ color })` from `@/lib/api` (already supports `color`).
- Produces: `ItemCard` gains `onPickColor?: (hex: string) => void` threaded to `PaletteDots`; when set, each dot is a `Pressable` calling `onPickColor(hex)`.

- [ ] **Step 1: Make PaletteDots tappable**

In `apps/mobile/components/ItemCard.tsx`, thread an optional `onPickColor` from `ItemCardProps` into `PaletteDots`. When provided, render each dot as a `Pressable` with `hitSlop={6}` calling `onPickColor(c)`; otherwise keep the current `View`. Add `onPickColor` to `ItemCardProps` and pass it into every `PaletteDots` usage. `Pressable` nested inside the card's `Pressable` is valid in RN and captures its own tap (add `accessibilityRole="button"`, `accessibilityLabel={`Find saves matching ${c}`}`). No banner comments.

- [ ] **Step 2: Wire Library colour state**

In `apps/mobile/app/(tabs)/index.tsx`:
- Add `const [colorFilter, setColorFilter] = useState<string | null>(null);`
- The list query: when `colorFilter` is set, key it (e.g. `queryKeys.search("color:" + colorFilter)`) and fetch `searchItems({ color: colorFilter })` mapped to items; otherwise the existing text-search / list behaviour. A colour filter takes precedence over an empty text query.
- Pass `onPickColor={(hex) => { setColorFilter(hex); setQuery(""); }}` to the `ItemCard`s rendered in the Library list.
- When `colorFilter` is set, render a small clearable chip above the list: a swatch (`backgroundColor: colorFilter`) + "Colour" + an × `Pressable` that calls `setColorFilter(null)`. Style with theme tokens.

- [ ] **Step 3: Verify**

```bash
cd apps/mobile && ./node_modules/.bin/tsc --noEmit
```
Expected: clean. (Native run happens in the next dev build; a simulator run is out of scope here.)

- [ ] **Step 4: Commit**

```bash
git add apps/mobile/components/ItemCard.tsx "apps/mobile/app/(tabs)/index.tsx"
git commit -m "feat(mobile): tap a palette dot to filter the library by colour"
```

---

### Task 7: Deploy web + verify

- [ ] **Step 1:** Merge to main (PR), deploy per standing procedure: rsync clean `git archive main` copy, `docker compose up -d --build web` (web-only — no api change), `docker restart cloudflared`, load < 8 first.
- [ ] **Step 2:** Verify live under the deployed (Clerk) instance: on The Mind, a card's palette dot navigates to `/?color=<hex>` and returns colour-ranked results; the item-detail swatches link; the one-time hint shows for a fresh browser and dismisses. Confirm it works even though the box runs the Gemini path (colour ΔE is AI-independent).
- [ ] **Step 3:** Update `TODO.md` (log the colour-discovery pass under Done; mobile dots ride the next dev build). Commit. Mobile ships with the next TestFlight build.
