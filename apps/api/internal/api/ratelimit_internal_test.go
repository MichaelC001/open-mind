package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGuardedPredicate proves the rate-limit predicate covers write/search/list
// and POST /assets, but deliberately excludes GET /assets/<id> reads: image
// loads are proxied server-side from a single web-container IP with no
// X-Forwarded-For, so guarding them would break any view with more images than
// the burst ceiling. Serving is already bearer-gated, user-scoped, and
// UUID-keyed, so throttling reads adds little.
func TestGuardedPredicate(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
	}{
		{http.MethodPost, "/items", true},
		{http.MethodGet, "/items", true},
		{http.MethodGet, "/search", true},
		{http.MethodGet, "/export", true},
		{http.MethodPost, "/assets", true},
		{http.MethodGet, "/assets/3f1a2b4c-0000-0000-0000-000000000000", false},
		{http.MethodGet, "/assets/", false},
		{http.MethodGet, "/healthz", false},
		{http.MethodPost, "/items/3f1a2b4c-0000-0000-0000-000000000000/kindle", true},
		{http.MethodPost, "/lenses/3f1a2b4c-0000-0000-0000-000000000000/kindle", true},
		{http.MethodGet, "/api-keys", true},
		{http.MethodPost, "/api-keys", true},
		{http.MethodDelete, "/api-keys/3f1a2b4c-0000-0000-0000-000000000000", true},
		{http.MethodPost, "/device-links", true},
		{http.MethodPost, "/device-links/claim", true},
		{http.MethodPost, "/items/3f1a2b4c-0000-0000-0000-000000000000/highlights", true},
		{http.MethodDelete, "/highlights/3f1a2b4c-0000-0000-0000-000000000000", true},
		{http.MethodGet, "/items/3f1a2b4c-0000-0000-0000-000000000000/highlights", false},
		{http.MethodPatch, "/settings", true},
		{http.MethodGet, "/settings", false},
		{http.MethodGet, "/feed", true},
	}
	for _, c := range cases {
		if got := guarded(c.method, c.path); got != c.want {
			t.Errorf("guarded(%q, %q) = %v, want %v", c.method, c.path, got, c.want)
		}
	}
}

// TestGuardedExcludesMCP proves /mcp is no longer under the global per-IP
// bucket: it has moved to its own per-credential limiter (mcpRateLimit).
func TestGuardedExcludesMCP(t *testing.T) {
	if guarded(http.MethodPost, "/mcp") || guarded(http.MethodGet, "/mcp") {
		t.Fatal("/mcp must not be under the global per-IP bucket")
	}
}

func TestMCPRateLimitKeying(t *testing.T) {
	if k := mcpRateKey(&http.Request{Header: http.Header{"Authorization": []string{"Bearer tok-a"}}, RemoteAddr: "1.2.3.4:99"}); k == "" || k == "1.2.3.4" {
		t.Fatalf("credential key = %q, want hash not IP", k)
	}
	ka := mcpRateKey(&http.Request{Header: http.Header{"Authorization": []string{"Bearer tok-a"}}, RemoteAddr: "1.2.3.4:99"})
	kb := mcpRateKey(&http.Request{Header: http.Header{"Authorization": []string{"Bearer tok-b"}}, RemoteAddr: "1.2.3.4:99"})
	if ka == kb {
		t.Fatal("different credentials must key different buckets")
	}
	if kip := mcpRateKey(&http.Request{RemoteAddr: "1.2.3.4:99"}); kip != "ip:1.2.3.4" {
		t.Fatalf("no-credential key = %q, want ip fallback", kip)
	}
}

func TestMCPRateLimitBuckets(t *testing.T) {
	handler := mcpRateLimit()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	doReq := func(method, path, bearer, remoteAddr string) int {
		req := httptest.NewRequest(method, path, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		req.RemoteAddr = remoteAddr
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	for i := 0; i < 20; i++ {
		if code := doReq(http.MethodPost, "/mcp", "tok-a", "5.5.5.5:1"); code != http.StatusOK {
			t.Fatalf("request %d for tok-a: got %d, want 200", i, code)
		}
	}
	for i := 0; i < 5; i++ {
		if code := doReq(http.MethodPost, "/mcp", "tok-a", "5.5.5.5:1"); code != http.StatusTooManyRequests {
			t.Fatalf("request %d for tok-a over burst: got %d, want 429", i, code)
		}
	}

	for i := 0; i < 5; i++ {
		if code := doReq(http.MethodPost, "/mcp", "tok-b", "5.5.5.5:1"); code != http.StatusOK {
			t.Fatalf("request %d for tok-b: got %d, want 200 (isolated bucket)", i, code)
		}
	}

	if code := doReq(http.MethodGet, "/items", "tok-a", "5.5.5.5:1"); code != http.StatusOK {
		t.Fatalf("GET /items passthrough: got %d, want 200", code)
	}
}

// TestMCPIPRateLimitCapsUniqueCredentialFlood proves the per-IP layer catches
// what the per-credential buckets alone would let through: a single IP
// sending a different (fake) Authorization value on every request, each
// minting its own fresh, unthrottled credential bucket. Chained ahead of
// mcpRateLimit as mounted in server.go, the per-IP ceiling (20 rps, burst
// 100) caps the flood regardless of how many distinct credentials appear.
func TestMCPIPRateLimitCapsUniqueCredentialFlood(t *testing.T) {
	handler := mcpIPRateLimit()(mcpRateLimit()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	doReq := func(method, path, bearer, remoteAddr string) int {
		req := httptest.NewRequest(method, path, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		req.RemoteAddr = remoteAddr
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	for i := 0; i < 100; i++ {
		bearer := fmt.Sprintf("fake-cred-%d", i)
		if code := doReq(http.MethodPost, "/mcp", bearer, "9.9.9.9:1"); code != http.StatusOK {
			t.Fatalf("request %d (distinct credential): got %d, want 200", i, code)
		}
	}
	for i := 0; i < 10; i++ {
		bearer := fmt.Sprintf("fake-cred-over-%d", i)
		if code := doReq(http.MethodPost, "/mcp", bearer, "9.9.9.9:1"); code != http.StatusTooManyRequests {
			t.Fatalf("request %d over per-IP burst: got %d, want 429", i, code)
		}
	}

	if code := doReq(http.MethodGet, "/items", "tok-a", "9.9.9.9:1"); code != http.StatusOK {
		t.Fatalf("GET /items passthrough: got %d, want 200", code)
	}
}
