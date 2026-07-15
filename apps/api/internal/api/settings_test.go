package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

type settingsResp struct {
	KindleEmail *string `json:"kindleEmail"`
}

func getSettings(t *testing.T, url string) settingsResp {
	t.Helper()
	resp, err := http.Get(url + "/settings")
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get settings status = %d, want 200", resp.StatusCode)
	}
	var out settingsResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	return out
}

// TestSettingsRoundTrip covers the full lifecycle: empty by default, set via
// PATCH (echoed and persisted), then cleared via an explicit empty string.
func TestSettingsRoundTrip(t *testing.T) {
	s, rc, _ := testDeps(t)
	srv := httptest.NewServer(newSrv(t, s, rc, ""))
	t.Cleanup(srv.Close)

	empty := getSettings(t, srv.URL)
	if empty.KindleEmail != nil {
		t.Fatalf("initial kindleEmail = %v, want absent", *empty.KindleEmail)
	}

	patch := doJSON(t, http.MethodPatch, srv.URL+"/settings", `{"kindleEmail":"me@kindle.com"}`)
	if patch.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d, want 200", patch.StatusCode)
	}
	var patched settingsResp
	if err := json.NewDecoder(patch.Body).Decode(&patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	patch.Body.Close()
	if patched.KindleEmail == nil || *patched.KindleEmail != "me@kindle.com" {
		t.Fatalf("patched echo = %v, want me@kindle.com", patched.KindleEmail)
	}

	got := getSettings(t, srv.URL)
	if got.KindleEmail == nil || *got.KindleEmail != "me@kindle.com" {
		t.Fatalf("get after patch = %v, want me@kindle.com", got.KindleEmail)
	}

	clear := doJSON(t, http.MethodPatch, srv.URL+"/settings", `{"kindleEmail":""}`)
	if clear.StatusCode != http.StatusOK {
		t.Fatalf("clear status = %d, want 200", clear.StatusCode)
	}
	var cleared settingsResp
	if err := json.NewDecoder(clear.Body).Decode(&cleared); err != nil {
		t.Fatalf("decode clear: %v", err)
	}
	clear.Body.Close()
	if cleared.KindleEmail != nil {
		t.Fatalf("cleared echo = %v, want absent", *cleared.KindleEmail)
	}

	final := getSettings(t, srv.URL)
	if final.KindleEmail != nil {
		t.Fatalf("final kindleEmail = %v, want absent", *final.KindleEmail)
	}
}

// TestSettingsValidation asserts a malformed address is rejected, while
// leaving the field absent is a no-op that still returns 200.
func TestSettingsValidation(t *testing.T) {
	s, rc, _ := testDeps(t)
	srv := httptest.NewServer(newSrv(t, s, rc, ""))
	t.Cleanup(srv.Close)

	bad := doJSON(t, http.MethodPatch, srv.URL+"/settings", `{"kindleEmail":"notanemail"}`)
	defer bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Errorf("bad email status = %d, want 400", bad.StatusCode)
	}

	noop := doJSON(t, http.MethodPatch, srv.URL+"/settings", `{}`)
	defer noop.Body.Close()
	if noop.StatusCode != http.StatusOK {
		t.Errorf("absent field status = %d, want 200", noop.StatusCode)
	}

	// Address itself well-formed, but exceeds RFC 5321's 254-octet limit.
	longLocal := strings.Repeat("a", 250)
	tooLong := doJSON(t, http.MethodPatch, srv.URL+"/settings", `{"kindleEmail":"`+longLocal+`@example.com"}`)
	defer tooLong.Body.Close()
	if tooLong.StatusCode != http.StatusBadRequest {
		t.Errorf("over-length email status = %d, want 400", tooLong.StatusCode)
	}
}

// TestSettingsScoped asserts one user's kindle_email setting is invisible to
// another user — settings are per-user like every other table. The HTTP
// harness only authenticates as the dev user, so user B's setting is seeded
// directly through the store (mirrors seedOtherUserItem in server_test.go);
// the dev user's GET must still only reflect their own value.
func TestSettingsScoped(t *testing.T) {
	s, rc, _ := testDeps(t)
	ctx := t.Context()
	other := uuid.MustParse("00000000-0000-0000-0000-0000000000ff")
	if err := s.Queries.EnsureUser(ctx, other); err != nil {
		t.Fatalf("ensure other user: %v", err)
	}
	if err := s.Queries.UpsertUserSetting(ctx, db.UpsertUserSettingParams{UserID: other, Key: "kindle_email", Value: "other@kindle.com"}); err != nil {
		t.Fatalf("seed other user setting: %v", err)
	}

	srv := httptest.NewServer(newSrv(t, s, rc, ""))
	t.Cleanup(srv.Close)

	patch := doJSON(t, http.MethodPatch, srv.URL+"/settings", `{"kindleEmail":"dev@kindle.com"}`)
	patch.Body.Close()

	got := getSettings(t, srv.URL)
	if got.KindleEmail == nil || *got.KindleEmail != "dev@kindle.com" {
		t.Fatalf("dev settings = %v, want dev@kindle.com", got.KindleEmail)
	}

	otherVal, err := s.Queries.GetUserSetting(ctx, db.GetUserSettingParams{UserID: other, Key: "kindle_email"})
	if err != nil {
		t.Fatalf("get other user setting: %v", err)
	}
	if otherVal != "other@kindle.com" {
		t.Errorf("other user setting = %q, want unaffected other@kindle.com", otherVal)
	}
}
