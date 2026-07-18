# Trusted-Proxy Client-IP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Only trust `X-Forwarded-For` when the direct peer is a configured trusted proxy, so a directly-exposed API can't have its per-IP rate buckets farmed via spoofed XFF.

**Architecture:** A `TRUSTED_PROXIES` CIDR allowlist parsed at startup into `[]*net.IPNet`, threaded from `main.go` → `NewServer` → the rate-limit middleware constructors → `clientIP(r, trusted)`. Default empty = ignore XFF (return the socket peer IP).

**Tech Stack:** Go stdlib (`net`), chi middleware, golang.org/x/time/rate. No new deps.

## Global Constraints

- Errors wrapped `fmt.Errorf("...: %w", err)`; invalid config fails loudly at startup (consistent with other config parsing in `cmd/openmind/main.go`).
- No banner-style comments (`// ==== X ====` / `// --- X ---`).
- Default (`TRUSTED_PROXIES` unset/empty) MUST ignore XFF and return the `RemoteAddr` host — the spoof-proof direction.
- Go work from `apps/api`; if the local `go` is a goenv shim failing on go.work ≥1.25, use `env -u GOROOT /opt/homebrew/bin/go`.
- Security-sensitive: the resolver must never return an attacker-controlled value when the peer is untrusted.

---

### Task 1: parseTrustedProxies + trusted-aware clientIP (+ unit tests)

**Files:**
- Modify: `apps/api/internal/api/ratelimit.go`
- Test: `apps/api/internal/api/ratelimit_test.go` (create or append if it exists)

**Interfaces:**
- Produces:
  - `func parseTrustedProxies(s string) ([]*net.IPNet, error)` — comma-separated CIDRs/bare IPs → nets; empty string → `nil, nil`; bare IP → /32 (v4) or /128 (v6); invalid entry → error.
  - `func clientIP(r *http.Request, trusted []*net.IPNet) string` — replaces the current `clientIP(r)`.
  - `func ipInNets(ip net.IP, nets []*net.IPNet) bool` — helper.

- [ ] **Step 1: Write the failing tests**

Create/append `apps/api/internal/api/ratelimit_test.go`:

```go
package api

import (
	"net/http"
	"testing"
)

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
```

Add `"net"` to the test imports.

- [ ] **Step 2: Run to verify it fails**

Run from `apps/api`: `go test ./internal/api/ -run 'TestParseTrustedProxies|TestClientIP' -v`
Expected: FAIL — `parseTrustedProxies` undefined and `clientIP` arity mismatch.

- [ ] **Step 3: Implement**

In `apps/api/internal/api/ratelimit.go`, replace the existing `clientIP` and add the helpers:

```go
// parseTrustedProxies parses a comma-separated list of CIDRs and/or bare IPs
// (a bare IP becomes a /32 or /128) into networks. Empty input yields nil
// (trust no proxy). An unparseable entry is a hard error so misconfiguration
// fails loudly at startup rather than silently trusting nothing.
func parseTrustedProxies(s string) ([]*net.IPNet, error) {
	var nets []*net.IPNet
	for _, raw := range strings.Split(s, ",") {
		part := strings.TrimSpace(raw)
		if part == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(part); err == nil {
			nets = append(nets, n)
			continue
		}
		ip := net.ParseIP(part)
		if ip == nil {
			return nil, fmt.Errorf("trusted proxy %q is not a valid IP or CIDR", part)
		}
		bits := 32
		if ip.To4() == nil {
			bits = 128
		}
		nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
	}
	return nets, nil
}

// ipInNets reports whether ip falls within any of nets.
func ipInNets(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP resolves the caller's IP for rate-bucketing. X-Forwarded-For is
// consulted ONLY when the direct peer (RemoteAddr) is a configured trusted
// proxy; then the rightmost XFF entry that is not itself a trusted proxy is the
// real client. With no trusted proxies, or an untrusted peer, XFF is ignored
// and the socket peer IP is used — so a directly-exposed API cannot have its
// per-IP buckets farmed with a spoofed header.
func clientIP(r *http.Request, trusted []*net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(host)
	if peer == nil || !ipInNets(peer, trusted) {
		return host
	}
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		cand := net.ParseIP(strings.TrimSpace(parts[i]))
		if cand == nil {
			continue
		}
		if !ipInNets(cand, trusted) {
			return cand.String()
		}
	}
	return host
}
```

`fmt` and `net` are needed; `net` and `strings` are already imported, add `fmt` if absent.

- [ ] **Step 4: Run to verify it passes**

Run from `apps/api`: `go test ./internal/api/ -run 'TestParseTrustedProxies|TestClientIP' -v`
Expected: PASS. (Package won't build fully until Task 2 updates the `clientIP` callers — run the two tests with `-run` which still compiles the package; if the package fails to compile due to the callers, that is expected and Task 2 fixes it. If it blocks the test run, do Task 2's caller updates in the same commit.)

- [ ] **Step 5: Commit**

```bash
git add apps/api/internal/api/ratelimit.go apps/api/internal/api/ratelimit_test.go
git commit -m "feat(api): trusted-proxy-aware clientIP + TRUSTED_PROXIES parser"
```

---

### Task 2: Thread trusted set through limiters + NewServer + main.go + docs

**Files:**
- Modify: `apps/api/internal/api/ratelimit.go` (limiter constructors + mcpRateKey)
- Modify: `apps/api/internal/api/server.go` (NewServer signature + Use() calls)
- Modify: `apps/api/cmd/openmind/main.go` (parse TRUSTED_PROXIES, pass to NewServer)
- Modify: `docs/self-hosting.md`
- Test: `apps/api/internal/api/ratelimit_test.go` (append a middleware integration test)

