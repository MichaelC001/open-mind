# PDF Capture Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Save a PDF by upload or by URL and get a normal enriched, searchable item with the original PDF stored as an asset.

**Architecture:** New `internal/pdftext` package wraps `go-pdfium` (pdfium compiled to WebAssembly, executed in-process by wazero — pure Go, no cgo). The `/assets` upload handler gains a PDF branch; the enrichment pipeline gains two new routes: uploaded-PDF (item `url` = `/assets/<id>`) and URL-that-serves-PDF (content-type/magic sniff → store asset → extract). Everything downstream (classify/summarise/tag/embed) is unchanged.

**Tech Stack:** Go (chi, River, sqlc/pgx, oapi-codegen), `github.com/klippa-app/go-pdfium` v1.19.x (webassembly runtime), Next.js web app, Taskfile.

**Spec:** `docs/superpowers/specs/20260713-pdf-capture-design.md` (read it first — it records the bake-off evidence and out-of-scope list).

## Global Constraints

- Capture is sacred: no extraction/AI inline in any save path; enrichment is async via River.
- Single binary: no sidecars; go-pdfium MUST use the `webassembly` runtime, never cgo.
- Every store query is `user_id`-scoped.
- Contract-first: `openapi.yaml` edited → `task generate` → never hand-edit `packages/api-client` or sqlc/oapi-codegen output.
- Errors wrapped `fmt.Errorf("doing x: %w", err)`.
- Go work runs from `apps/api`; DB-backed tests need local Postgres (`go test -p 1 ./...`).
- No OCR, no PDF metadata stripping, no thumbnails (spec out-of-scope).

---

### Task 1: `internal/pdftext` package

**Files:**
- Create: `apps/api/internal/pdftext/pdftext.go`
- Create: `apps/api/internal/pdftext/pdftext_test.go`
- Create: `apps/api/internal/pdftext/testdata/hello.pdf`
- Modify: `apps/api/go.mod` (via `go get`)

**Interfaces:**
- Produces: `pdftext.Result{Title string; Text string; Pages int}`, `pdftext.Extractor` struct with method `Extract(ctx context.Context, data []byte) (Result, error)`, constructor `pdftext.New() (*Extractor, error)`. Task 4's pipeline consumes exactly this.

- [ ] **Step 1: Add the dependency**

```bash
cd apps/api && go get github.com/klippa-app/go-pdfium@latest
```

- [ ] **Step 2: Create the fixture PDF**

Write `apps/api/internal/pdftext/testdata/hello.pdf` with this exact minimal single-page PDF (ASCII, no binary needed):

```
%PDF-1.4
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 >> endobj
3 0 obj << /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >> endobj
4 0 obj << /Length 44 >> stream
BT /F1 24 Tf 72 700 Td (Hello Openmind) Tj ET
endstream endobj
5 0 obj << /Type /Font /Subtype /Type1 /BaseFont /Helvetica >> endobj
xref
0 6
0000000000 65535 f 
trailer << /Size 6 /Root 1 0 R >>
%%EOF
```

(pdfium tolerates the truncated xref; if `OpenDocument` rejects it, regenerate the fixture with `qpdf` or Python `fpdf` — the test only requires "Hello Openmind" on one page.)

- [ ] **Step 3: Write the failing tests**

`apps/api/internal/pdftext/pdftext_test.go`:

```go
package pdftext

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func newExtractor(t *testing.T) *Extractor {
	t.Helper()
	e, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func TestExtractHelloPDF(t *testing.T) {
	data, err := os.ReadFile("testdata/hello.pdf")
	if err != nil {
		t.Fatal(err)
	}
	res, err := newExtractor(t).Extract(context.Background(), data)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !strings.Contains(res.Text, "Hello Openmind") {
		t.Errorf("text = %q, want it to contain %q", res.Text, "Hello Openmind")
	}
	if res.Pages != 1 {
		t.Errorf("pages = %d, want 1", res.Pages)
	}
}

func TestExtractCorruptBytes(t *testing.T) {
	_, err := newExtractor(t).Extract(context.Background(), []byte("%PDF-not really a pdf"))
	if err == nil {
		t.Fatal("want error for corrupt input, got nil")
	}
}

func TestExtractHonoursContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	data, _ := os.ReadFile("testdata/hello.pdf")
	if _, err := newExtractor(t).Extract(ctx, data); err == nil {
		t.Fatal("want error for expired context, got nil")
	}
}
```

