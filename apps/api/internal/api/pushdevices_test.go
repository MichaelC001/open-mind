package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/rohithgilla12/openmind/api/internal/api"
)

// TestRegisterPushDeviceIsIdempotent proves that registering the same token
// twice leaves exactly one row, rather than accumulating duplicates every
// time a client re-registers (e.g. on every app foreground).
func TestRegisterPushDeviceIsIdempotent(t *testing.T) {
	s, rc, _ := testDeps(t)
	srv := httptest.NewServer(newSrv(t, s, rc, ""))
	t.Cleanup(srv.Close)

	body := `{"token":"ExponentPushToken[abc]","platform":"ios"}`
	for range 2 {
		resp := doJSON(t, http.MethodPost, srv.URL+"/push-devices", body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", resp.StatusCode)
		}
	}

	devices, err := s.Queries.ListPushDevices(context.Background(), api.DevUserID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(devices) != 1 {
		t.Errorf("devices = %d, want 1 after two registrations", len(devices))
	}
}

// TestRegisterPushDeviceRejectsBadPlatform asserts an unrecognised platform is
// a 400, not silently accepted.
func TestRegisterPushDeviceRejectsBadPlatform(t *testing.T) {
	s, rc, _ := testDeps(t)
	srv := httptest.NewServer(newSrv(t, s, rc, ""))
	t.Cleanup(srv.Close)

	resp := doJSON(t, http.MethodPost, srv.URL+"/push-devices", `{"token":"t","platform":"blackberry"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestRegisterPushDeviceRejectsMissingToken asserts an empty token is a 400
// rather than being stored as a useless row.
func TestRegisterPushDeviceRejectsMissingToken(t *testing.T) {
	s, rc, _ := testDeps(t)
	srv := httptest.NewServer(newSrv(t, s, rc, ""))
	t.Cleanup(srv.Close)

	resp := doJSON(t, http.MethodPost, srv.URL+"/push-devices", `{"token":"","platform":"ios"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestUnregisterPushDevice proves a registered token stops appearing after
// unregistering, and that unregistering a token that was never registered is
// not an error: the caller's desired end state (token not receiving pushes)
// is reached either way.
func TestUnregisterPushDevice(t *testing.T) {
	s, rc, _ := testDeps(t)
	srv := httptest.NewServer(newSrv(t, s, rc, ""))
	t.Cleanup(srv.Close)

	reg := doJSON(t, http.MethodPost, srv.URL+"/push-devices", `{"token":"ExponentPushToken[abc]","platform":"ios"}`)
	reg.Body.Close()

	resp := doJSON(t, http.MethodPost, srv.URL+"/push-devices/unregister", `{"token":"ExponentPushToken[abc]"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	devices, err := s.Queries.ListPushDevices(context.Background(), api.DevUserID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(devices) != 0 {
		t.Errorf("devices = %d, want 0", len(devices))
	}

	again := doJSON(t, http.MethodPost, srv.URL+"/push-devices/unregister", `{"token":"never-registered"}`)
	defer again.Body.Close()
	if again.StatusCode != http.StatusNoContent {
		t.Errorf("unregistering an unknown token = %d, want 204 (no-op, not an error)", again.StatusCode)
	}
}

// TestRegisterPushDeviceClearsFailedAt proves that re-registering a token
// Expo previously reported as dead (failed_at set) makes it eligible for
// delivery again, so a client that reinstalls and re-registers starts
// receiving pushes rather than staying silently blackholed.
func TestRegisterPushDeviceClearsFailedAt(t *testing.T) {
	s, rc, _ := testDeps(t)
	srv := httptest.NewServer(newSrv(t, s, rc, ""))
	t.Cleanup(srv.Close)

	const token = "ExponentPushToken[abc]"
	reg := doJSON(t, http.MethodPost, srv.URL+"/push-devices", `{"token":"`+token+`","platform":"ios"}`)
	reg.Body.Close()

	ctx := context.Background()
	if err := s.Queries.MarkPushDeviceFailed(ctx, token); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	failed, err := s.Queries.ListPushDevices(ctx, api.DevUserID)
	if err != nil {
		t.Fatalf("list after failure: %v", err)
	}
	if len(failed) != 0 {
		t.Fatalf("devices after marking failed = %d, want 0 (excluded from delivery)", len(failed))
	}

	reReg := doJSON(t, http.MethodPost, srv.URL+"/push-devices", `{"token":"`+token+`","platform":"ios"}`)
	defer reReg.Body.Close()
	if reReg.StatusCode != http.StatusNoContent {
		t.Fatalf("re-register status = %d, want 204", reReg.StatusCode)
	}

	revived, err := s.Queries.ListPushDevices(ctx, api.DevUserID)
	if err != nil {
		t.Fatalf("list after re-register: %v", err)
	}
	if len(revived) != 1 {
		t.Errorf("devices after re-register = %d, want 1 (failed_at cleared)", len(revived))
	}
}

// TestRegisterPushDeviceTiesToCallingAPIKey proves the stored row's
// api_key_id matches the bearer key the request authenticated with, and is
// null for a dev-mode caller with no key — both cases the handler must
// support (Clerk and dev-mode callers never have an API key).
func TestRegisterPushDeviceTiesToCallingAPIKey(t *testing.T) {
	s, rc, pool := testDeps(t)
	uid := uuid.New()
	if err := s.Queries.EnsureUser(context.Background(), uid); err != nil {
		t.Fatalf("ensure user: %v", err)
	}
	full := mintAPIKey(t, s, uid, "phone")

	srv := httptest.NewServer(newSrvWithAuthConfig(t, s, rc, api.AuthConfig{Mode: api.AuthModeToken, LegacyToken: "sekret"}))
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/push-devices", strings.NewReader(`{"token":"ExponentPushToken[key]","platform":"ios"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("Authorization", "Bearer "+full)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	keys, err := s.Queries.ListAPIKeys(context.Background(), uid)
	if err != nil || len(keys) != 1 {
		t.Fatalf("listing minted key: keys=%v err=%v", keys, err)
	}

	var stored uuid.NullUUID
	row := pool.QueryRow(context.Background(), `SELECT api_key_id FROM push_devices WHERE token = $1`, "ExponentPushToken[key]")
	if err := row.Scan(&stored); err != nil {
		t.Fatalf("scanning api_key_id: %v", err)
	}
	if !stored.Valid || stored.UUID != keys[0].ID {
		t.Errorf("stored api_key_id = %v valid=%v, want %s", stored.UUID, stored.Valid, keys[0].ID)
	}
}
