package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/rohithgilla12/openmind/api/internal/feeds"
	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

// CreateFeed subscribes the caller to an RSS/Atom feed. The service fetches and
// parses the feed synchronously (so a broken feed is rejected before any row is
// persisted) and backfills its current entries as pending items; enrichment
// runs asynchronously. Errors map to: bad url → 400, already subscribed → 409,
// fetch/parse failure → 502.
func (s *Server) CreateFeed(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req CreateFeedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !validURL(req.Url) {
		writeError(w, http.StatusBadRequest, "url must be a valid http(s) URL")
		return
	}

	ctx := r.Context()
	feed, _, err := s.feeds.Add(ctx, userID(ctx), req.Url)
	if err != nil {
		if errors.Is(err, feeds.ErrAlreadySubscribed) {
			writeError(w, http.StatusConflict, "already subscribed to this feed")
			return
		}
		// The URL is pre-validated above, so any remaining error is a fetch or
		// parse failure: the feed is unreachable or not valid RSS/Atom. Nothing
		// is persisted, so the caller can retry.
		slog.Warn("subscribing to feed", "url", req.Url, "err", err)
		writeError(w, http.StatusBadGateway, "could not fetch or parse feed")
		return
	}
	writeJSON(w, http.StatusCreated, toAPIFeed(feed))
}

// ListFeeds returns the caller's feed subscriptions, newest first. It always
// returns an array (never null).
func (s *Server) ListFeeds(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := s.feeds.Store.Queries.ListFeeds(ctx, userID(ctx))
	if err != nil {
		slog.Error("listing feeds", "err", err)
		writeError(w, http.StatusInternalServerError, "could not list feeds")
		return
	}
	out := make([]Feed, 0, len(rows))
	for _, f := range rows {
		out = append(out, toAPIFeed(f))
	}
	writeJSON(w, http.StatusOK, out)
}

// DeleteFeed unsubscribes the caller from a feed and returns 204. A delete that
// affects no rows (unknown id or another user's feed) returns 404. Already
// imported items are kept; only polling stops: every still-unkept item from
// this feed is stamped kept_at = now before the feed row (and its FK) go away,
// so nothing silently drops out of the library once feed_id is nulled. This
// two-step (stamp then delete) is fail-safe if it partially applies: an abort
// happens before the delete call, so a run that stamps items kept but then
// fails on delete just leaves the items kept and the feed row intact — safe to
// retry, no dropped or duplicated items.
func (s *Server) DeleteFeed(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	ctx := r.Context()
	uid := userID(ctx)
	kept, err := s.feeds.Store.Queries.KeepFeedItems(ctx, db.KeepFeedItemsParams{
		UserID: uid,
		FeedID: pgtype.UUID{Bytes: id, Valid: true},
	})
	if err != nil {
		slog.Error("keeping feed items before unsubscribe", "err", err)
		writeError(w, http.StatusInternalServerError, "could not delete feed")
		return
	}
	if kept > 0 {
		slog.Info("stamped feed items kept on unsubscribe", "feed_id", id, "count", kept)
	}
	rows, err := s.feeds.Store.Queries.DeleteFeed(ctx, db.DeleteFeedParams{UserID: uid, ID: id})
	if err != nil {
		slog.Error("deleting feed", "err", err)
		writeError(w, http.StatusInternalServerError, "could not delete feed")
		return
	}
	if rows == 0 {
		writeError(w, http.StatusNotFound, "feed not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// toAPIFeed maps a stored feed to the API model, omitting last_polled_at until
// the feed has been polled at least once.
func toAPIFeed(f db.Feed) Feed {
	out := Feed{
		Id:         openapi_types.UUID(f.ID),
		Url:        f.Url,
		Title:      f.Title,
		SiteUrl:    f.SiteUrl,
		LastStatus: f.LastStatus,
		CreatedAt:  f.CreatedAt.Time,
	}
	if f.LastPolledAt.Valid {
		t := f.LastPolledAt.Time
		out.LastPolledAt = &t
	}
	return out
}
