package enrich_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/rohithgilla12/openmind/api/internal/ai"
	"github.com/rohithgilla12/openmind/api/internal/assets"
	"github.com/rohithgilla12/openmind/api/internal/enrich"
	"github.com/rohithgilla12/openmind/api/internal/pdftext"
	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

// fakePDF is a PDFTexter test double so enrich tests never boot the pdfium
// wasm pool.
type fakePDF struct {
	res pdftext.Result
	err error
}

func (f fakePDF) Extract(ctx context.Context, data []byte) (pdftext.Result, error) {
	return f.res, f.err
}

func newTestAssetStore(t *testing.T) *assets.FSStore {
	t.Helper()
	s, err := assets.NewFSStore(t.TempDir())
	if err != nil {
		t.Fatalf("new asset store: %v", err)
	}
	return s
}

func TestRunUploadedPDF(t *testing.T) {
	t.Run("title from extraction", func(t *testing.T) {
		s := newTestStore(t)
		assetStore := newTestAssetStore(t)
		ctx := context.Background()
		userID := uuid.New()
		if err := s.Queries.EnsureUser(ctx, userID); err != nil {
			t.Fatalf("ensure user: %v", err)
		}
		item, err := s.Queries.CreateItem(ctx, db.CreateItemParams{UserID: userID})
		if err != nil {
			t.Fatalf("create item: %v", err)
		}
		asset, err := s.Queries.CreateAsset(ctx, db.CreateAssetParams{
			UserID: userID, ItemID: pgtype.UUID{Bytes: item.ID, Valid: true},
			ContentType: "application/pdf", OriginalFilename: "upload.pdf",
		})
		if err != nil {
			t.Fatalf("create asset: %v", err)
		}
		data := []byte("%PDF-fixture-bytes")
		if _, err := assetStore.Put(asset.ID, strings.NewReader(string(data)), int64(len(data))); err != nil {
			t.Fatalf("put blob: %v", err)
		}
		if err := s.Queries.SetItemURL(ctx, db.SetItemURLParams{UserID: userID, ID: item.ID, Url: "/assets/" + asset.ID.String()}); err != nil {
			t.Fatalf("set url: %v", err)
		}
		if err := s.Queries.UpdateItemExtraction(ctx, db.UpdateItemExtractionParams{
			UserID: userID, ID: item.ID, Title: "upload", Body: "", LeadImageUrl: "", CardType: "article",
		}); err != nil {
			t.Fatalf("set metadata: %v", err)
		}

		p := &enrich.Pipeline{
			Store: s, AI: ai.NewFake(), Assets: assetStore,
			PDF: fakePDF{res: pdftext.Result{Title: "Doc", Text: "body text", Pages: 3}},
		}
		if err := p.Run(ctx, userID, item.ID); err != nil {
			t.Fatalf("first run: %v", err)
		}
		first, err := s.Queries.GetItem(ctx, db.GetItemParams{UserID: userID, ID: item.ID})
		if err != nil {
			t.Fatalf("get item: %v", err)
		}
		if first.Body != "body text" {
			t.Errorf("body = %q, want %q", first.Body, "body text")
		}
		if !first.PageCount.Valid || first.PageCount.Int32 != 3 {
			t.Errorf("page_count = %+v, want valid 3", first.PageCount)
		}
		if first.Title != "Doc" {
			t.Errorf("title = %q, want %q", first.Title, "Doc")
		}
		if first.Status != "enriched" {
			t.Errorf("status = %q, want enriched", first.Status)
		}

		// Re-run: idempotent, identical rows.
		if err := p.Run(ctx, userID, item.ID); err != nil {
			t.Fatalf("second run: %v", err)
		}
		second, err := s.Queries.GetItem(ctx, db.GetItemParams{UserID: userID, ID: item.ID})
		if err != nil {
			t.Fatalf("get item: %v", err)
		}
		if second.Body != first.Body || second.Title != first.Title || second.PageCount != first.PageCount || second.Status != first.Status {
			t.Errorf("second run changed state:\nfirst  %+v\nsecond %+v", first, second)
		}
	})

	t.Run("title falls back to upload filename", func(t *testing.T) {
		s := newTestStore(t)
		assetStore := newTestAssetStore(t)
		ctx := context.Background()
		userID := uuid.New()
		if err := s.Queries.EnsureUser(ctx, userID); err != nil {
			t.Fatalf("ensure user: %v", err)
		}
		item, err := s.Queries.CreateItem(ctx, db.CreateItemParams{UserID: userID})
		if err != nil {
			t.Fatalf("create item: %v", err)
		}
		asset, err := s.Queries.CreateAsset(ctx, db.CreateAssetParams{
			UserID: userID, ItemID: pgtype.UUID{Bytes: item.ID, Valid: true},
			ContentType: "application/pdf", OriginalFilename: "upload.pdf",
		})
		if err != nil {
			t.Fatalf("create asset: %v", err)
		}
		data := []byte("%PDF-fixture-bytes")
		if _, err := assetStore.Put(asset.ID, strings.NewReader(string(data)), int64(len(data))); err != nil {
			t.Fatalf("put blob: %v", err)
		}
		if err := s.Queries.SetItemURL(ctx, db.SetItemURLParams{UserID: userID, ID: item.ID, Url: "/assets/" + asset.ID.String()}); err != nil {
			t.Fatalf("set url: %v", err)
		}
		if err := s.Queries.UpdateItemExtraction(ctx, db.UpdateItemExtractionParams{
			UserID: userID, ID: item.ID, Title: "upload", Body: "", LeadImageUrl: "", CardType: "article",
		}); err != nil {
			t.Fatalf("set metadata: %v", err)
		}

		p := &enrich.Pipeline{
			Store: s, AI: ai.NewFake(), Assets: assetStore,
			PDF: fakePDF{res: pdftext.Result{Title: "", Text: "body text", Pages: 1}},
		}
		if err := p.Run(ctx, userID, item.ID); err != nil {
			t.Fatalf("run: %v", err)
		}
		got, err := s.Queries.GetItem(ctx, db.GetItemParams{UserID: userID, ID: item.ID})
		if err != nil {
			t.Fatalf("get item: %v", err)
		}
		if got.Title != "upload" {
			t.Errorf("title = %q, want %q (upload filename retained)", got.Title, "upload")
		}
	})
}

