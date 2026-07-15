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
	"github.com/tetratelabs/wazero"
)

// maxPages bounds extraction work on pathological documents. Pages beyond the
// cap are skipped; the result is truncated, never an error.
const maxPages = 200

// maxTextBytes bounds the accumulated body, mirroring the enrich package's
// 10 MB response cap.
const maxTextBytes = 10 << 20

// extractTimeout is a hard per-document ceiling: a hostile PDF must never pin
// a worker.
const extractTimeout = 30 * time.Second

// extractTimeoutOverride lets tests shrink the effective timeout to exercise
// the timeout/Kill path deterministically, without changing the exported
// API or the production default. Zero (the default) means "use
// extractTimeout".
var extractTimeoutOverride time.Duration

// onInFlightKill, when non-nil, is invoked immediately before an in-flight
// timeout kills its pdfium instance. It exists purely so tests can assert
// they actually exercised this path, as opposed to timing out earlier while
// merely acquiring a pool instance.
var onInFlightKill func()

// effectiveTimeout returns extractTimeoutOverride when set (tests only),
// otherwise the production extractTimeout constant.
func effectiveTimeout() time.Duration {
	if extractTimeoutOverride > 0 {
		return extractTimeoutOverride
	}
	return extractTimeout
}

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
//
// RuntimeConfig sets WithCloseOnContextDone(true). This is load-bearing for
// the timeout-recovery path in Extract: pdfiumInstance.Kill() (see
// go-pdfium@v1.19.4 webassembly/webassembly.go) cancels the worker's
// context to interrupt an in-flight wasm call, but its own doc comment
// states that cancellation only takes effect "if the caller enable[d]
// WithCloseOnContextDone on the RuntimeConfig passed to Init." Without it,
// Kill() would still free the pool slot (see below) but the wedged wasm
// call could keep running concurrently with the pool destroying/recreating
// the worker's module — a data race we can't tolerate.
func New() (*Extractor, error) {
	pool, err := webassembly.Init(webassembly.Config{
		MinIdle:       1,
		MaxIdle:       1,
		MaxTotal:      1,
		RuntimeConfig: wazero.NewRuntimeConfig().WithCloseOnContextDone(true),
	})
	if err != nil {
		return nil, fmt.Errorf("initialising pdfium wasm pool: %w", err)
	}
	return &Extractor{pool: pool}, nil
}

// Extract pulls the title, plain text and page count out of data. It respects
// ctx and additionally enforces its own 30s ceiling. Corrupt input returns an
// error; an empty-text (e.g. scanned) PDF returns Result{Text: ""} and no error.
func (e *Extractor) Extract(ctx context.Context, data []byte) (Result, error) {
	timeout := effectiveTimeout()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	instance, err := e.pool.GetInstance(timeout)
	if err != nil {
		return Result{}, fmt.Errorf("acquiring pdfium instance: %w", err)
	}

	type outcome struct {
		res Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := e.extract(instance, data)
		done <- outcome{res: res, err: err}
	}()

	select {
	case o := <-done:
		defer instance.Close()
		if o.err != nil {
			return Result{}, o.err
		}
		return o.res, ctx.Err()
	case <-ctx.Done():
		// The wasm call is wedged. instance.Kill() (not Close()) is what
		// provably frees the pool slot: per go-pdfium's implementation,
		// Kill() calls workerPool.InvalidateObject on the underlying
		// commons-pool object, which both removes the worker from the
		// pool's live-object accounting and destroys it (worker.Module.Close),
		// so a fresh worker is created for the next GetInstance() up to
		// MaxTotal — the slot is not silently held. Kill() also sets
		// instance.closed = true and clears the instance's pool
		// reference itself.
		//
		// We deliberately do NOT call instance.Close() after Kill(): the
		// two are mutually exclusive by design (Close returns
		// "instance is already closed" once Kill has run — see the
		// closed-flag guard in pdfiumInstance.Close/Kill), and Close()
		// would additionally try to return/invalidate a worker that
		// Kill() already handed to the pool for destruction. Calling
		// Close() here would be a harmless no-op (it only returns an
		// ignored error), but omitting it documents the real invariant:
		// exactly one of Kill/Close ever runs per instance.
		if onInFlightKill != nil {
			onInFlightKill()
		}
		instance.Kill()
		return Result{}, ctx.Err()
	}
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