- [ ] **Step 4: Run tests to verify they fail**

Run: `cd apps/api && go test ./internal/pdftext/ -v`
Expected: FAIL — `undefined: New` / `undefined: Extractor`.

- [ ] **Step 5: Implement**

`apps/api/internal/pdftext/pdftext.go`:

```go
// Package pdftext extracts plain text from PDF bytes. It is the only package
// that touches pdfium; callers see plain Go values. pdfium runs as WebAssembly
// under wazero, so the build stays pure Go (no cgo, no sidecar).
package pdftext

import (
	"context"
	"fmt"
	"time"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
)

// maxPages bounds extraction work on pathological documents. Pages beyond the
// cap are skipped; the result is truncated, never an error.
const maxPages = 200

// maxTextBytes bounds the accumulated body, mirroring the enrich package's
// 10 MB response cap.
const maxTextBytes = 10 << 20

// extractTimeout is a hard per-document ceiling: a hostile PDF must never pin
// a worker (the dslipak bake-off candidate looped for 74 minutes on a normal
// arXiv paper — that failure mode is what this guards against).
const extractTimeout = 30 * time.Second

// Result is the extracted content of one PDF.
type Result struct {
	Title string // document-metadata title, "" when absent
	Text  string // reading-order plain text of all (capped) pages
	Pages int    // total page count of the document, pre-cap
}

// Extractor owns a pdfium WebAssembly pool. Create one per process and reuse
// it; instances are checked out per call.
type Extractor struct {
	pool pdfium.Pool
}

// New initialises the pdfium wasm pool (compiles the module once).
func New() (*Extractor, error) {
	pool, err := webassembly.Init(webassembly.Config{MinIdle: 1, MaxIdle: 1, MaxTotal: 1})
	if err != nil {
		return nil, fmt.Errorf("initialising pdfium wasm pool: %w", err)
	}
	return &Extractor{pool: pool}, nil
}

// Extract pulls the title, plain text and page count out of data. It respects
// ctx and additionally enforces its own 30s ceiling. Corrupt input returns an
// error; an empty-text (e.g. scanned) PDF returns Result{Text: ""} and no error.
func (e *Extractor) Extract(ctx context.Context, data []byte) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, extractTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	instance, err := e.pool.GetInstance(extractTimeout)
	if err != nil {
		return Result{}, fmt.Errorf("acquiring pdfium instance: %w", err)
	}
	defer instance.Close()

	type outcome struct {
		res Result
		err error
	}
	done := make(chan outcome, 1)
	go func() { done <- outcome{} /* replaced below */ }()
	// NOTE to implementer: run the body below inside the goroutine and select
	// on ctx.Done() vs done, so a wedged wasm call cannot block the worker
	// beyond the timeout. On ctx timeout, also instance.Kill() to reclaim it.
	res, err := e.extract(instance, data)
	if err != nil {
		return Result{}, err
	}
	return res, ctx.Err()
}

func (e *Extractor) extract(instance pdfium.Pdfium, data []byte) (Result, error) {
	doc, err := instance.OpenDocument(&requests.OpenDocument{File: &data})
	if err != nil {
		return Result{}, fmt.Errorf("opening pdf: %w", err)
	}
	defer instance.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc.Document})

	var out Result
	if meta, err := instance.GetMetaData(&requests.GetMetaData{Document: doc.Document}); err == nil {
		for _, tag := range meta.Tags {
			if tag.Tag == "Title" {
				out.Title = tag.Value
			}
		}
	}
	pc, err := instance.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: doc.Document})
	if err != nil {
		return Result{}, fmt.Errorf("counting pages: %w", err)
	}
	out.Pages = pc.PageCount

	var text []byte
	for i := 0; i < pc.PageCount && i < maxPages && len(text) < maxTextBytes; i++ {
		pt, err := instance.GetPageText(&requests.GetPageText{
			Page: requests.Page{ByIndex: &requests.PageByIndex{Document: doc.Document, Index: i}},
		})
		if err != nil {
			continue // one bad page never kills the document
		}
		if len(text) > 0 {
			text = append(text, '\n', '\n')
		}
		text = append(text, pt.Text...)
	}
	if len(text) > maxTextBytes {
		text = text[:maxTextBytes]
	}
	out.Text = string(text)
	return out, nil
}
```

