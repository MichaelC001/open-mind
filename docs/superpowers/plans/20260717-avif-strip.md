# AVIF Metadata Strip Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Losslessly strip EXIF/XMP from AVIF uploads by rewriting the ISOBMFF item tables, then re-add `image/avif` to the upload allowlist.

**Architecture:** A new `stripAVIF` joins the format switch in `internal/assets/strip.go`. It parses the ISOBMFF box tree, removes `Exif`/XMP items from the `meta` box's item tables (`iinf`, `iloc`, `iref`, `ipma`), drops their `mdat`/`idat` bytes, and rewrites all affected offsets and box sizes. Pixel item bytes are copied verbatim.

**Tech Stack:** Go, stdlib only (`encoding/binary`) — no new dependency, matching the existing strippers.

## Global Constraints

- Stdlib only; justify any new dependency (none expected). Match the existing strippers' style in `strip.go` (bounds-check every read; return an error rather than panic on malformed input).
- Fail closed: a `stripAVIF` error must prevent storage. The existing handler maps a `StripMetadata` error to **400 "could not process image"** (not 415 — 415 is for types not on the allowlist). This plan follows the existing 400 behaviour; the design doc's "415" refers loosely to rejection.
- No banner-style comments.
- Panic-safety: malformed/hostile input returns an error, never panics (the existing strippers are panic-safe by bounds-checking; a table-driven fuzz-ish test asserts this).
- Losslessness: bytes of the primary image item's payload must be identical in input and output.

## Background: the box formats you touch

ISOBMFF box = `size(4 BE) + type(4)`; `size==1` means a 64-bit `largesize(8)` follows the type; `size==0` means "to EOF". Boxes you must understand (all big-endian):
- `ftyp` — copied verbatim.
- `meta` — a FullBox (1-byte version + 3-byte flags after the header) containing child boxes: `hdlr`, `pitm`, `iinf`, `iloc`, `iref`, `ipma`/`iprp`, `idat`, etc.
- `iinf` (FullBox) → count + `infe` entries; each `infe` (FullBox, v2/v3) carries `item_ID(2 or 4)` and a 4-char `item_type` (`Exif`, `mime`, `av01`, `grid`, …). For `mime`, the null-terminated `content_type` string identifies XMP (`application/rdf+xml`).
- `iloc` (FullBox) → per-item extent lists with `base_offset` + `(offset,length)` extents pointing into `mdat`/`idat`. Offset/length field sizes are declared in the iloc header nibbles.
- `iref` (FullBox) → references between items (drop any to/from a removed item).
- `ipma` (FullBox) → item→property associations (drop entries for removed items).
- `mdat` — raw item payloads concatenated; removing an item means excising its extent bytes and shifting subsequent `iloc` offsets.

Target items to remove: `item_type == "Exif"`, and `item_type == "mime"` whose content_type is XMP. Never touch the `pitm` primary item or `av01`/`grid` image items.

---

### Task 1: ISOBMFF box walker + metadata-item finder

**Files:**
- Create: `apps/api/internal/assets/avif.go`
- Test: `apps/api/internal/assets/avif_test.go`

**Interfaces:**
- Produces (unexported, in package `assets`):
  - `type box struct { typ string; start, headerLen, size int }` (size = full box size incl. header; start = offset in data)
  - `func walkBoxes(data []byte, start, end int) ([]box, error)` — parses sibling boxes in `[start,end)`, bounds-checked, supporting 32/64-bit sizes and `size==0` (to end).
  - `func findAVIFMetadataItems(data []byte) (ids map[uint32]bool, err error)` — returns the set of item IDs whose type is `Exif` or XMP `mime`, by locating `meta`→`iinf`→`infe`.
- A synthetic-fixture builder in the test file so tests are hermetic (no external AVIF tooling needed): `func buildAVIF(t *testing.T, items []testItem) []byte` assembling `ftyp` + `meta`(`hdlr`,`pitm`,`iinf`,`iloc`) + `mdat`.

- [ ] **Step 1: Write the failing test**

