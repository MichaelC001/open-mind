package api

import (
	"log/slog"
	"net/http"
)

// ListPlaces returns every place extracted across the user's items, newest
// item first, with enough item context to label a map pin. Places without
// coordinates are included; clients list rather than pin them.
func (s *Server) ListPlaces(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := s.store.Queries.ListPlaces(ctx, userID(ctx))
	if err != nil {
		slog.Error("querying places", "err", err)
		writeError(w, http.StatusInternalServerError, "could not load places")
		return
	}
	out := make([]PlaceWithItem, 0, len(rows))
	for _, row := range rows {
		p := PlaceWithItem{
			Id:           row.ID,
			Name:         row.Name,
			Hint:         row.Hint,
			Address:      row.Address,
			Source:       row.Source,
			ItemId:       row.ItemID,
			ItemTitle:    row.ItemTitle,
			ItemCardType: row.ItemCardType,
		}
		if row.Lat.Valid {
			p.Lat = &row.Lat.Float64
		}
		if row.Lng.Valid {
			p.Lng = &row.Lng.Float64
		}
		out = append(out, p)
	}
	writeJSON(w, http.StatusOK, out)
}