Clean up the placeholder goroutine sketch: the final code must actually run `e.extract` in the goroutine with a `select { case o := <-done: ...; case <-ctx.Done(): instance.Kill(); return Result{}, ctx.Err() }`. `instance.Kill()` exists on the pdfium instance API; if unavailable in this version, use `instance.Close()` and document that a timed-out instance is abandoned.

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd apps/api && go test ./internal/pdftext/ -v`
Expected: PASS (first run is slower — wasm compile).

- [ ] **Step 7: Commit**

```bash
git add apps/api/go.mod apps/api/go.sum apps/api/internal/pdftext
git commit -m "feat(api): pdftext package — PDF text extraction via pdfium wasm"
```

---

### Task 2: Schema + contract (`page_count`, `pageCount`, store queries)

**Files:**
- Create: `apps/api/internal/store/migrations/0012_pdf.sql`
- Modify: `apps/api/internal/store/queries/items.sql` (or the file holding item queries — check `apps/api/internal/store/queries/`)
- Modify: `openapi.yaml` (Item schema)
- Modify: `apps/api/internal/api/server.go` (or wherever `toAPIItem` lives — `grep -rn "func toAPIItem" apps/api/internal/api`)
- Generated: sqlc output + `packages/api-client` via `task generate`

**Interfaces:**
- Produces: SQL queries `SetItemURL(user_id, id, url)` and `SetItemPageCount(user_id, id, page_count)`; API `Item.pageCount *int` (nullable). Tasks 3–5 consume these.

- [ ] **Step 1: Migration**

`apps/api/internal/store/migrations/0012_pdf.sql`:

```sql
ALTER TABLE items ADD COLUMN page_count int;
```

(Column appended last so `SELECT *` ordinal order for existing columns is unchanged; sqlc regeneration handles the rest.)

- [ ] **Step 2: Store queries**

Append to the items query file:

```sql
-- name: SetItemURL :exec
UPDATE items SET url = $3 WHERE user_id = $1 AND id = $2;

-- name: SetItemPageCount :exec
UPDATE items SET page_count = $3 WHERE user_id = $1 AND id = $2;
```

- [ ] **Step 3: Contract**

In `openapi.yaml` `Item` properties (after `pinnedAt`):

```yaml
        pageCount: { type: integer, nullable: true, description: "Page count when the item's original is a stored PDF; null otherwise." }
