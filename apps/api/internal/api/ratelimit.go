package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// rateLimit throttles the write/search/list endpoints per client IP. It guards
// POST /items, GET /items, and GET /search; all other routes pass through
// untouched. GET /items is the login-probe target, so guarding it (with the
// limiter ahead of bearer auth) throttles token brute-force attempts. Each
// client gets a token-bucket limiter (rps refill, burst ceiling).
func rateLimit(rps rate.Limit, burst int, trusted []*net.IPNet) func(http.Handler) http.Handler {
	return perIPRateLimit(rps, burst, guarded, trusted)
}

// claimRateLimit throttles POST /device-links/claim with a small, strict
// per-IP bucket (5/min, burst 5) distinct from the general limiter: the
// device code is the only credential on that route, so it's the one place a
// stranger can cheaply guess at a secret, and it deserves a tighter ceiling
// than the rest of the API.
func claimRateLimit(trusted []*net.IPNet) func(http.Handler) http.Handler {
	return perIPRateLimit(rate.Limit(5.0/60.0), 5, func(method, path string) bool {
		return method == http.MethodPost && path == "/device-links/claim"
	}, trusted)
}

// mcpRateKey buckets an /mcp request by the presented credential (hashed) so
// agent sessions get isolated, generous buckets regardless of which IP or
// proxy they arrive through — the web container proxies all browser-origin
// MCP traffic from one IP, so per-IP bucketing collapses everyone together.
// Requests with no Authorization header fall back to the client IP (they 401
// at auth, but stay bounded here first).
//
// A single IP can mint unlimited distinct credential buckets by sending a
// different (fake) Authorization value on every request — each one gets its
// own fresh 5rps/burst-20 bucket, so this keying alone bounds neither
// unauthenticated 401 probing nor limiter-map growth. That is deliberately
// left to mcpIPRateLimit, a loose per-IP layer mounted ahead of this one.
func mcpRateKey(r *http.Request, trusted []*net.IPNet) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		sum := sha256.Sum256([]byte(auth))
		return "cred:" + hex.EncodeToString(sum[:8])
	}
	return "ip:" + clientIP(r, trusted)
}

// mcpRateLimit throttles /mcp per credential: 5 req/s with burst 20 —
// tool-calling agents legitimately burst, so this is looser per caller than
// the global per-IP bucket the endpoint used to share.
func mcpRateLimit(trusted []*net.IPNet) func(http.Handler) http.Handler {
	return perKeyRateLimit(rate.Limit(5), 20, func(method, path string) bool {
		return strings.HasPrefix(path, "/mcp")
	}, func(r *http.Request) string { return mcpRateKey(r, trusted) })
}

// mcpIPRateLimit is a loose per-IP ceiling on /mcp (20 rps, burst 100) mounted
// immediately before mcpRateLimit. It exists solely to cap the case
// mcpRateKey's per-credential buckets cannot: a single IP flooding /mcp with a
// different fake Authorization value on every request, each minting its own
// unthrottled credential bucket. 20 rps / burst 100 is far above legitimate
// interactive agent traffic — including all proxied browser-origin MCP
// traffic arriving from the web container's single IP — so it never bites
// real usage, but it caps a single-IP flood and bounds limiter-map growth.
func mcpIPRateLimit(trusted []*net.IPNet) func(http.Handler) http.Handler {
	return perIPRateLimit(rate.Limit(20), 100, func(method, path string) bool {
		return strings.HasPrefix(path, "/mcp")
	}, trusted)
}

// perIPRateLimit throttles requests matching match(method, path) with a
// per-client-IP token-bucket limiter (rps refill, burst ceiling). Limiters are
// created lazily per IP and evicted after 10 minutes of inactivity.
func perIPRateLimit(rps rate.Limit, burst int, match func(method, path string) bool, trusted []*net.IPNet) func(http.Handler) http.Handler {
	return perKeyRateLimit(rps, burst, match, func(r *http.Request) string { return clientIP(r, trusted) })
}

// perKeyRateLimit throttles requests matching match(method, path) with a
// per-key token-bucket limiter (rps refill, burst ceiling), keyed by key(r).
// Limiters are created lazily per key and evicted after 10 minutes of
// inactivity.
func perKeyRateLimit(rps rate.Limit, burst int, match func(method, path string) bool, key func(*http.Request) string) func(http.Handler) http.Handler {
	var (
		mu      sync.Mutex
		clients = make(map[string]*ipLimiter)
		lookups int
	)

	getLimiter := func(k string) *rate.Limiter {
		mu.Lock()
		defer mu.Unlock()

		lookups++
		if lookups%100 == 0 {
			cutoff := time.Now().Add(-10 * time.Minute)
			for k, v := range clients {
				if v.lastSeen.Before(cutoff) {
					delete(clients, k)
				}
			}
		}

		c, ok := clients[k]
		if !ok {
			c = &ipLimiter{limiter: rate.NewLimiter(rps, burst)}
			clients[k] = c
		}
		c.lastSeen = time.Now()
		return c.limiter
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !match(r.Method, r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			if !getLimiter(key(r)).Allow() {
				http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// guarded reports whether a request to (method, path) is subject to the rate
// limiter. It covers the write/search/list endpoints plus POST /assets uploads.
// GET /assets/<id> reads are deliberately NOT guarded: image loads are proxied
// server-side from the single web-container IP with no X-Forwarded-For, so a
// per-IP burst limiter would break any view with more images than the burst
// ceiling. Serving is already bearer-gated, user-scoped, and UUID-keyed.
func guarded(method, path string) bool {
	return (method == http.MethodPost && path == "/items") ||
		(method == http.MethodGet && path == "/items") ||
		(method == http.MethodPatch && strings.HasPrefix(path, "/items/")) ||
		(method == http.MethodGet && path == "/search") ||
		(method == http.MethodGet && path == "/desk") ||
		(method == http.MethodGet && path == "/feed") ||
		(method == http.MethodGet && path == "/drift") ||
		(method == http.MethodPost && strings.HasPrefix(path, "/drift/")) ||
		(method == http.MethodGet && path == "/export") ||
		(method == http.MethodPost && path == "/assets") ||
		(method == http.MethodPost && path == "/feeds") ||
		(method == http.MethodPatch && path == "/settings") ||
		// Kindle sends enqueue an SMTP delivery per call — unthrottled they'd be
		// an email-amplification vector, so both are guarded like other writes.
		(method == http.MethodPost && strings.HasPrefix(path, "/items/") && strings.HasSuffix(path, "/kindle")) ||
		(method == http.MethodPost && strings.HasPrefix(path, "/lenses/") && strings.HasSuffix(path, "/kindle")) ||
		// Highlight creation/deletion mirror the item write endpoints.
		(method == http.MethodPost && strings.HasPrefix(path, "/items/") && strings.HasSuffix(path, "/highlights")) ||
		(method == http.MethodDelete && strings.HasPrefix(path, "/highlights/")) ||
		// API keys and device links mint/revoke credentials; guard them like
		// other writes. /device-links/claim also sits behind its own, much
		// stricter claimRateLimit bucket since the code is the credential.
		strings.HasPrefix(path, "/api-keys") ||
		strings.HasPrefix(path, "/device-links")
}

type ipLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

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

// ParseTrustedProxies is the exported entry point for parsing the
// TRUSTED_PROXIES env var; see parseTrustedProxies for the format and
// semantics.
func ParseTrustedProxies(s string) ([]*net.IPNet, error) {
	return parseTrustedProxies(s)
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
	xff := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
	parts := strings.Split(xff, ",")
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
