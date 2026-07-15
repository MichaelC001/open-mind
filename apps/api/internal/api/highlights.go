package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/rohithgilla12/openmind/api/internal/jobs"
	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

const (
	maxHighlightExactRunes   = 2000
	maxHighlightContextRunes = 64
)

// truncRunes caps s at n runes without splitting a rune.
func truncRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

// CreateItemHighlight saves a text selection on an item as a highlight plus a
// mirrored quote card, linked to the source — all in one transaction, so a
// failure anywhere leaves nothing behind. Enrichment for the quote card is
// enqueued best-effort after commit (capture is sacred).
func (s *Server) CreateItemHighlight(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req CreateHighlightRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	exact := strings.TrimSpace(req.Exact)
	if exact == "" {
		writeError(w, http.StatusBadRequest, "exact must not be empty")
		return
	}
	if utf8.RuneCountInString(exact) > maxHighlightExactRunes {
		writeError(w, http.StatusBadRequest, "exact too long (max 2000 chars)")
		return
	}
	prefix, suffix := "", ""
	if req.Prefix != nil {
		prefix = truncRunes(*req.Prefix, maxHighlightContextRunes)
	}
	if req.Suffix != nil {
		suffix = truncRunes(*req.Suffix, maxHighlightContextRunes)
	}
	offsetHint := 0
	if req.OffsetHint != nil {
		offsetHint = *req.OffsetHint
	}

	ctx := r.Context()
	uid := userID(ctx)
	owned, err := s.ownsItem(ctx, uid, id)
	if err != nil {
		slog.Error("checking item ownership", "err", err)
		writeError(w, http.StatusInternalServerError, "could not create highlight")
		return
	}
	if !owned {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}

	tx, err := s.store.Pool.Begin(ctx)
	if err != nil {
		slog.Error("beginning highlight tx", "err", err)
		writeError(w, http.StatusInternalServerError, "could not create highlight")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.store.Queries.WithTx(tx)

	quote, err := q.CreateQuoteItem(ctx, db.CreateQuoteItemParams{UserID: uid, Body: exact})
	if err != nil {
		slog.Error("creating quote item", "err", err)
		writeError(w, http.StatusInternalServerError, "could not create highlight")
		return
	}
	hl, err := q.CreateHighlight(ctx, db.CreateHighlightParams{
		UserID: uid, SourceItemID: id, QuoteItemID: quote.ID,
		Exact: exact, Prefix: prefix, Suffix: suffix, OffsetHint: int32(offsetHint),
	})
	if err != nil {
		slog.Error("creating highlight row", "err", err)
		writeError(w, http.StatusInternalServerError, "could not create highlight")
		return
	}
	a, b := canonicalPair(id, quote.ID)
	if _, err := q.CreateLink(ctx, db.CreateLinkParams{UserID: uid, AItem: a, BItem: b}); err != nil {
		slog.Error("linking quote to source", "err", err)
		writeError(w, http.StatusInternalServerError, "could not create highlight")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Error("committing highlight tx", "err", err)
		writeError(w, http.StatusInternalServerError, "could not create highlight")
		return
	}

	if _, err := s.riverClient.Insert(ctx, jobs.EnrichArgs{UserID: uid, ItemID: quote.ID}, nil); err != nil {
		slog.Error("enqueueing enrichment for quote item", "item_id", quote.ID, "err", err)
	}
	writeJSON(w, http.StatusCreated, CreateHighlightResponse{Highlight: toAPIHighlight(hl), QuoteItem: toAPIItem(quote)})
}

// ListItemHighlights returns the highlights anchored to an item, oldest first.
func (s *Server) ListItemHighlights(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	ctx := r.Context()
	uid := userID(ctx)
	owned, err := s.ownsItem(ctx, uid, id)
	if err != nil {
		slog.Error("checking item ownership", "err", err)
		writeError(w, http.StatusInternalServerError, "could not list highlights")
		return
	}
	if !owned {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}
	rows, err := s.store.Queries.ListHighlightsBySource(ctx, db.ListHighlightsBySourceParams{UserID: uid, SourceItemID: id})
	if err != nil {
		slog.Error("listing highlights", "err", err)
		writeError(w, http.StatusInternalServerError, "could not list highlights")
		return
	}
	out := make([]Highlight, 0, len(rows))
	for _, h := range rows {
		out = append(out, toAPIHighlight(h))
	}
	writeJSON(w, http.StatusOK, out)
}

// DeleteHighlight removes a highlight and its quote card. Deleting the quote
// item is the single mutation: the highlights row goes via ON DELETE CASCADE,
// and the quote↔source link goes via the links cascade.
func (s *Server) DeleteHighlight(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	ctx := r.Context()
	uid := userID(ctx)
	hl, err := s.store.Queries.GetHighlight(ctx, db.GetHighlightParams{UserID: uid, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "highlight not found")
		return
	}
	if err != nil {
		slog.Error("fetching highlight", "err", err)
		writeError(w, http.StatusInternalServerError, "could not delete highlight")
		return
	}
	if _, err := s.store.Queries.DeleteItem(ctx, db.DeleteItemParams{UserID: uid, ID: hl.QuoteItemID}); err != nil {
		slog.Error("deleting quote item", "err", err)
		writeError(w, http.StatusInternalServerError, "could not delete highlight")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toAPIHighlight(h db.Highlight) Highlight {
	return Highlight{
		Id:           h.ID,
		SourceItemId: h.SourceItemID,
		QuoteItemId:  h.QuoteItemID,
		Exact:        h.Exact,
		Prefix:       h.Prefix,
		Suffix:       h.Suffix,
		OffsetHint:   int(h.OffsetHint),
		CreatedAt:    h.CreatedAt.Time,
	}
}