```

- [ ] **Step 4: Regenerate + apply**

Run: `task generate && task migrate`
Expected: sqlc emits `SetItemURL`/`SetItemPageCount`; TS `Item` gains `pageCount?`. Touch nothing by hand in generated dirs.

- [ ] **Step 5: Map the field**

In `toAPIItem` (and the detail mapper if separate), copy `page_count` into `PageCount` the same way `PinnedAt` is handled (nullable pgtype → pointer). Search-result row mappers (`ftsRowToItem`/`vecRowToItem` in `internal/search/search.go`) may leave it null — add a one-line comment there saying so.

- [ ] **Step 6: Build + test + commit**

Run: `cd apps/api && go build ./... && go test -p 1 ./internal/store/... && cd ../.. && pnpm turbo run build --filter=web`
Expected: green.

```bash
git add openapi.yaml apps/api/internal/store packages/api-client apps/api/internal/api
git commit -m "feat(api): items.page_count column + pageCount contract field"
```

---

### Task 3: Upload path — `POST /assets` accepts PDFs

**Files:**
- Modify: `apps/api/internal/api/assets.go`
- Test: `apps/api/internal/api/assets_test.go`

**Interfaces:**
- Consumes: `SetItemURL` (Task 2).
- Produces: an uploaded PDF creates an item with `url = "/assets/<asset-id>"`, `card_type = "article"`, empty body/leadImage, title = filename stem; asset row `content_type = "application/pdf"`. Task 4 routes on the `/assets/` url prefix.

- [ ] **Step 1: Write the failing test**

Add to `assets_test.go`, following the existing upload-test helpers (multipart builder, test server, auth):

```go
func TestCreateAssetPDF(t *testing.T) {
	// minimal valid-enough PDF: sniffing only needs the magic bytes
	pdfBytes := []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF")
	// build multipart with filename "paper.pdf" (reuse the existing helper used
	// by TestCreateAsset for images) and POST /assets with a valid token.
	// Assert:
	//  - 201
	//  - response item.url == "/assets/"+<asset id from response leadImageUrl-less lookup>
	//  - response item.cardType == "article"
	//  - response item.leadImageUrl == ""
	//  - asset row content_type == "application/pdf"
	//  - GET /assets/<id> returns the exact bytes with Content-Type application/pdf
	//    (NOT stripped/transformed) and X-Content-Type-Options: nosniff
	//  - exactly one river enrich job enqueued (same assertion style as the image test)
}
```

Flesh the comment skeleton into real assertions by copying the structure of the existing image-upload test in the same file (it already covers 201 + asset row + job; mirror it).

- [ ] **Step 2: Run it to verify it fails**

Run: `cd apps/api && go test -p 1 ./internal/api/ -run TestCreateAssetPDF -v`
Expected: FAIL — 415 `unsupported image type`.

- [ ] **Step 3: Implement the PDF branch**

In `assets.go`:

1. Detection: `http.DetectContentType` returns `application/pdf` for `%PDF-` — add it to the decision, but keep image and PDF allowlists separate:

```go
// allowedUploadTypes: images (stripped) + PDF (stored verbatim; metadata
// stripping for PDFs is explicitly out of scope — documented in self-hosting).
func isPDF(ct string) bool { return ct == "application/pdf" }
```

In `CreateAsset`, after sniffing:

```go
contentType := detectImageType(head)
_, isImage := allowedImageTypes[contentType]
if !isImage && !isPDF(contentType) {
	writeError(w, http.StatusUnsupportedMediaType, "unsupported file type")
	return
}
```

(Change the 415 message to `unsupported file type`; update the existing AVIF-rejection test string if it asserts the old message.)

2. Skip stripping for PDFs: `stripped := data; if isImage { stripped, err = assets.StripMetadata(...) ... }`.

3. After the blob is stored, branch the item metadata write:

```go
if isPDF(contentType) {
	if err := s.store.Queries.SetItemURL(ctx, db.SetItemURLParams{
		UserID: uid, ID: item.ID, Url: "/assets/" + asset.ID.String(),
	}); err != nil { /* 500 as the image path does */ }
	if err := s.store.Queries.UpdateItemExtraction(ctx, db.UpdateItemExtractionParams{
		UserID: uid, ID: item.ID,
		Title: filenameStem(header.Filename), Body: "", LeadImageUrl: "", CardType: "article",
	}); err != nil { /* 500 */ }
} else {
	// existing image branch unchanged
}
```

- [ ] **Step 4: Run the tests**

Run: `cd apps/api && go test -p 1 ./internal/api/ -run 'TestCreateAsset|TestGetAsset' -v`
Expected: PASS, including the pre-existing image tests.

- [ ] **Step 5: Commit**

```bash
git add apps/api/internal/api/assets.go apps/api/internal/api/assets_test.go
git commit -m "feat(api): POST /assets accepts application/pdf"
```

---

### Task 4: Pipeline — uploaded-PDF and URL-PDF enrichment routes

**Files:**
- Create: `apps/api/internal/enrich/pdf.go`
- Create: `apps/api/internal/enrich/pdf_test.go`
- Modify: `apps/api/internal/enrich/pipeline.go` (routing in `Run`, new fields)
- Modify: wherever `Pipeline` is constructed (grep `enrich.Pipeline{` — the worker wiring in `cmd/openmind` or `internal/jobs`) to inject the pdftext extractor and asset store.

**Interfaces:**
- Consumes: `pdftext.New()/Extract` (Task 1), `SetItemPageCount` (Task 2), `/assets/`-url items (Task 3), existing `p.Assets *assets.FSStore`, `p.enrichText`, `SafeHTTPClient`.
- Produces: `Pipeline.PDF PDFTexter` field where `type PDFTexter interface { Extract(ctx context.Context, data []byte) (pdftext.Result, error) }` — tests use a fake.

- [ ] **Step 1: Write the failing tests**

`pdf_test.go`, in the style of `pipeline_test.go` (DB-backed, fake AI provider):

```go
type fakePDF struct{ res pdftext.Result; err error }

func (f fakePDF) Extract(ctx context.Context, data []byte) (pdftext.Result, error) {
	return f.res, f.err
}

// TestRunUploadedPDF: create item+asset rows as Task 3's handler would
// (url="/assets/<id>", card_type article, blob = fixture bytes in a temp
// FSStore), run the pipeline with fakePDF{res: {Title:"Doc", Text:"body text", Pages:3}}.
// Assert: item body == "body text", page_count == 3, title stays the upload
// filename when fake Title is "" (second subtest) or becomes "Doc" when set,
// status enriched. Run the pipeline TWICE and assert identical rows (idempotency).

// TestRunUploadedPDFExtractFails: fakePDF{err: errors.New("boom")} → item
// keeps empty body, status "enriched" is NOT set... decide: mirror the
// uploaded-image philosophy — extraction failure marks status "failed" but the
// item and asset survive; assert exactly that and that a re-run with a working
// extractor recovers to enriched.

// TestRunURLPDF: httptest server serving the pdftext fixture bytes with
// Content-Type application/pdf; item with url = server.URL. Run pipeline with
// fakePDF success. Assert: an asset row now exists for the item with
// content_type application/pdf, blob stored, body/page_count set, card_type
// "article". Re-run: no second asset row (idempotency — re-use the existing
// asset for this item if one exists).

// TestRunURLPDFTooLarge: server serves > maxResponseBytes of %PDF- prefixed
// junk → item status failed, no asset row.
```

Write these as real tests (the comments define the exact assertions).

- [ ] **Step 2: Run to verify they fail**

Run: `cd apps/api && go test -p 1 ./internal/enrich/ -run 'PDF' -v`
Expected: FAIL — routing not implemented (uploaded-PDF items fall into the note branch, URL PDFs go to trafilatura).

- [ ] **Step 3: Implement `pdf.go`**

```go
package enrich

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rohithgilla12/openmind/api/internal/pdftext"
	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

// PDFTexter is the slice of pdftext.Extractor the pipeline needs; tests
// substitute a fake so enrich tests never boot wasm.
type PDFTexter interface {
	Extract(ctx context.Context, data []byte) (pdftext.Result, error)
}

// isPDFURL sniffs whether url serves a PDF: a HEAD Content-Type of
// application/pdf, or (when HEAD is inconclusive) a ranged GET whose first
// bytes are "%PDF-". Inconclusive/never errors — falls through to normal
// extraction, mirroring isImageURL.
func isPDFURL(ctx context.Context, c *http.Client, url string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return false
	}
	resp, err := c.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/pdf") {
		return true
	}
	if ct != "" && ct != "application/octet-stream" {
		return false
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Range", "bytes=0-7")
	resp, err = c.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	head, _ := io.ReadAll(io.LimitReader(resp.Body, 8))
	return bytes.HasPrefix(head, []byte("%PDF-"))
}

