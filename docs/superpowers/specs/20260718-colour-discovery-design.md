# Colour-search discovery — teach the feature in-app

Date: 2026-07-18. Openmind already supports colour search but a new user rarely
finds it. This makes the feature teach itself through passive, always-there
in-app affordances — no modal, no multi-step tour (respects the calm-UI /
"capture is sacred" ethos).

## How colour search works today (grounding)

- Each item stores an extracted `palette` (array of hex strings).
- Colour search is a server-side **palette-proximity** match: a colour term
  (hex or named) is ranked by ΔE (CIELAB) distance against each item's palette
  (`apps/api/internal/search/color.go`). **Pure Go — no AI.**
- Web: `GET /?color=<term>` (swatch row in `SearchContext.tsx`) and the
  `color` param on search hit the ΔE matcher directly. Free-text NL colour
  ("that orange recipe") is extracted by the AI `ParseQuery` and only works
  when a provider is configured.
- Mobile: Library search calls `searchItems({ q, parse:true })`; the client
  `searchItems` already accepts a `color` param that maps to the same ΔE path.
- Palette dots already render on web cards, the web item-detail "Palette"
  rail, mobile `ItemCard`, and the mobile detail screen — but are decorative.

**Design consequence:** lean the education on the always-works path (tap a
colour → ΔE match, no AI needed). Never teach NL colour as the primary path,
since it silently no-ops under `noop`.

## What we build

### 1. Clickable palette dots — the core "aha" (web + mobile)

Turn the dots users already see everywhere into the entry point.

- **Web cards** (`components/ItemCard.tsx`): each palette dot becomes a link
  to `/?color=<hex>` (URL-encoded). Hover raises it slightly + shows a
  `title="Find saves this colour"`; keyboard-focusable with an
  `aria-label="Find saves matching <hex>"`. Clicking a dot must not also
  trigger the card's navigate-to-item — stop propagation.
- **Web item detail** (`app/item/[id]/page.tsx`, the `Palette` rail block):
  swatches become the same colour-search links, under a hint line "Tap a
  colour to find matches".
- **Mobile** (`components/ItemCard.tsx` `PaletteDots` / Library): dots on
  Library cards become tappable; a tap sets an active colour filter in the
  Library screen (`app/(tabs)/index.tsx`) that runs
  `searchItems({ color: hex })` and shows a clearable "Colour ●" chip
  (mirrors the web `?color=` echo). Tapping a dot on the mobile detail screen
  navigates back to the Library with that colour active. Detail-screen dot
  wiring is included but is the lower-priority half if effort must be cut.

### 2. One-time dismissible hint on the "Search by colour" strip (web)

`components/SearchContext.tsx` renders the swatch strip with a subtle "Search
by colour" label. Add a one-time inline hint beside it:

> New — every save keeps its colours. Tap one to find things that match.

- Dismissed permanently via `localStorage` on (a) an explicit ✕, or (b) the
  first colour search the user runs. Client-only; SSR renders without it and
  it appears after hydration to avoid a flash for returning users.
- Inline text + ✕ only — no modal, no overlay, no arrow-tour.

### 3. Colour-aware empty / thin-result copy (web)

When a colour search (`?color=` active) returns zero or very few results, the
home empty state teaches the mechanic instead of a generic "nothing found":

> Colours come from each save's palette — try a warmer or cooler shade.

## Non-goals

- No onboarding flow / product tour / modal.
- No change to the ΔE search algorithm or the palette-extraction pipeline.
- No new NL-colour promises (AI-only path stays as-is).
- No marketing/landing/docs changes (separate effort if wanted later).

## Testing

- Web: clickable dot renders an `href="/?color=..."` with the encoded hex and
  does not bubble to card navigation (component test / assertion); one-time
  hint shows when the localStorage key is absent and hides after dismiss
  (client component test or manual). Empty-state copy renders under an active
  colour param with no results.
- Mobile: tapping a Library card palette dot sets colour state and issues
  `searchItems({color})`; the colour chip clears back to the default list.
  `tsc --noEmit` clean; existing Library tests still pass.
- Verify the whole flow under the `noop` provider (must work end-to-end
  without AI) as well as with a provider.

## Rollout

Web ships independently (URL-param based, no new deps). Mobile ships in the
next dev build alongside the other pending mobile changes.
