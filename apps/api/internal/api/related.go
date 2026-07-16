package api

import (
	"log/slog"
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

const (
	// relatedMaxDistance is the cosine-distance ceiling for a suggestion —
	// beyond this the pair isn't meaningfully related. Tuned by the DB tests.
	relatedMaxDistance = 0.5
	relatedLimit       = 5
)

// relatedRowToAPIItem maps the RelatedByEmbedding row's item columns through
// the same shape toAPIItem uses for a plain db.Item.
func relatedRowToAPIItem(row db.RelatedByEmbeddingRow) Item {
	return toAPIItem(db.Item{
		ID:            row.ID,
		UserID:        row.UserID,
		Url:           row.Url,
		Title:         row.Title,
		Body:          row.Body,
		LeadImageUrl:  row.LeadImageUrl,
		Summary:       row.Summary,
		Tags:          row.Tags,
		CardType:      row.CardType,
		Status:        row.Status,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
		Palette:       row.Palette,
		UserTags:      row.UserTags,
		PinnedAt:      row.PinnedAt,
		LastDriftedAt: row.LastDriftedAt,
		SearchTsv:     row.SearchTsv,
		PageCount:     row.PageCount,
	})
}

// GetRelatedItems returns up to relatedLimit embedding-similar items for one
// item, nearest first, excluding itself and anything already linked. An item
// with no embedding (noop provider, pending enrichment) yields an empty list
// rather than an error — the UI hides the section.
func (s *Server) GetRelatedItems(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	ctx := r.Context()
	uid := userID(ctx)

	owned, err := s.ownsItem(ctx, uid, id)
	if err != nil {
		slog.Error("checking item ownership", "err", err)
		writeError(w, http.StatusInternalServerError, "could not load related items")
		return
	}
	if !owned {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}

	rows, err := s.store.Queries.RelatedByEmbedding(ctx, db.RelatedByEmbeddingParams{
		UserID: uid, ItemID: id, MaxDistance: relatedMaxDistance, LimitCount: relatedLimit,
	})
	if err != nil {
		slog.Error("querying related items", "err", err)
		writeError(w, http.StatusInternalServerError, "could not load related items")
		return
	}
	out := make([]RelatedItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, RelatedItem{Item: relatedRowToAPIItem(row), Distance: row.Distance})
	}
	writeJSON(w, http.StatusOK, out)
}
