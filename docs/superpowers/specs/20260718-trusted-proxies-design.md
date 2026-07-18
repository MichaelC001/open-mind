# Trusted-proxy configuration for client-IP resolution (Issue #10)

Date: 2026-07-18. Closes GitHub Issue #10.

## Problem

`clientIP()` (`apps/api/internal/api/ratelimit.go`) returns the **first**
(leftmost) `X-Forwarded-For` entry unconditionally. That entry is
attacker-controlled: when the API is exposed directly (not behind the
documented reverse proxy), anyone can send `X-Forwarded-For: <random>` on
every request and mint a fresh per-IP rate bucket each time, defeating the
per-IP limiter (brute-force throttle, claim-code throttle, MCP flood ceiling).

## Design decision (made autonomously; flag for review)

Standard, conservative trusted-proxy model, opt-in via env:

- **`TRUSTED_PROXIES`** — comma-separated CIDRs and/or bare IPs (bare IP →
  `/32` or `/128`), e.g. `10.0.0.0/8,172.16.0.0/12`. Empty (default) = trust
  no proxy.
- **Client IP resolution** (`clientIP`):
  1. `host` = host part of `RemoteAddr` (the direct socket peer).
  2. If `TRUSTED_PROXIES` is empty **or** `host` is not within any trusted
     CIDR → return `host`, **ignoring XFF entirely** (spoof-proof default).
  3. If `host` is a trusted proxy → walk `X-Forwarded-For` **right-to-left**
     and return the rightmost entry that is a valid IP and is **not** itself
     within a trusted CIDR (the real client just beyond the trusted chain).
     If XFF is empty or every entry is trusted → return `host`.
- Invalid `TRUSTED_PROXIES` entries fail loudly at startup (like other config),
  never silently ignored.

Why right-to-left with trusted-skip (not "trust the leftmost"): the leftmost
XFF entry is client-supplied and forgeable even behind a proxy; only the
addresses appended by trusted infrastructure are reliable, so we consume from
the right past known-trusted hops.

Default behaviour change vs today: with `TRUSTED_PROXIES` empty, XFF is now
ignored (was: leftmost XFF trusted). This is the safe direction. Self-hosters
behind a reverse proxy — and our own prod deployment where the web container
proxies to the API and the claim proxy forwards XFF — must set
`TRUSTED_PROXIES` to the proxy's source range to keep per-real-client
bucketing. See Deployment.

## Scope

Only client-IP resolution for rate limiting. `clientIP` is consumed by
`perIPRateLimit` (general/claim/MCP-IP limiters) and `mcpRateKey`
(no-Authorization fallback). Thread a parsed `[]*net.IPNet` set from startup
config into `NewServer` → the limiter constructors → `clientIP(r, trusted)`.
No change to which routes are guarded, the buckets, or auth.

## Deployment (our prod)

Our API is not publicly exposed; cloudflared → web container → API. The claim
proxy already forwards `X-Forwarded-For` so `claimRateLimit` buckets by the
real client. After this ships with the default-empty config, the claim
endpoint would bucket by the web container's IP (coarser, not a hole). To
preserve real-client bucketing, set `TRUSTED_PROXIES` on the box to the
Docker bridge subnet the web container connects from (a private range, safe).
This is a config decision for the maintainer — verify the actual source range
before setting, and confirm the claim endpoint still throttles per real IP.

## Testing

- `parseTrustedProxies(s)` unit: empty→nil; single CIDR; bare IPv4→/32; bare
  IPv6→/128; multiple; whitespace; invalid entry→error.
- `clientIP(r, trusted)` unit (table): no trusted set → RemoteAddr, XFF
  ignored even when present; peer trusted + XFF `a, b, c` with none trusted →
  `c` (rightmost); peer trusted + trailing trusted hops skipped → first
  non-trusted from the right; peer trusted + XFF empty → RemoteAddr; peer NOT
  trusted + XFF present → RemoteAddr (spoof ignored); RemoteAddr without port;
  invalid XFF token skipped.
- Middleware integration: a request with a spoofed XFF from an untrusted peer
  shares the RemoteAddr bucket (can't farm buckets); from a trusted peer,
  distinct real clients get distinct buckets.
- `docs/self-hosting.md`: document `TRUSTED_PROXIES` + the reverse-proxy note.

## Out of scope

`Forwarded` (RFC 7239) header, `X-Real-IP`, trusting Cloudflare's
`CF-Connecting-IP`, per-route trust differences.