Create `apps/api/internal/assets/avif_test.go` with a fixture builder and tests. The builder assembles a minimal but spec-valid AVIF: `ftyp` (brand `avif`), a `meta` box containing `hdlr`, `pitm` (→ the av01 item), `iinf` with one `infe` per item, and `iloc` pointing each item at bytes in a trailing `mdat`. Include:
- `TestWalkBoxes`: a hand-built buffer of two boxes → correct types/sizes; a truncated box → error; a 64-bit-largesize box → parsed.
- `TestFindAVIFMetadataItems`: fixture with an `av01` item + an `Exif` item + an XMP `mime` item → returns exactly the Exif and XMP item IDs; fixture with only `av01` → empty set; truncated `iinf` → error.

Write the builder and assertions concretely (real byte assembly, real ID assertions). This test file's builder is reused by Task 2.

- [ ] **Step 2: Run to verify it fails**

Run from `apps/api`: `go test ./internal/assets/ -run 'TestWalkBoxes|TestFindAVIFMetadataItems' -v`
Expected: FAIL — undefined `walkBoxes`, `findAVIFMetadataItems`, `box`.

- [ ] **Step 3: Implement the walker + finder**

Create `apps/api/internal/assets/avif.go` implementing `walkBoxes` and `findAVIFMetadataItems` per the interfaces above. Bounds-check every field read (mirror `stripPNG`'s `end < i || end > len(data)` guards). `findAVIFMetadataItems` locates the `meta` box via `walkBoxes` on the top level, skips `meta`'s 4-byte FullBox version/flags, walks its children to `iinf`, and parses each `infe` (handle version 2 with 16-bit item_ID and version 3 with 32-bit item_ID; read the 4-char item_type; for `mime`, read the null-terminated content_type and match `application/rdf+xml`). Return an error on any short read.

- [ ] **Step 4: Run to verify it passes**

Run from `apps/api`: `go test ./internal/assets/ -run 'TestWalkBoxes|TestFindAVIFMetadataItems' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/api/internal/assets/avif.go apps/api/internal/assets/avif_test.go
git commit -m "feat(assets): ISOBMFF box walker + AVIF metadata-item finder"
```

---

### Task 2: stripAVIF — remove items and rewrite offsets

**Files:**
- Modify: `apps/api/internal/assets/avif.go`
- Test: `apps/api/internal/assets/avif_test.go` (append)

**Interfaces:**
- Consumes: `walkBoxes`, `findAVIFMetadataItems`, the test builder (Task 1).
- Produces: `func stripAVIF(data []byte) ([]byte, error)` — returns a new buffer with Exif/XMP items removed and all tables/offsets rewritten; byte-identical output when there are no such items (idempotent).

- [ ] **Step 1: Write the failing test**

Append tests using the Task 1 builder:
- `TestStripAVIF_RemovesExif`: fixture with av01 + Exif → output parses, `findAVIFMetadataItems(output)` is empty, the av01 item's payload bytes (via its rewritten iloc extent) equal the original av01 payload bytes (losslessness), and output is smaller than input by the Exif payload + table entries.
- `TestStripAVIF_RemovesXMP`: same for an XMP `mime` item.
- `TestStripAVIF_RemovesBoth`: av01 + Exif + XMP → both gone, av01 intact.
- `TestStripAVIF_CleanIsByteIdentical`: av01-only fixture → output equals input exactly.
- `TestStripAVIF_Idempotent`: strip twice → identical bytes.
- `TestStripAVIF_Malformed`: truncated meta / iloc offset past EOF / oversized box → error, no panic (wrap the call in a `func(){ defer recover-assert }` or rely on the bounds checks and assert `err != nil`).

Assert the av01 payload equality by re-parsing the output's `iloc` for the primary item and slicing `mdat` at the rewritten offset.

- [ ] **Step 2: Run to verify it fails**

Run from `apps/api`: `go test ./internal/assets/ -run TestStripAVIF -v`
Expected: FAIL — undefined `stripAVIF`.

- [ ] **Step 3: Implement stripAVIF**

Implement per the design's algorithm:
1. `walkBoxes` top level; require `ftyp` + `meta` (+ optional `mdat`/`idat`). If no `meta`, return an unchanged copy (nothing to strip).
2. `ids := findAVIFMetadataItems(data)`. If empty → return `append([]byte(nil), data...)` (byte-identical, satisfies clean + idempotent cases).
3. Otherwise rebuild: for each removed item, gather its `iloc` extents (offset,length) into `mdat`. Excise those byte ranges from `mdat`, producing a new `mdat` and a mapping old→new offset for surviving extents (each surviving offset decreases by the total removed bytes lying before it).
4. Rebuild `iinf` (drop `infe` for removed ids, decrement count), `iloc` (drop removed items' entries, rewrite surviving extents' offsets via the mapping and any base_offset delta), `iref` (drop refs mentioning removed ids), `ipma` (drop assocs for removed ids). Recompute each rewritten box's `size` field.
5. Recompute the `meta` box size and reassemble `ftyp` + new `meta` + new `mdat` (+ other top-level boxes preserved in order). If `iloc` uses `construction_method==1` (offsets into `idat` inside `meta`), handle the `idat` excision analogously; if a construction method or offset size you don't support appears, return an error (fail closed).
6. Bounds-check throughout; any inconsistency → error.

Keep `av01`/`grid`/`pitm` and all image properties untouched so pixels stay lossless.

- [ ] **Step 4: Run to verify it passes**

Run from `apps/api`: `go test ./internal/assets/ -run TestStripAVIF -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/api/internal/assets/avif.go apps/api/internal/assets/avif_test.go
git commit -m "feat(assets): lossless AVIF EXIF/XMP stripping via ISOBMFF rewrite"
```

---

### Task 3: Wire into StripMetadata + allowlist + handler test + docs

**Files:**
- Modify: `apps/api/internal/assets/strip.go` (dispatch)
- Modify: `apps/api/internal/api/assets.go` (allowlist)
- Test: `apps/api/internal/api/assets_test.go` (append)
- Modify: `docs/self-hosting.md` (AVIF now accepted)

**Interfaces:**
- Consumes: `stripAVIF` (Task 2); existing `detectAVIF` (already present in assets.go).

- [ ] **Step 1: Write the failing handler test**

Append to `apps/api/internal/api/assets_test.go`, reusing its existing multipart-upload harness. Using an AVIF fixture with EXIF (reuse the builder from `internal/assets` by exporting a tiny test helper, OR embed a committed `testdata/exif.avif`; prefer a committed fixture generated with `ffmpeg -i x.png x.avif && exiftool -GPSLatitude=51.5 x.avif` — document the command in `testdata/README.md`):
- AVIF upload → 201 (previously 415).
- The stored bytes contain no `Exif` item (call `assets.StripMetadata("image/avif", stored)` and assert it's byte-identical, i.e. already stripped / idempotent).
- A deliberately malformed AVIF (valid ftyp brand so it sniffs as avif, corrupt meta) → 400 "could not process image", and no item/asset row created.

- [ ] **Step 2: Run to verify it fails**

Run from `apps/api`: `go test ./internal/api/ -run TestCreateAsset -v` (or the new test names)
Expected: FAIL — AVIF still 415 (not in allowlist).

- [ ] **Step 3: Wire dispatch + allowlist**

In `strip.go` `StripMetadata`, add:

```go
	case "image/avif":
		return stripAVIF(data)
```

In `assets.go`, add `"image/avif": {},` to `allowedImageTypes`.

- [ ] **Step 4: Run to verify it passes**

Run from `apps/api`: `go test ./internal/api/ ./internal/assets/ -run 'TestCreateAsset|TestStripAVIF' -v`
Expected: PASS. Then `go test ./... -p 1` green (modulo known unrelated flakiness).

- [ ] **Step 5: Update docs**

In `docs/self-hosting.md`, update the image-upload section: AVIF is now accepted; EXIF/XMP stripped losslessly like JPEG/PNG/WebP (previously listed as rejected/415).

- [ ] **Step 6: Commit**

```bash
git add apps/api/internal/assets/strip.go apps/api/internal/api/assets.go apps/api/internal/api/assets_test.go apps/api/internal/api/testdata docs/self-hosting.md
git commit -m "feat(assets): re-allow AVIF uploads with lossless metadata stripping"
```

---

### Task 4: Deploy + verify

- [ ] **Step 1:** Merge to main (PR), then deploy per the standing procedure: rsync clean `git archive main` copy, `docker compose up -d --build api` (api-only), load < 8 first.
- [ ] **Step 2:** Verify live: upload an AVIF with GPS EXIF via the authenticated API (device key or local stack), download the stored asset back, and confirm no EXIF/GPS remains (e.g. `exiftool` on the downloaded bytes shows no GPS) and the image still decodes. A clean AVIF also uploads 201.
- [ ] **Step 3:** Close issue #7, update `TODO.md` (remove the AVIF Later item). Commit.