func TestRunUploadedPDFExtractFails(t *testing.T) {
	s := newTestStore(t)
	assetStore := newTestAssetStore(t)
	ctx := context.Background()
	userID := uuid.New()
	if err := s.Queries.EnsureUser(ctx, userID); err != nil {
		t.Fatalf("ensure user: %v", err)
	}
	item, err := s.Queries.CreateItem(ctx, db.CreateItemParams{UserID: userID})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	asset, err := s.Queries.CreateAsset(ctx, db.CreateAssetParams{
		UserID: userID, ItemID: pgtype.UUID{Bytes: item.ID, Valid: true},
		ContentType: "application/pdf", OriginalFilename: "upload.pdf",
	})
	if err != nil {
		t.Fatalf("create asset: %v", err)
	}
	data := []byte("%PDF-fixture-bytes")
	if _, err := assetStore.Put(asset.ID, strings.NewReader(string(data)), int64(len(data))); err != nil {
		t.Fatalf("put blob: %v", err)
	}
	if err := s.Queries.SetItemURL(ctx, db.SetItemURLParams{UserID: userID, ID: item.ID, Url: "/assets/" + asset.ID.String()}); err != nil {
		t.Fatalf("set url: %v", err)
	}
	if err := s.Queries.UpdateItemExtraction(ctx, db.UpdateItemExtractionParams{
		UserID: userID, ID: item.ID, Title: "upload", Body: "", LeadImageUrl: "", CardType: "article",
	}); err != nil {
		t.Fatalf("set metadata: %v", err)
	}

	failing := &enrich.Pipeline{Store: s, AI: ai.NewFake(), Assets: assetStore, PDF: fakePDF{err: errors.New("boom")}}
	if err := failing.Run(ctx, userID, item.ID); err == nil {
		t.Fatal("want error from failing extractor")
	}
	got, err := s.Queries.GetItem(ctx, db.GetItemParams{UserID: userID, ID: item.ID})
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if got.Status != "failed" {
		t.Errorf("status = %q, want failed", got.Status)
	}
	if got.Body != "" {
		t.Errorf("body = %q, want empty (extraction never persisted)", got.Body)
	}
	// The item and asset survive: a re-run with a working extractor recovers.
	if _, err := s.Queries.GetAsset(ctx, db.GetAssetParams{UserID: userID, ID: asset.ID}); err != nil {
		t.Fatalf("asset row should survive a failed extraction: %v", err)
	}

	recovering := &enrich.Pipeline{
		Store: s, AI: ai.NewFake(), Assets: assetStore,
		PDF: fakePDF{res: pdftext.Result{Title: "Doc", Text: "recovered body", Pages: 2}},
	}
	if err := recovering.Run(ctx, userID, item.ID); err != nil {
		t.Fatalf("recovery run: %v", err)
	}
	recovered, err := s.Queries.GetItem(ctx, db.GetItemParams{UserID: userID, ID: item.ID})
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if recovered.Status != "enriched" {
		t.Errorf("status = %q, want enriched after recovery", recovered.Status)
	}
	if recovered.Body != "recovered body" {
		t.Errorf("body = %q, want recovered body", recovered.Body)
	}
}

