# Lossless AVIF metadata stripping (Issue #7)

Date: 2026-07-17. Closes GitHub Issue #7. AVIF uploads currently 415 pending a
lossless metadata-strip implementation; EXIF/XMP/IPTC are already stripped for
JPEG/PNG/WebP/GIF in `internal/assets/strip.go`.

## Goal

Strip EXIF/XMP from AVIF uploads without recompressing pixels (lossless), then
re-add `image/avif` to the upload allowlist.

## Why it's harder than the others

AVIF is an ISOBMFF (ISO base media file format / MP4-family) container. Unlike
JPEG's APPn segments or PNG's ancillary chunks, EXIF and XMP live as *items*
inside the `meta` box, referenced by parallel tables (`iinf`, `iloc`, `iref`,
`ipma`) with byte offsets into `mdat`/`idat`. Removing an item means editing
several tables and rewriting the offsets of everything after it — not a linear
segment skip.

## Approach — hand-rolled ISOBMFF rewrite (stdlib only)

No new dependency (repo principle: justify every dependency; the other
strippers are stdlib-only).

`stripAVIF(data []byte) ([]byte, error)` in the strip.go family:
1. Parse top-level boxes (`ftyp`, `meta`, `mdat`, others) recording type,
   offset, size; support 64-bit `largesize`.
2. Inside `meta`, parse `iinf` → item IDs whose `item_type` is `Exif` or
   `mime` with content-type `application/rdf+xml` (XMP). Collect their IDs.
3. Remove those items from `iinf` (decrement `entry_count`), `iloc` (drop
   their extents), `iref` (drop references to/from them), and `ipma` (drop
   their property associations). Leave `pitm` (primary item) and its `ipco`
   properties untouched.
4. Drop the removed items' byte ranges from `mdat`/`idat`, then rewrite every
   surviving `iloc` extent offset to account for the removed bytes and any
   `meta`-box size delta. Rewrite all affected box sizes up the tree.
5. If any structure is unrecognised or offsets don't reconcile, return an
   error (caller fails closed — see below). Pixel item bytes are copied
   verbatim → lossless.

Idempotent: a second pass finds no Exif/mime items and returns byte-identical
output.

## Wiring

- `internal/assets`: dispatch `image/avif` → `stripAVIF` alongside the
  existing type switch.
- Re-add `image/avif` to the upload MIME allowlist.
- Fail closed: if `stripAVIF` errors (malformed/hostile/unsupported box
  layout), the upload is rejected 415 — never stored with metadata intact.
  This preserves today's privacy guarantee (no unstripped image reaches disk).

## Testing

Table-driven with real AVIF fixtures (generate at test time or commit small
fixtures under `internal/assets/testdata`):
- Clean AVIF (no EXIF/XMP) → output decodes, pixels identical.
- AVIF with EXIF (incl. GPS) → EXIF item gone, image still decodes, no GPS.
- AVIF with XMP → XMP item gone, decodes.
- AVIF with both → both gone.
- Idempotency: strip twice → identical bytes.
- Truncated / oversized-box / cyclic-ref AVIF → error (→ 415), never a
  panic (the existing strippers are panic-safe; match that).
- 64-bit largesize box path.
- Handler test: AVIF upload now 201 (was 415); stored bytes carry no EXIF/XMP.

## Out of scope

Stripping metadata from AVIF image sequences / animated AVIF (treat as
unsupported → 415 for now), ICC profile removal (colour-relevant, keep),
re-encoding or thumbnail regeneration.
