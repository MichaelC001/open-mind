package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"unicode/utf8"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

// kindleSettingKey is the user_settings row key holding the per-user
// Send-to-Kindle destination address.
const kindleSettingKey = "kindle_email"

// maxKindleEmailRunes mirrors RFC 5321's 254-octet address limit.
const maxKindleEmailRunes = 254

// validKindleEmailRe is a permissive shape check (not full RFC 5322) — good
// enough to catch obvious typos without rejecting valid addresses.
var validKindleEmailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// validKindleEmail reports whether v looks like a usable e-mail address.
func validKindleEmail(v string) bool {
	if utf8.RuneCountInString(v) > maxKindleEmailRunes {
		return false
	}
	return validKindleEmailRe.MatchString(v)
}

// currentSettings loads the caller's settings row and maps it to the API
// shape, used to build both the GET response and the PATCH echo.
func (s *Server) currentSettings(w http.ResponseWriter, r *http.Request) (Settings, bool) {
	ctx := r.Context()
	rows, err := s.store.Queries.ListUserSettings(ctx, userID(ctx))
	if err != nil {
		slog.Error("listing user settings", "err", err)
		writeError(w, http.StatusInternalServerError, "could not fetch settings")
		return Settings{}, false
	}
	out := Settings{}
	for _, row := range rows {
		if row.Key == kindleSettingKey {
			email := openapi_types.Email(row.Value)
			out.KindleEmail = &email
		}
	}
	return out, true
}

// GetSettings returns the caller's per-user settings.
func (s *Server) GetSettings(w http.ResponseWriter, r *http.Request) {
	settings, ok := s.currentSettings(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// PatchSettings applies a partial update to the caller's settings. An
// explicit empty kindleEmail clears the setting; a non-empty value must pass
// validKindleEmail. Always responds with the resulting settings object.
func (s *Server) PatchSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := userID(ctx)

	// Decode into a plain-string local shape rather than PatchSettingsRequest:
	// openapi_types.Email's UnmarshalJSON rejects "" as an invalid address,
	// which would turn the "empty string clears the setting" case into a 400
	// before validKindleEmail ever runs.
	var req struct {
		KindleEmail *string `json:"kindleEmail"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.KindleEmail != nil {
		email := *req.KindleEmail
		if email == "" {
			if _, err := s.store.Queries.DeleteUserSetting(ctx, db.DeleteUserSettingParams{UserID: uid, Key: kindleSettingKey}); err != nil {
				slog.Error("deleting kindle setting", "err", err)
				writeError(w, http.StatusInternalServerError, "could not update settings")
				return
			}
		} else {
			if !validKindleEmail(email) {
				writeError(w, http.StatusBadRequest, "invalid kindle e-mail address")
				return
			}
			if err := s.store.Queries.UpsertUserSetting(ctx, db.UpsertUserSettingParams{UserID: uid, Key: kindleSettingKey, Value: email}); err != nil {
				slog.Error("upserting kindle setting", "err", err)
				writeError(w, http.StatusInternalServerError, "could not update settings")
				return
			}
		}
	}

	settings, ok := s.currentSettings(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, settings)
}
