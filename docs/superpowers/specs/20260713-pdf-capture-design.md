# PDF capture — design

2026-07-13. Milestone 2 "imports — remaining formats" slice.

## Goal

Save a PDF into Openmind two ways — upload it, or save a URL that serves a PDF — and get a normal enriched item: extracted text body, summary, tags, embedding, searchable. The original PDF is stored as an asset and always openable.

## Decisions (with evidence)

- **Text extraction: `github.com/klippa-app/go-pdfium` via its WebAssembly runtime** (pdfium compiled to wasm, executed in-process by wazero). Pure-Go build, no cgo, no sidecar — single-binary self-hosting preserved. BSD-3.
- Chosen by bake-off (2026-07-13, three real PDFs: W3C dummy, BIS Annual Report 2025, arXiv 1706.03762):
  - `dslipak/pdf`: clean text on the trivial file, **0 chars** on the BIS report, **infinite loop** (74 min CPU before kill) on the arXiv paper — disqualifying, a hostile PDF would peg a worker core.
  - `pdfcpu`: has no text-extraction API; `ExtractContent` returns raw content-stream operators (hex glyph indices on subsetted fonts) — unusable for a body.
  - `go-pdfium` wasm: perfect reading-order prose on all three, 1.5–25 ms for 3 pages.
- **Cost accepted**: ~10 MB binary growth (embedded wasm) + a pooled pdfium instance's memory in the worker.
- **No new card type.** PDFs become `type: article` (or whatever classify decides from the text). YAGNI on a `pdf` type; the meta line carries the format signal.

## Architecture

### `internal/pdftext` (new package)

The only place pdfium types appear.

- `Extract(ctx context.Context, data []byte) (Result, error)` where `Result{Text string, Pages int}`.
- One lazily-initialised wasm pool (`MinIdle:1, MaxIdle:1, MaxTotal:1`) shared per process; instance checkout per call.
- Guards: hard timeout via ctx (default 30 s), page cap (default 200), text cap (reuse the extractor's existing body cap). Exceeding a cap truncates, never errors.
- Errors are ordinary wrapped errors; a corrupt PDF returns `(Result{}, err)`.

### Upload path (`POST /assets`)

- `application/pdf` joins the sniff allowlist (`%PDF-` magic; `http.DetectContentType` already recognises it). No metadata stripping (out of scope; PDFs keep their metadata — documented).
- Stored via the existing asset store; item created exactly like image upload does today, but `leadImageUrl` stays empty and the item records the asset as its original (`url` = `/assets/<id>` as for images).
- Size cap: the existing asset upload limit applies unchanged.
- Enrichment queued as normal (capture returns instantly; extraction is async).

### URL path (extract stage)

In `internal/enrich` extract: after fetch, if `Content-Type` is `application/pdf` or the first bytes sniff as `%PDF-`:

1. Save the fetched bytes as an asset (same size cap; over-cap → treat as extraction failure, keep the item with no body).
2. Point the item's stored asset reference at it.
3. Body = `pdftext.Extract` output instead of trafilatura; title = PDF title metadata if present, else filename from the URL.

SSRF safety: unchanged — same `SafeHTTPClient` fetch already in place.

### Enrichment pipeline

For both paths the stage after extract is identical to today: classify → summarise → tag → embed on the extracted text. Idempotent: re-running the job re-extracts from the stored asset (upload) or re-fetches (URL) and converges to the same result. Empty text (scanned/image-only PDF — no OCR, deliberately) → item stays enriched-with-no-body; web shows the open-original fallback. Extraction failure never blocks or corrupts the save.

### Web

- Detail page meta line gains `PDF · <n> pages` when the item's asset is a PDF.
- "Open original ↗" points at `/assets/<id>` (served with `Content-Type: application/pdf`, `nosniff` already set).
- `/import` page copy updated to mention PDF upload; the upload control itself is the existing asset-upload affordance extended to accept `.pdf`.

### Contract

- `openapi.yaml`: `/assets` request description/allowlist notes PDF; `Item` gains nullable `pageCount` (integer) — the only schema change. Regenerate Go + TS.
- New `items.page_count int` nullable column (migration) — set by extraction, null for non-PDFs.

## Testing

- `pdftext` unit tests with small fixture PDFs (text PDF → prose; corrupt bytes → error; ctx timeout honoured).
- DB-backed handler test: upload a fixture PDF → 201, item + asset rows, enrichment job queued.
- Pipeline idempotency test: run the enrich job twice on a PDF item → same body/pageCount.
- Extract-stage test with a local test server serving `application/pdf` → asset stored, body extracted; and one serving an over-cap PDF → item saved, no body.
- e2e via compose (noop provider): upload + URL-save a real PDF, confirm searchable body and open-original.

## Out of scope

- OCR for scanned PDFs (would need a sidecar — banned).
- PDF metadata stripping.
- Omnivore JSON import (separate TODO item).
- Rendering page thumbnails (pdfium can do this later — noted as a possible follow-up, not built).
