# Omnivore JSON import — design

Date: 2026-07-14. Scope: Milestone 2 imports, final format. Slice A only:
URLs + labels via the normal capture path. Slice B (ingesting the archived
`content/*.html` bodies so dead links survive) is an explicit follow-up, not
covered here.

## Goal

`POST /import` accepts an Omnivore export zip and imports every saved page as
a pending item, with Omnivore labels preserved as user tags. Enrichment
re-fetches each URL as usual; dead URLs land as `failed` items, which the
pipeline already handles.

## Format

An Omnivore export is a zip containing:

- `metadata_<n>_to_<m>.json` — paged JSON arrays of saved-page objects. The
  fields we use: `url` (string), `title` (string), `labels` (label names — the
  export emits an array of strings, but the parser also tolerates
  `[{"name": ...}]` objects since Omnivore's API shape used objects), `state` (e.g. `"SUCCEEDED"`, `"DELETED"`).
- `content/*.html` — archived article bodies (ignored in this slice).
- `highlights/*.md` — highlights (ignored).

## Changes

### `apps/api/internal/importer` (the whole change, essentially)

- `Parse` detection order gains zip first: filename ends `.zip` **or** data
  starts with the `PK\x03\x04` local-file-header magic → `parseOmnivoreZip`.
  (Zip must be checked before the HTML/CSV heuristics — binary data could
  accidentally contain `<a `.)
- `parseOmnivoreZip` uses stdlib `archive/zip.NewReader` over the in-memory
  bytes (the handler already reads the body fully under the 16 MB cap).
  For every entry whose base name matches `metadata_*.json`:
  - decode as a JSON array of objects;
  - skip elements with `state == "DELETED"` or an empty `url`;
  - emit `Link{URL, Title, Tags: labels}`.
  All other entries (`content/`, `highlights/`, anything else) are ignored.
- Defensive caps inside the parser: skip any single zip entry whose
  uncompressed size exceeds 8 MB (metadata pages are ~100 entries, well under
  1 MB), and stop parsing once 10 000 links are collected (mirrors the
  handler's `importMaxLinks`).
- Malformed metadata JSON in one entry skips that entry, not the whole file.
- A zip with no importable links (including a non-Omnivore zip) returns the
  existing `ErrEmpty`, which the handler already maps to a 400 with a clear
  message.

### Handler, contract, store

No changes. `ImportItems` already validates URLs, de-duplicates against the
library and within the file, canonicalises tags into `user_tags`, caps at
10 000, and enqueues enrichment. `openapi.yaml` is untouched (same endpoint,
same `ImportResult`).

### Web

- `/import` page: add “Omnivore (zip export)” to the supported-sources list;
  add `.zip` to the file input `accept`.

### Docs

- `docs/self-hosting.md` import section: mention Omnivore zip exports and the
  slice-A caveat (archived bodies are not used yet; dead URLs will show as
  failed items).

## Testing

- Unit tests in `importer_test.go` with zip fixtures built in-test via
  `archive/zip.Writer`:
  - valid export: two metadata pages + a `content/` entry + a `highlights/`
    entry → links in file order, labels carried as tags, DELETED entry and
    empty-url entry skipped;
  - zip with no metadata files → `ErrEmpty`;
  - metadata entry with malformed JSON alongside a valid one → valid links
    still returned;
  - oversized entry skipped;
  - existing non-zip formats unaffected (regression: HTML/CSV/text tests stay
    green).
- Existing DB-backed handler tests already cover dedup/idempotency; no new
  DB test needed.
- E2e against the local compose stack: upload a crafted Omnivore zip to
  `POST /import`, confirm the summary counts, items created with
  `userTags` from labels, and re-import skips everything.
