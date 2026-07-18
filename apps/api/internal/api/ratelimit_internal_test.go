package api

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/time/rate"
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
	if k := mcpRateKey(&http.Request{Header: http.Header{"Authorization": []string{"Bearer tok-a"}}, RemoteAddr: "1.2.3.4:99"}, nil); k == "" || k == "1.2.3.4" {
		t.Fatalf("credential key = %q, want hash not IP", k)
	}
	ka := mcpRateKey(&http.Request{Header: http.Header{"Authorization": []string{"Bearer tok-a"}}, RemoteAddr: "1.2.3.4:99"}, nil)
	kb := mcpRateKey(&http.Request{Header: http.Header{"Authorization": []string{"Bearer tok-b"}}, RemoteAddr: "1.2.3.4:99"}, nil)
	if ka == kb {
		t.Fatal("different credentials must key different buckets")
	}
	if kip := mcpRateKey(&http.Request{RemoteAddr: "1.2.3.4:99"}, nil); kip != "ip:1.2.3.4" {
		t.Fatalf("no-credential key = %q, want ip fallback", kip)
	}
}

func TestMCPRateLimitBuckets(t *testing.T) {
	handler := mcpRateLimit(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	handler := mcpIPRateLimit(nil)(mcpRateLimit(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func mustReq(t *testing.T, remoteAddr, xff string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(http.MethodGet, "/items", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.RemoteAddr = remoteAddr
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

func TestParseTrustedProxies(t *testing.T) {
	if nets, err := parseTrustedProxies(""); err != nil || nets != nil {
		t.Fatalf("empty: got %v, %v", nets, err)
	}
	nets, err := parseTrustedProxies("10.0.0.0/8, 192.168.1.5 , ::1")
	if err != nil {
		t.Fatalf("valid: %v", err)
	}
	if len(nets) != 3 {
		t.Fatalf("want 3 nets, got %d", len(nets))
	}
	// bare IPv4 → /32
	if ones, _ := nets[1].Mask.Size(); ones != 32 {
		t.Errorf("bare IPv4 mask = /%d, want /32", ones)
	}
	// bare IPv6 → /128
	if ones, _ := nets[2].Mask.Size(); ones != 128 {
		t.Errorf("bare IPv6 mask = /%d, want /128", ones)
	}
	if _, err := parseTrustedProxies("not-an-ip"); err == nil {
		t.Error("invalid entry: want error")
	}
}

func TestClientIP(t *testing.T) {
	trusted, err := parseTrustedProxies("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, remoteAddr, xff string
		trusted               []*net.IPNet
		want                  string
	}{
		{"no trusted set ignores xff", "203.0.113.7:5555", "1.2.3.4", nil, "203.0.113.7"},
		{"untrusted peer ignores spoofed xff", "203.0.113.7:5555", "1.2.3.4", trusted, "203.0.113.7"},
		{"trusted peer takes rightmost non-trusted", "10.1.2.3:5555", "9.9.9.9, 8.8.8.8", trusted, "8.8.8.8"},
		{"trusted peer skips trailing trusted hops", "10.1.2.3:5555", "8.8.8.8, 10.0.0.9", trusted, "8.8.8.8"},
		{"trusted peer empty xff falls back to peer", "10.1.2.3:5555", "", trusted, "10.1.2.3"},
		{"trusted peer all-trusted xff falls back to peer", "10.1.2.3:5555", "10.0.0.9, 10.0.0.8", trusted, "10.1.2.3"},
		{"remoteaddr without port", "203.0.113.7", "", trusted, "203.0.113.7"},
		{"invalid xff token skipped", "10.1.2.3:5555", "garbage, 8.8.8.8", trusted, "8.8.8.8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clientIP(mustReq(t, tt.remoteAddr, tt.xff), tt.trusted); got != tt.want {
				t.Errorf("clientIP = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestPerIPRateLimitMiddlewareTrustedProxyThreading proves perIPRateLimit's
// key function actually consults the trusted set threaded through it, end to
// end through the middleware: an untrusted peer's spoofed X-Forwarded-For
// must not mint a new bucket — it shares the peer's own bucket — while a
// trusted peer's X-Forwarded-For does bucket by the real client it names, so
// two different real clients behind the proxy get independent limits.
func TestPerIPRateLimitMiddlewareTrustedProxyThreading(t *testing.T) {
	trusted, err := parseTrustedProxies("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}

	onlyItems := func(_, path string) bool { return path == "/items" }
	// burst 1, rps 0: exactly one request per bucket ever succeeds, with no refill.
	handler := perIPRateLimit(rate.Limit(0), 1, onlyItems, trusted)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	doReq := func(remoteAddr, xff string) int {
		req := httptest.NewRequest(http.MethodGet, "/items", nil)
		req.RemoteAddr = remoteAddr
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	t.Run("untrusted peer: spoofed XFF shares the peer bucket", func(t *testing.T) {
		if code := doReq("203.0.113.7:1", "1.1.1.1"); code != http.StatusOK {
			t.Fatalf("first request = %d, want 200", code)
		}
		if code := doReq("203.0.113.7:1", "2.2.2.2"); code != http.StatusTooManyRequests {
			t.Fatalf("second request (different spoofed XFF, same untrusted peer) = %d, want 429 — the spoof must not mint a new bucket", code)
		}
	})

	t.Run("trusted peer: distinct XFF client IPs get distinct buckets", func(t *testing.T) {
		if code := doReq("10.1.2.3:1", "9.9.9.9"); code != http.StatusOK {
			t.Fatalf("client A first request = %d, want 200", code)
		}
		if code := doReq("10.1.2.3:1", "8.8.8.8"); code != http.StatusOK {
			t.Fatalf("client B first request = %d, want 200 (distinct bucket from client A)", code)
		}
		if code := doReq("10.1.2.3:1", "9.9.9.9"); code != http.StatusTooManyRequests {
			t.Fatalf("client A repeat request = %d, want 429", code)
		}
		if code := doReq("10.1.2.3:1", "8.8.8.8"); code != http.StatusTooManyRequests {
			t.Fatalf("client B repeat request = %d, want 429", code)
		}
	})
}
