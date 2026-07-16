package api

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

// GetFeedItems returns the caller's feed-sourced items, newest first, whether
// or not they've been kept into the library. An optional feedId narrows to a
// single subscription. It always returns an array (never null).
func (s *Server) GetFeedItems(w http.ResponseWriter, r *http.Request, params GetFeedItemsParams) {
	limit := defaultListLimit
	if params.Limit != nil {
		limit = *params.Limit
	}
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}

	var filterFeedID pgtype.UUID
	if params.FeedId != nil {
		filterFeedID = pgtype.UUID{Bytes: *params.FeedId, Valid: true}
	}

	ctx := r.Context()
	items, err := s.store.Queries.ListFeedItems(ctx, db.ListFeedItemsParams{
		UserID:       userID(ctx),
		FilterFeedID: filterFeedID,
		LimitCount:   int32(limit),
	})
	if err != nil {
		slog.Error("listing feed items", "err", err)
		writeError(w, http.StatusInternalServerError, "could not list feed items")
		return
	}
	out := make([]Item, 0, len(items))
	for _, it := range items {
		out = append(out, toAPIItem(it))
	}
	writeJSON(w, http.StatusOK, out)
}
