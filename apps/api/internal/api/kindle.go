package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/riverqueue/river"

	"github.com/rohithgilla12/openmind/api/internal/jobs"
	"github.com/rohithgilla12/openmind/api/internal/search"
	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

// kindleNotConfiguredMsg is returned verbatim as the 409 body's "error"
// field when neither a per-user Kindle address nor the server-wide env
// config is set.
const kindleNotConfiguredMsg = "kindle is not configured — set your Kindle address in Settings, or set KINDLE_EMAIL on the server"

// kindleConfiguredFor reports whether Send-to-Kindle can both send (SMTP
// transport configured) and resolve a destination address for uid: the
// server's KINDLE_EMAIL fallback, or the user's own kindle_email setting. A
// user setting alone can never satisfy this — it only ever supplies a
// recipient, and without a configured SMTP transport there is nothing to
// send with.
//
// A non-nil error means the configuration check itself failed (e.g. a DB
// error unrelated to "no such setting") — callers must surface that as a
// 500, not silently treat it as "not configured".
func (s *Server) kindleConfiguredFor(ctx context.Context, uid uuid.UUID) (bool, error) {
	if !s.kindle.SMTPConfigured {
		return false, nil
	}
	if s.kindle.EnvRecipient {
		return true, nil
	}
	_, err := s.store.Queries.GetUserSetting(ctx, db.GetUserSettingParams{UserID: uid, Key: kindleSettingKey})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// kindleMaxAttempts caps River retries for a send_kindle job: SMTP failures
// are usually transient, but a job should eventually give up rather than
// retry forever.
const kindleMaxAttempts = 5

// SendItemToKindle queues an item to be built into an EPUB and e-mailed to
// the configured Kindle address. Checks run in order: ownership (404),
// feature configured (409), body present (422) — so the most specific,
// cheapest-to-explain failure wins.
func (s *Server) SendItemToKindle(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	ctx := r.Context()
	uid := userID(ctx)
	item, err := s.store.Queries.GetItem(ctx, db.GetItemParams{UserID: uid, ID: id})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "item not found")
			return
		}
		slog.Error("getting item", "err", err)
		writeError(w, http.StatusInternalServerError, "could not fetch item")
		return
	}
	configured, err := s.kindleConfiguredFor(ctx, uid)
	if err != nil {
		slog.Error("checking kindle configuration", "err", err)
		writeError(w, http.StatusInternalServerError, "could not check kindle configuration")
		return
	}
	if !configured {
		writeError(w, http.StatusConflict, kindleNotConfiguredMsg)
		return
	}
	if item.Body == "" {
		writeError(w, http.StatusUnprocessableEntity, "item has no body to send")
		return
	}

	itemID := item.ID
	if _, err := s.riverClient.Insert(ctx, jobs.SendKindleArgs{UserID: uid, ItemID: &itemID}, &river.InsertOpts{MaxAttempts: kindleMaxAttempts}); err != nil {
		slog.Error("enqueueing send_kindle job", "item_id", item.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "could not queue kindle send")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"queued": true})
}

// SendLensToKindle queues a Lens's current matches to be built into a digest
// EPUB and e-mailed to the configured Kindle address. The emptiness check
// runs the Lens rule synchronously (same cost as GetLensItems) so a rule
// matching nothing with a body returns 422 immediately rather than after a
// round trip through the job queue.
func (s *Server) SendLensToKindle(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	ctx := r.Context()
	uid := userID(ctx)
	l, err := s.store.Queries.GetLens(ctx, db.GetLensParams{UserID: uid, ID: id})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "lens not found")
			return
		}
		slog.Error("getting lens", "err", err)
		writeError(w, http.StatusInternalServerError, "could not fetch lens")
		return
	}
	configured, err := s.kindleConfiguredFor(ctx, uid)
	if err != nil {
		slog.Error("checking kindle configuration", "err", err)
		writeError(w, http.StatusInternalServerError, "could not check kindle configuration")
		return
	}
	if !configured {
		writeError(w, http.StatusConflict, kindleNotConfiguredMsg)
		return
	}

	rule := decodeStoredRule(l.Rule)
	results, err := s.runLensRule(ctx, uid, rule)
	if err != nil && !errors.Is(err, search.ErrBadColor) {
		// A stored colour became invalid is treated as an empty match set
		// (mirrors GetLensItems), not a 500 — any other error is infra.
		slog.Error("running lens rule", "err", err)
		writeError(w, http.StatusInternalServerError, "could not run lens")
		return
	}
	hasBody := false
	for _, res := range results {
		if res.Item.Body != "" {
			hasBody = true
			break
		}
	}
	if !hasBody {
		writeError(w, http.StatusUnprocessableEntity, "lens has no matching items with a body to send")
		return
	}

	lensID := l.ID
	if _, err := s.riverClient.Insert(ctx, jobs.SendKindleArgs{UserID: uid, LensID: &lensID}, &river.InsertOpts{MaxAttempts: kindleMaxAttempts}); err != nil {
		slog.Error("enqueueing send_kindle job", "lens_id", l.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "could not queue kindle send")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"queued": true})
}
