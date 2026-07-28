package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

// maxPushTokenLen bounds the stored token. Expo tokens are ~50 characters;
// this is generous headroom that still refuses junk.
const maxPushTokenLen = 512

// RegisterPushDevice records an Expo push token for the calling device. It is
// idempotent on the token and clears any prior failure marker, so a client
// that re-registers after reinstalling starts receiving pushes again.
func (s *Server) RegisterPushDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req struct {
		Token    string `json:"token"`
		Platform string `json:"platform"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Token == "" || len(req.Token) > maxPushTokenLen {
		writeError(w, http.StatusBadRequest, "invalid push token")
		return
	}
	if req.Platform != "ios" && req.Platform != "android" {
		writeError(w, http.StatusBadRequest, "platform must be ios or android")
		return
	}

	// Tying the row to the calling API key means that if the key is later
	// revoked directly (currently only via the web Devices & Keys screen),
	// the device row cascades away with it rather than being left pushing at
	// a key that no longer exists. That is not what makes an account switch
	// on one device safe, though: a normal sign-out never revokes the key, so
	// the client is expected to call POST /push-devices/unregister itself
	// before signing out (mobile does, in settings-context.tsx). Clerk and
	// dev-mode callers have no key at all, so there is nothing to cascade for
	// them either way — the row for those callers keeps working, or fails
	// closed via UpsertPushDevice's cross-user guard, on its own.
	var keyID pgtype.UUID
	if id, ok := apiKeyID(ctx); ok {
		keyID = pgtype.UUID{Bytes: id, Valid: true}
	}

	rows, err := s.store.Queries.UpsertPushDevice(ctx, db.UpsertPushDeviceParams{
		UserID:   userID(ctx),
		ApiKeyID: keyID,
		Token:    req.Token,
		Platform: req.Platform,
	})
	if err != nil {
		slog.Error("upserting push device", "err", err)
		writeError(w, http.StatusInternalServerError, "could not register device")
		return
	}
	// Zero rows is the only signal the query gives for a cross-user conflict:
	// the row exists (so the INSERT's ON CONFLICT fired) but the WHERE guard
	// didn't match, so neither the insert nor the update applied. Any other
	// count means the row is now the caller's, so it must be handled
	// explicitly rather than falling through as a false success.
	if rows == 0 {
		writeError(w, http.StatusConflict, "this push token is already registered to a different account")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UnregisterPushDevice stops delivering to a token. Removing a token that is
// not registered is not an error: the caller's desired end state is reached
// either way.
func (s *Server) UnregisterPushDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req struct {
		Token string `json:"token"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Token == "" {
		writeError(w, http.StatusBadRequest, "missing token")
		return
	}
	if _, err := s.store.Queries.DeletePushDevice(ctx, db.DeletePushDeviceParams{
		UserID: userID(ctx), Token: req.Token,
	}); err != nil {
		slog.Error("deleting push device", "err", err)
		writeError(w, http.StatusInternalServerError, "could not unregister device")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