func servePDFFixture(t *testing.T, data []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRunURLPDF(t *testing.T) {
	s := newTestStore(t)
	assetStore := newTestAssetStore(t)
	ctx := context.Background()
	userID := uuid.New()
	if err := s.Queries.EnsureUser(ctx, userID); err != nil {
		t.Fatalf("ensure user: %v", err)
	}
	data := []byte("%PDF-1.4 fixture bytes for url test")
	srv := servePDFFixture(t, data)
	item, err := s.Queries.CreateItem(ctx, db.CreateItemParams{UserID: userID, Url: srv.URL, Body: ""})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	p := &enrich.Pipeline{
		Store: s, AI: ai.NewFake(), Assets: assetStore, HTTPClient: srv.Client(),
		PDF: fakePDF{res: pdftext.Result{Title: "URL Doc", Text: "url body", Pages: 4}},
	}
	if err := p.Run(ctx, userID, item.ID); err != nil {
		t.Fatalf("first run: %v", err)
	}
	got, err := s.Queries.GetItem(ctx, db.GetItemParams{UserID: userID, ID: item.ID})
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if got.Body != "url body" {
		t.Errorf("body = %q, want %q", got.Body, "url body")
	}
	if !got.PageCount.Valid || got.PageCount.Int32 != 4 {
		t.Errorf("page_count = %+v, want valid 4", got.PageCount)
	}
	if got.CardType != "article" {
		t.Errorf("card_type = %q, want article", got.CardType)
	}
	if got.Url != srv.URL {
		t.Errorf("url = %q, want unchanged %q", got.Url, srv.URL)
	}

	asset, err := s.Queries.GetAssetByItem(ctx, db.GetAssetByItemParams{UserID: userID, ItemID: pgtype.UUID{Bytes: item.ID, Valid: true}})
	if err != nil {
		t.Fatalf("expected asset row for item: %v", err)
	}
	if asset.ContentType != "application/pdf" {
		t.Errorf("asset content_type = %q, want application/pdf", asset.ContentType)
	}
	rc, err := assetStore.Open(asset.ID)
	if err != nil {
		t.Fatalf("opening stored blob: %v", err)
	}
	defer rc.Close()

	// Re-run: idempotent, no second asset row created.
	if err := p.Run(ctx, userID, item.ID); err != nil {
		t.Fatalf("second run: %v", err)
	}
	var assetCount int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE item_id = $1`, item.ID).Scan(&assetCount); err != nil {
		t.Fatalf("counting assets: %v", err)
	}
	if assetCount != 1 {
		t.Errorf("asset rows = %d, want 1 (idempotent re-run must not create a second one)", assetCount)
	}
}

// TestRunURLPDFSelfHealsOrphanedAsset simulates the first run's blob write
// having failed after the asset row was already created (e.g. a transient
// Put or SetAssetByteSize error): the row exists with byte_size 0 and no
// blob on disk. A naive idempotency gate that only checks "does an asset
// row exist for this item" would skip storage entirely on retry, extract
// from the re-fetched bytes, and mark the item enriched while its asset
// download 404s forever. The fix must instead detect the orphaned row
// (byte_size 0 / missing blob) and re-Put before enriching.
func TestRunURLPDFSelfHealsOrphanedAsset(t *testing.T) {
	s := newTestStore(t)
	assetStore := newTestAssetStore(t)
	ctx := context.Background()
	userID := uuid.New()
	if err := s.Queries.EnsureUser(ctx, userID); err != nil {
		t.Fatalf("ensure user: %v", err)
	}
	data := []byte("%PDF-1.4 fixture bytes for self-heal test")
	srv := servePDFFixture(t, data)
	item, err := s.Queries.CreateItem(ctx, db.CreateItemParams{UserID: userID, Url: srv.URL, Body: ""})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	// Simulate the "Put/SetAssetByteSize failed" half-state directly: an
	// asset row for this item with byte_size 0 and no blob on disk.
	orphan, err := s.Queries.CreateAsset(ctx, db.CreateAssetParams{
		UserID: userID, ItemID: pgtype.UUID{Bytes: item.ID, Valid: true},
		ContentType: "application/pdf", OriginalFilename: "orphan.pdf",
	})
	if err != nil {
		t.Fatalf("create orphan asset: %v", err)
	}
	if orphan.ByteSize != 0 {
		t.Fatalf("orphan asset byte_size = %d, want 0", orphan.ByteSize)
	}
	if _, err := assetStore.Open(orphan.ID); err == nil {
		t.Fatal("orphan asset blob should not exist on disk yet")
	}

	p := &enrich.Pipeline{
		Store: s, AI: ai.NewFake(), Assets: assetStore, HTTPClient: srv.Client(),
		PDF: fakePDF{res: pdftext.Result{Title: "Healed Doc", Text: "healed body", Pages: 2}},
	}
	if err := p.Run(ctx, userID, item.ID); err != nil {
		t.Fatalf("run: %v", err)
	}

	got, err := s.Queries.GetItem(ctx, db.GetItemParams{UserID: userID, ID: item.ID})
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if got.Status != "enriched" {
		t.Errorf("status = %q, want enriched", got.Status)
	}
	if got.Body != "healed body" {
		t.Errorf("body = %q, want healed body", got.Body)
	}

	healed, err := s.Queries.GetAsset(ctx, db.GetAssetParams{UserID: userID, ID: orphan.ID})
	if err != nil {
		t.Fatalf("get asset: %v", err)
	}
	if healed.ByteSize != int64(len(data)) {
		t.Errorf("asset byte_size = %d, want %d (self-heal must record the write)", healed.ByteSize, len(data))
	}
	rc, err := assetStore.Open(orphan.ID)
	if err != nil {
		t.Fatalf("blob should now exist on disk after self-heal: %v", err)
	}
	defer rc.Close()
	got2, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("reading healed blob: %v", err)
	}
	if string(got2) != string(data) {
		t.Errorf("healed blob content = %q, want %q", got2, data)
	}

	var assetCount int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE item_id = $1`, item.ID).Scan(&assetCount); err != nil {
		t.Fatalf("counting assets: %v", err)
	}
	if assetCount != 1 {
		t.Errorf("asset rows = %d, want 1 (self-heal reuses the existing row)", assetCount)
	}
}

