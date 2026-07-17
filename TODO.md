# TODO

> Lightweight maintainer notes. The real backlog lives in
> [GitHub Issues](../../issues) — file bugs and feature requests there.

## Now
- (empty)

## Next
- (see Issues)

## Later
- Reel places Phase 2 + 4: thumbnail vision, MCP `item_places` tool, optional
  yt-dlp deep media — see `docs/superpowers/specs/20260716-reel-places-design.md`
- Places map follow-ups: marker clustering, Android Google Maps API key,
  consolidate web Place types via api-client `paths[]`, note OSM tile runtime
  dep in self-hosting docs
- Lossless AVIF metadata stripping / re-allow AVIF uploads
- Omnivore import slice B: ingest archived content from export zips so dead links survive
- Dock follow-ups: tray Desk submenu, Win/Linux tab-grab, hotkey rebinding, DMG/notarisation

## Done (recent)
- Places map on web + mobile (GET /places, /places MapLibre page, item-rail
  places, react-native-maps screen — needs new dev build) + Google Places
  geocoder (2026-07-17)
- Mobile: Desk pins + TanStack Query cache (no feed/tab reload flash) + richer
  long-press actions (pin/open/copy/share/delete) (2026-07-17)
- Mobile: delete an item (detail screen + long-press in Library) and group Library by card type (2026-07-16)
- Mobile offline capture queue + in-app Library search (2026-07-16)
- Dock v1.1 Desk/Recents home + Launch at login (2026-07-15) — see `docs/superpowers/specs/20260715-dock-desk-autostart-design.md`