// runUploadedPDF enriches an uploaded PDF item (url = "/assets/<uuid>"): read
// the stored blob, extract text, persist body + page count, then the shared
// enrichment tail. Idempotent — re-running re-extracts to the same state.
func (p *Pipeline) runUploadedPDF(ctx context.Context, userID uuid.UUID, item db.Item) error {
	q := p.Store.Queries
	fail := func(stage string, err error) error {
		if serr := q.SetItemStatus(ctx, db.SetItemStatusParams{UserID: userID, ID: item.ID, Status: "failed"}); serr != nil {
			return fmt.Errorf("marking failed after %s error %v: %w", stage, err, serr)
		}
		return fmt.Errorf("%s: %w", stage, err)
	}
	if p.PDF == nil || p.Assets == nil {
		return fail("pdf extraction", fmt.Errorf("pdf support not configured"))
	}
	assetID := strings.TrimPrefix(item.Url, "/assets/")
	data, err := p.Assets.Read(uuid.MustParse(assetID)) // match FSStore's actual read API
	if err != nil {
		return fail("reading pdf asset", err)
	}
	res, err := p.PDF.Extract(ctx, data)
	if err != nil {
		return fail("extracting pdf text", err)
	}
	title := item.Title
	if res.Title != "" {
		title = res.Title
	}
	if err := q.UpdateItemExtraction(ctx, db.UpdateItemExtractionParams{
		UserID: userID, ID: item.ID,
		Title: title, Body: res.Text, LeadImageUrl: "", CardType: "article",
	}); err != nil {
		return fmt.Errorf("saving pdf extraction: %w", err)
	}
	if err := q.SetItemPageCount(ctx, db.SetItemPageCountParams{
		UserID: userID, ID: item.ID, PageCount: pgtype.Int4{Int32: int32(res.Pages), Valid: true},
	}); err != nil {
		return fmt.Errorf("saving pdf page count: %w", err)
	}
	return p.enrichText(ctx, userID, item.ID, title, res.Text)
}