func TestRunURLPDFTooLarge(t *testing.T) {
	s := newTestStore(t)
	assetStore := newTestAssetStore(t)
	ctx := context.Background()
	userID := uuid.New()
	if err := s.Queries.EnsureUser(ctx, userID); err != nil {
		t.Fatalf("ensure user: %v", err)
	}
	huge := append([]byte("%PDF-"), make([]byte, 11<<20)...) // > maxResponseBytes (10 MiB)
	srv := servePDFFixture(t, huge)
	item, err := s.Queries.CreateItem(ctx, db.CreateItemParams{UserID: userID, Url: srv.URL, Body: ""})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	p := &enrich.Pipeline{
		Store: s, AI: ai.NewFake(), Assets: assetStore, HTTPClient: srv.Client(),
		PDF: fakePDF{res: pdftext.Result{Title: "Big", Text: "should not be reached", Pages: 1}},
	}
	if err := p.Run(ctx, userID, item.ID); err == nil {
		t.Fatal("want error for oversize pdf response")
	}
	got, err := s.Queries.GetItem(ctx, db.GetItemParams{UserID: userID, ID: item.ID})
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if got.Status != "failed" {
		t.Errorf("status = %q, want failed", got.Status)
	}
	var assetCount int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE item_id = $1`, item.ID).Scan(&assetCount); err != nil {
		t.Fatalf("counting assets: %v", err)
	}
	if assetCount != 0 {
		t.Errorf("asset rows = %d, want 0 (oversize response must not be stored)", assetCount)
	}
}

// TestRunURLPDFTOCTOUGuard covers a server that claims PDF at HEAD-check time
// (isPDFURL) but serves non-PDF bytes to the actual GET fetched here — a
// classic TOCTOU gap. The fetch must be re-verified (status 200 + "%PDF-"
// prefix) before anything is stored: the item is failed and zero asset rows
// are created.
func TestRunURLPDFTOCTOUGuard(t *testing.T) {
	s := newTestStore(t)
	assetStore := newTestAssetStore(t)
	ctx := context.Background()
	userID := uuid.New()
	if err := s.Queries.EnsureUser(ctx, userID); err != nil {
		t.Fatalf("ensure user: %v", err)
	}
	notPDF := []byte("<html>not actually a pdf</html>")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Claims PDF regardless of method, so a prior isPDFURL HEAD check
		// would have believed it — but the body fetched here isn't one.
		w.Header().Set("Content-Type", "application/pdf")
		w.Write(notPDF)
	}))
	t.Cleanup(srv.Close)
	item, err := s.Queries.CreateItem(ctx, db.CreateItemParams{UserID: userID, Url: srv.URL, Body: ""})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	p := &enrich.Pipeline{
		Store: s, AI: ai.NewFake(), Assets: assetStore, HTTPClient: srv.Client(),
		PDF: fakePDF{res: pdftext.Result{Title: "Should not run", Text: "should not be reached", Pages: 1}},
	}
	if err := p.Run(ctx, userID, item.ID); err == nil {
		t.Fatal("want error when fetched body is not a pdf")
	}
	got, err := s.Queries.GetItem(ctx, db.GetItemParams{UserID: userID, ID: item.ID})
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if got.Status != "failed" {
		t.Errorf("status = %q, want failed", got.Status)
	}
	var assetCount int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE item_id = $1`, item.ID).Scan(&assetCount); err != nil {
		t.Fatalf("counting assets: %v", err)
	}
	if assetCount != 0 {
		t.Errorf("asset rows = %d, want 0 (non-pdf body must not be stored)", assetCount)
	}
}