**Interfaces:**
- Consumes: `parseTrustedProxies`, `clientIP(r, trusted)` (Task 1).
- Produces: `rateLimit`, `claimRateLimit`, `mcpRateLimit`, `mcpIPRateLimit` each take `trusted []*net.IPNet`; `perIPRateLimit`/`perKeyRateLimit` thread it; `mcpRateKey(r, trusted)`; `NewServer(...)` gains a trailing `trusted []*net.IPNet` param.

- [ ] **Step 1: Update the limiter constructors + key func**

In `ratelimit.go`:
- `func rateLimit(rps rate.Limit, burst int, trusted []*net.IPNet) ...` → `return perIPRateLimit(rps, burst, guarded, trusted)`.
- `func claimRateLimit(trusted []*net.IPNet) ...` → `perIPRateLimit(rate.Limit(5.0/60.0), 5, matchClaim, trusted)`.
- `func mcpRateLimit(trusted []*net.IPNet) ...` → `perKeyRateLimit(rate.Limit(5), 20, matchMCP, func(r *http.Request) string { return mcpRateKey(r, trusted) })`.
- `func mcpIPRateLimit(trusted []*net.IPNet) ...` → `perIPRateLimit(rate.Limit(20), 100, matchMCP, trusted)`.
- `func mcpRateKey(r *http.Request, trusted []*net.IPNet) string` → the `ip:` fallback becomes `"ip:" + clientIP(r, trusted)`.
- `func perIPRateLimit(rps rate.Limit, burst int, match func(method, path string) bool, trusted []*net.IPNet) ...` → key func `func(r *http.Request) string { return clientIP(r, trusted) }`.
- `perKeyRateLimit` is unchanged (already takes a key func).

- [ ] **Step 2: Update NewServer + mounts**

In `server.go`, add a trailing param to `NewServer(..., kindleCfg KindleConfig, trusted []*net.IPNet)` and update the `r.Use` calls:

```go
	r.Use(rateLimit(rate.Limit(1), 10, trusted))
	r.Use(claimRateLimit(trusted))
	r.Use(mcpIPRateLimit(trusted))
	r.Use(mcpRateLimit(trusted))
```

- [ ] **Step 3: Parse config in main.go**

In `cmd/openmind/main.go`, before `NewServer` is called, parse the env and fail loudly on error:

```go
	trustedProxies, err := api.ParseTrustedProxies(os.Getenv("TRUSTED_PROXIES"))
	if err != nil {
		return fmt.Errorf("parsing TRUSTED_PROXIES: %w", err)
	}
```

Since `parseTrustedProxies` is unexported, export a thin wrapper `ParseTrustedProxies` in `ratelimit.go` (`func ParseTrustedProxies(s string) ([]*net.IPNet, error) { return parseTrustedProxies(s) }`) OR make `parseTrustedProxies` exported and update Task 1 references. Prefer exporting `ParseTrustedProxies` directly (rename in Task 1 usage is fine — pick one name and use it consistently in both tasks). Pass `trustedProxies` as the new final arg to `NewServer`. Confirm the exact `NewServer(...)` call site and update it. If `err` is already declared in scope, use `=` not `:=`.

- [ ] **Step 4: Middleware integration test**

Append to `ratelimit_test.go` a test that builds `perIPRateLimit(rate.Limit(0), 1, func(_, p string) bool { return p == "/items" }, trusted)` (burst 1, no refill) wrapped around a 200 handler, and asserts:
- Untrusted peer with two different spoofed XFF values (same RemoteAddr) → second request is 429 (shared peer bucket; spoof didn't create a new bucket).
- Trusted peer with two different real client XFFs → both allowed once each then 429 on repeat (distinct buckets per real client).

Use `httptest.NewRecorder()` and set `RemoteAddr`/XFF on each request.

- [ ] **Step 5: Docs**

In `docs/self-hosting.md`, add `TRUSTED_PROXIES` to the env-var reference: comma-separated CIDRs/IPs of reverse proxies to trust for `X-Forwarded-For`; empty (default) ignores XFF and rate-limits by the direct connection IP; set it to your proxy's source range when running behind a reverse proxy so per-client limits work.

- [ ] **Step 6: Verify + commit**

Run from `apps/api`: `go build ./... && go test ./internal/api/ -p 1`
Expected: PASS. Then `go vet ./...` clean.

```bash
git add apps/api/internal/api/ratelimit.go apps/api/internal/api/server.go apps/api/cmd/openmind/main.go apps/api/internal/api/ratelimit_test.go docs/self-hosting.md
git commit -m "feat(api): wire TRUSTED_PROXIES through rate limiters (issue #10)"
```

---

### Task 3: Final review + merge (deploy deferred to maintainer)

- [ ] **Step 1:** Whole-branch review (security-focused): confirm untrusted peers can never inject a client IP; the default is spoof-proof; no route/guard/bucket changes; config fails loud.
- [ ] **Step 2:** Merge to main (PR).
- [ ] **Step 3:** Do NOT change prod security config unattended. Leave a clear note: the next `api` deploy ships default-empty `TRUSTED_PROXIES`, which makes the claim endpoint bucket by the web-container IP; to preserve per-real-client bucketing, set `TRUSTED_PROXIES` on the box to the Docker bridge subnet the web container connects from, then deploy `api` and verify the claim endpoint still throttles per real IP. Record in the ledger + report for the maintainer.
