# Native Clerk sign-in on mobile — remove the web-first onboarding hop

Date: 2026-07-19. Fixes the onboarding friction: new users currently must sign
into the **web** app (Clerk), open Settings → Devices, mint a device
key/QR, then enter it on the phone. The mobile app has no sign-in of its own.

## Goal

A friend installs the app, taps **Continue with Google** (or enters an email
code), and is in — no web app, no code copying, no second device.

## Key backend fact (already true, no API change)

`apps/api/internal/api/auth.go` accepts BOTH an `omk_` device key AND a Clerk
session JWT (in `AuthMode=clerk`), JIT-provisioning a user on first Clerk
subject. So the mobile app can authenticate a request with a Clerk JWT and
mint a device key with it — no new endpoints.

## Approach: Clerk sign-in → mint a device key → store it

1. Add `@clerk/clerk-expo`; wrap the app root in `ClerkProvider`
   (`publishableKey` + a token cache backed by `expo-secure-store`).
2. When not connected, show a **sign-in screen** offering:
   - **Continue with Google** — Clerk SSO/OAuth via `expo-web-browser`,
     redirect `openmind://`.
   - **Email code** — Clerk email OTP (`useSignIn`).
3. On a successful Clerk session, call `getToken()` for the session JWT, then
   `POST {instanceUrl}/api/api-keys` with `Authorization: Bearer <clerkJWT>`
   and body `{ name: "Mobile" }`. The `201 ApiKeyCreated.key` is the full
   `omk_` key.
4. Persist `{ instanceUrl, token: <omk_ key> }` via the existing
   `settings-context.save(...)` and **sign out of the Clerk session** — the
   device key is now the durable credential. The entire rest of the app
   (all `api.ts` calls use a stored `omk_` Bearer, and the iOS share extension
   mirror) works unchanged. No Clerk-session-refresh plumbing anywhere.

**Why mint a device key instead of using the Clerk JWT directly:** Clerk
session JWTs are short-lived and need refresh; the app already speaks
long-lived `omk_` keys everywhere (and mirrors them to the iOS share
extension). Clerk becomes purely a friendly way to obtain that key.

## Config (build-time, public)

- `EXPO_PUBLIC_CLERK_PUBLISHABLE_KEY` — Clerk publishable key (public; safe to
  embed). Injected via the existing `app.config.js` env pattern; set as an EAS
  env var (preview environment), like the Maps key.
- `EXPO_PUBLIC_INSTANCE_URL` — default instance (`https://openmind.gilla.fun`)
  so friends never type a URL. Falls back to a hardcoded default if unset.
- The distributed build targets this one instance's Clerk. Self-hosters on a
  different instance/Clerk keep using the existing manual path (below).

## Keep the manual path (self-hosters / other instances)

The current device-key QR/deep-link (`app/link.tsx`) and manual Settings entry
stay as-is, reachable from the sign-in screen via a small "Connect a different
instance / enter a code" link. Native Clerk sign-in is the default; manual is
the escape hatch.

## User's Clerk-dashboard steps (documented, not code)

- Add/enable a native application in the Clerk instance
  (`clerk.openmind.gilla.fun`); enable the **Google** social connection.
- Allowlist the `openmind://` OAuth redirect (and any Clerk-provided native
  redirect).
- Copy the publishable key → set `EXPO_PUBLIC_CLERK_PUBLISHABLE_KEY` as an EAS
  env var (preview). New build required.

## Testing

- `tsc --noEmit` clean; `expo install --check` clean AFTER adding clerk-expo
  (avoid the SDK-patch-skew dyld crash class — align deps).
- Unit: the device-key mint helper `mintDeviceKey(instanceUrl, clerkJWT, name)`
  → returns `{ ok, status, key }`, never throws, parses `201 {key}`; non-201
  handled (401/429/network).
- Manual/native (post-build, on device or sim with the dev client): Google
  sign-in completes → returns to the app connected; email-code path works;
  after connect, saving/list/search all work (device key in effect); manual
  code entry still works; sign-out clears the key and returns to sign-in.
- Web export (`expo export --platform web`) still builds (ClerkProvider must
  not break the web/preview surface — clerk-expo supports web, but guard the
  token cache for web like `settings.ts` does).

## Out of scope

- Replacing the web app's own Clerk auth.
- Passkeys, multi-account switching, Apple sign-in (Google + email first).
- Self-hoster in-app Clerk config UI (build-time only for now).