// runURLPDF enriches a saved URL that serves a PDF: fetch (size-capped), store
// as an asset (re-using this item's existing pdf asset on re-run), extract,
// persist. The original URL stays the item's url; the asset is reachable from
// the web detail page via the item→asset relation.
func (p *Pipeline) runURLPDF(ctx context.Context, userID uuid.UUID, item db.Item) error {
	// fetch with p.httpClient(), io.LimitReader(maxResponseBytes+1); over-cap or
	// fetch error → SetItemStatus failed (same fail helper shape as above).
	// Asset row: look up an existing asset for this item (add sqlc query
	// GetAssetByItem if none exists — user_id + item_id scoped); create row +
	// Put blob only when absent, so re-runs are idempotent.
	// Then: same extract/persist/enrichText tail as runUploadedPDF, except
	// title falls back to the URL's filename stem when both res.Title and
	// item.Title are empty. Card type: "article".
	panic("implement following runUploadedPDF; shared tail may be factored into a helper")
}
```

Implement `runURLPDF` fully (no panic left); factor the shared persist/enrich tail into an unexported helper `persistPDF(ctx, userID, item, res, title)` used by both. If `FSStore` has no `Read(uuid)` method, use whatever the palette stage uses to read blobs (see `palette.go`) and match it. Add the `GetAssetByItem` sqlc query if needed (append to the queries file + `task generate` — mention in the commit).

- [ ] **Step 4: Wire routing in `Run`**

In `pipeline.go`, ordered:

```go
// after the uploaded-image branch, before the note branch:
if strings.HasPrefix(item.Url, "/assets/") {
	return p.runUploadedPDF(ctx, userID, item)
}
// after the isImageURL branch, before p.Extractor.Extract:
if p.PDF != nil && isPDFURL(ctx, p.httpClient(), item.Url) {
	return p.runURLPDF(ctx, userID, item)
}
```

Add the field to `Pipeline`:

```go
// PDF extracts text from PDF bytes (uploaded or fetched). When nil, PDF
// routing is disabled and PDF URLs fall through to the normal extractor.
PDF PDFTexter
```

Wire the real extractor where the worker builds the Pipeline: `pdfx, err := pdftext.New()` at startup, log-and-continue with `nil` on error (PDF support degrades, app still works).

- [ ] **Step 5: Run the tests**

Run: `cd apps/api && go test -p 1 ./internal/enrich/ -v`
Expected: all PASS, including pre-existing pipeline tests (routing order must not break notes/images).

- [ ] **Step 6: Commit**

```bash
git add apps/api/internal/enrich apps/api/internal/store apps/api/cmd apps/api/internal/jobs packages/api-client
git commit -m "feat(enrich): PDF enrichment routes — uploaded assets and PDF URLs"
```

---

### Task 5: Web — PDF affordances

**Files:**
- Modify: `apps/web/components/ImageDrop.tsx` (accept PDFs; rename copy from "image" to "file" where shown)
- Modify: `apps/web/app/item/[id]/page.tsx` (meta line)
- Modify: `apps/web/components/ImportForm.tsx` (supported-sources copy mentions PDF upload lives on the composer)

**Interfaces:**
- Consumes: `Item.pageCount` from the regenerated client; items whose `url` starts with `/assets/`.

- [ ] **Step 1: Accept PDFs in the drop zone**

In `ImageDrop.tsx`: `accept="image/*,application/pdf"`; any user-visible "image" copy becomes "image or PDF". The existing POST to `/api/assets` needs no change (same multipart field).

- [ ] **Step 2: Detail-page meta line**

In `app/item/[id]/page.tsx`, where the mono meta line renders, add (matching existing meta-segment styling exactly):

```tsx
{item.pageCount != null && <span>PDF · {item.pageCount} {item.pageCount === 1 ? "page" : "pages"}</span>}
```

"Open original ↗" already links `item.url`; for uploaded PDFs that is `/assets/<id>` and streams with the correct content-type (Task 3) — verify the anchor doesn't force `target` handling that breaks same-origin asset paths.

- [ ] **Step 3: Import page copy**

Add "PDF (drop it on the home-page capture box)" to `ImportForm.tsx`'s supported-sources list.

- [ ] **Step 4: Verify + commit**

Run: `pnpm turbo run lint build --filter=web`
Expected: green.

```bash
git add apps/web
git commit -m "feat(web): PDF upload accept, page-count meta line, import copy"
```

---

### Task 6: Docs, e2e verification, TODO

**Files:**
- Modify: `docs/self-hosting.md` (PDF capture section)
- Modify: `TODO.md`

- [ ] **Step 1: Docs**

Add a **PDF capture** subsection to `docs/self-hosting.md`: upload or save a PDF URL; text extracted in-process by pdfium (wasm — no extra service); scanned/image-only PDFs store fine but have no searchable body (no OCR, by design); PDF metadata is NOT stripped (unlike images); same upload size cap as images.

- [ ] **Step 2: Compose e2e**

```bash
docker compose up -d --build api web
# upload: real small PDF (curl multipart to :3000/api/assets with cookie/bearer)
#   → 201, item url=/assets/<id>; within ~10s GET /items shows status enriched,
#     body non-empty, pageCount set; GET /search?q=<word from the pdf> finds it.
# url-save: POST /api/items {"url":"https://arxiv.org/pdf/1706.03762"}
#   → 201 instantly (capture is sacred); after enrichment: body contains
#     "Attention", pageCount 15, an asset row exists; open original still the arxiv URL.
# failure: POST a URL that 404s → item status failed, save unaffected.
```

Record results honestly (what was verified vs not — per repo convention in TODO.md Done entries).

- [ ] **Step 3: Update TODO.md + commit**

Move "Imports — PDF capture" out of the Next bullet (leave Omnivore JSON in it); add the Done entry with the e2e evidence and the bake-off note.

```bash
git add docs/self-hosting.md TODO.md
git commit -m "docs: PDF capture — self-hosting section + TODO"
```
