package pdftext

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

// manyPagesPDF builds a syntactically valid (if xref-truncated, same as the
// hello.pdf fixture) PDF with n pages, each carrying a short text run. A few
// hundred pages of page-by-page GetPageText round trips through the wasm
// boundary reliably take longer than a low-single-digit-millisecond budget,
// which is what TestExtractRecoversAfterTimeout needs to force a genuine
// in-flight timeout rather than a timeout while merely acquiring the pool
// instance.
func manyPagesPDF(n int) []byte {
	var objs bytes.Buffer
	var kids bytes.Buffer
	firstPageObj := 3
	fontObj := firstPageObj + 2*n
	for i := 0; i < n; i++ {
		pageObj := firstPageObj + i
		contentObj := firstPageObj + n + i
		if i > 0 {
			kids.WriteString(" ")
		}
		fmt.Fprintf(&kids, "%d 0 R", pageObj)
		fmt.Fprintf(&objs, "%d 0 obj << /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents %d 0 R /Resources << /Font << /F1 %d 0 R >> >> >> endobj\n",
			pageObj, contentObj, fontObj)
		content := fmt.Sprintf("BT /F1 24 Tf 72 700 Td (Page %d of many) Tj ET", i)
		fmt.Fprintf(&objs, "%d 0 obj << /Length %d >> stream\n%s\nendstream endobj\n", contentObj, len(content), content)
	}

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	buf.WriteString("1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n")
	fmt.Fprintf(&buf, "2 0 obj << /Type /Pages /Kids [%s] /Count %d >> endobj\n", kids.String(), n)
	buf.Write(objs.Bytes())
	fmt.Fprintf(&buf, "%d 0 obj << /Type /Font /Subtype /Type1 /BaseFont /Helvetica >> endobj\n", fontObj)
	buf.WriteString("xref\n0 1\n0000000000 65535 f \ntrailer << /Size 1 /Root 1 0 R >>\n%%EOF")
	return buf.Bytes()
}

// TestExtractRecoversAfterTimeout is the fix for review finding #1/#2: a
// timed-out Extract call must Kill() (not merely Close()) its pdfium
// instance so the pool slot is provably reclaimed, and a subsequent Extract
// on the same *Extractor* must still succeed. See the comment on Kill()'s
// call site in pdftext.go for the mechanism (workerPool.InvalidateObject +
// WithCloseOnContextDone) this test is exercising.
func TestExtractRecoversAfterTimeout(t *testing.T) {
	e := newExtractor(t)

	// Force the in-flight-kill path: a timeout short enough that it cannot
	// possibly be hit merely while acquiring the (already pre-warmed, single)
	// pool instance, but that a several-hundred-page extraction blows
	// through while doing real wasm work.
	old := extractTimeoutOverride
	extractTimeoutOverride = 2 * time.Millisecond
	t.Cleanup(func() { extractTimeoutOverride = old })

	oldHook := onInFlightKill
	killed := false
	onInFlightKill = func() { killed = true }
	t.Cleanup(func() { onInFlightKill = oldHook })

	big := manyPagesPDF(400)
	_, err := e.Extract(context.Background(), big)
	if err == nil {
		t.Fatal("want a deadline error extracting the oversized PDF, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded (in-flight timeout)", err)
	}
	if !killed {
		t.Fatal("want the in-flight-kill path to run (timeout hit during acquisition, not extraction) — the recovery this test targets was never exercised")
	}

	// Recovery: restore a sane timeout and confirm the pool slot was
	// reclaimed rather than left permanently wedged.
	extractTimeoutOverride = 5 * time.Second
	hello, err := os.ReadFile("testdata/hello.pdf")
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.Extract(context.Background(), hello)
	if err != nil {
		t.Fatalf("Extract after timeout recovery: %v", err)
	}
	if !strings.Contains(res.Text, "Hello Openmind") {
		t.Errorf("text = %q, want it to contain %q", res.Text, "Hello Openmind")
	}
}
