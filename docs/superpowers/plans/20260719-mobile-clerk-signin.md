# Mobile Native Clerk Sign-in Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a new user sign in with Google / email code directly in the mobile app (no web-first hop), minting a long-lived `omk_` device key from the Clerk session so the rest of the app is unchanged.

**Architecture:** Add `@clerk/clerk-expo` (ClerkProvider at root, secure-store token cache). A sign-in screen does Clerk Google-SSO / email-OTP, then calls `POST /api/api-keys` with the Clerk session JWT to mint an `omk_` key, stores it via the existing `settings-context.save()`, and signs out of Clerk. The unconfigured guard points to this screen; the existing manual device-key/QR path stays as the self-hoster fallback.

**Tech Stack:** Expo SDK 57 / RN 0.86 (New Arch), @clerk/clerk-expo, expo-secure-store, expo-web-browser, expo-auth-session.

## Global Constraints

- No mobile unit-test runner exists — verify with `./node_modules/.bin/tsc --noEmit` (npx is shimmed; use `./node_modules/.bin`), `./node_modules/.bin/expo config --json`, and `./node_modules/.bin/expo export --platform web` (must still build). Runtime auth flows verify on a device/dev-client build (deferred to the build task).
- After adding `@clerk/clerk-expo`, run `./node_modules/.bin/expo install --check` and align anything it flags — SDK patch-skew caused a prior dyld launch crash.
- Secrets: never log the Clerk JWT or the `omk_` key. Store the key only via the existing `settings.ts` (SecureStore). Config values are public `EXPO_PUBLIC_*` env vars (Expo auto-inlines them).
- No banner-style comments (`// ==== X ====` / `// --- X ---`).
- Web/preview surface must keep working — guard any native-only module (SecureStore) for web like `settings.ts` does.
- Keep the existing manual path (`app/link.tsx`, Settings entry) intact.

---

### Task 1: Device-key mint helper

**Files:**
- Modify: `apps/mobile/lib/api.ts`

**Interfaces:**
- Produces: `mintDeviceKey(instanceUrl: string, clerkToken: string, name: string): Promise<{ ok: boolean; status: number; key?: string }>` — POSTs `{instanceUrl}/api/api-keys` with `Authorization: Bearer <clerkToken>` and body `{ name }`; parses `201 { key }`; never throws.

- [ ] **Step 1: Add the helper**

In `apps/mobile/lib/api.ts`, add (mirroring the existing never-throw `{ok,status,...}` helpers like `claimDeviceCode`):

```ts
/**
 * Mint a long-lived API key using a Clerk session JWT, via
 * POST {instanceUrl}/api/api-keys. Used right after native Clerk sign-in so
 * the app can store an omk_ key and use it for every subsequent request (no
 * Clerk-session refresh). The returned key is a secret and is never logged.
 */
export async function mintDeviceKey(
  instanceUrl: string,
  clerkToken: string,
  name: string,
): Promise<{ ok: boolean; status: number; key?: string }> {
  const url = instanceUrl.trim().replace(/\/+$/, "");
  if (!url) return { ok: false, status: 0 };
  try {
    const res = await fetch(`${url}/api/api-keys`, {
      method: "POST",
      headers: { Authorization: `Bearer ${clerkToken}`, "content-type": "application/json" },
      body: JSON.stringify({ name }),
    });
    let key: string | undefined;
    if (res.status === 201) {
      try {
        const data = (await res.json()) as { key?: string };
        key = data.key;
      } catch {
        key = undefined;
      }
    }
    return { ok: res.status === 201 && !!key, status: res.status, key };
  } catch {
    return { ok: false, status: 0 };
  }
}
```

- [ ] **Step 2: Verify + commit**

Run from `apps/mobile`: `./node_modules/.bin/tsc --noEmit` → clean (install node_modules first with `npm install` if needed).

```bash
git add apps/mobile/lib/api.ts
git commit -m "feat(mobile): mintDeviceKey helper (Clerk JWT -> omk_ key)"
```

---

### Task 2: Clerk dependency, config, and provider

**Files:**
- Modify: `apps/mobile/package.json` (via `expo install @clerk/clerk-expo`)
- Create: `apps/mobile/lib/clerk.ts`
- Modify: `apps/mobile/app/_layout.tsx`

**Interfaces:**
- Produces: `apps/mobile/lib/clerk.ts` exports `clerkPublishableKey: string | undefined` (from `process.env.EXPO_PUBLIC_CLERK_PUBLISHABLE_KEY`), `defaultInstanceUrl: string` (`process.env.EXPO_PUBLIC_INSTANCE_URL` ?? `"https://openmind.gilla.fun"`), and `tokenCache` (a SecureStore-backed Clerk token cache, web-guarded).

- [ ] **Step 1: Install the SDK**

From `apps/mobile`: `./node_modules/.bin/expo install @clerk/clerk-expo expo-auth-session expo-crypto`
(expo-web-browser + expo-secure-store are already deps.) Then `./node_modules/.bin/expo install --check` and align anything flagged.

- [ ] **Step 2: Create the config + token cache**

Create `apps/mobile/lib/clerk.ts`:

```ts
import { Platform } from "react-native";
import * as SecureStore from "expo-secure-store";
import type { TokenCache } from "@clerk/clerk-expo/dist/cache/types";

export const clerkPublishableKey = process.env.EXPO_PUBLIC_CLERK_PUBLISHABLE_KEY;

export const defaultInstanceUrl =
  process.env.EXPO_PUBLIC_INSTANCE_URL ?? "https://openmind.gilla.fun";

// SecureStore-backed token cache for the Clerk session. Web has no SecureStore,
// so it falls back to in-memory (the web/preview surface doesn't persist auth).
const memory = new Map<string, string>();

export const tokenCache: TokenCache = {
  getToken: async (key) => {
    if (Platform.OS === "web") return memory.get(key) ?? null;
    try {
      return await SecureStore.getItemAsync(key);
    } catch {
      return null;
    }
  },
  saveToken: async (key, value) => {
    if (Platform.OS === "web") {
      memory.set(key, value);
      return;
    }
    try {
      await SecureStore.setItemAsync(key, value);
    } catch {
      // best-effort; a failed cache write just forces re-auth next launch
    }
  },
};
```

(If the `TokenCache` type import path differs in the installed clerk-expo version, check `node_modules/@clerk/clerk-expo` for the exported type and adjust — the shape `{ getToken, saveToken }` is stable.)

- [ ] **Step 3: Wrap the app in ClerkProvider**

In `apps/mobile/app/_layout.tsx`, import and wrap. ClerkProvider goes ABOVE the app content but its absence of a key must not crash a self-hoster build — when `clerkPublishableKey` is unset, still render (Clerk hooks simply won't be used because the sign-in screen falls back to manual). Add:

```tsx
import { ClerkProvider } from "@clerk/clerk-expo";
import { clerkPublishableKey, tokenCache } from "@/lib/clerk";
```

Wrap the existing tree (inside `SafeAreaProvider`, outside `QueryProvider`):

```tsx
    <SafeAreaProvider>
      <ClerkProvider publishableKey={clerkPublishableKey ?? ""} tokenCache={tokenCache}>
        <QueryProvider>
          {/* ...existing SettingsProvider / CaptureQueueProvider / Stack... */}
        </QueryProvider>
      </ClerkProvider>
    </SafeAreaProvider>
```

- [ ] **Step 4: Verify + commit**

From `apps/mobile`: `./node_modules/.bin/tsc --noEmit` clean; `./node_modules/.bin/expo config --json >/dev/null` succeeds; `./node_modules/.bin/expo export --platform web` builds (ClerkProvider must not break web export — with an empty key it renders; if export fails, guard the provider so web renders children without Clerk).

```bash
git add apps/mobile/package.json apps/mobile/package-lock.json apps/mobile/lib/clerk.ts apps/mobile/app/_layout.tsx
git commit -m "feat(mobile): add Clerk provider + config (env-driven, web-guarded)"
```

---

### Task 3: Sign-in screen + route the unconfigured guard to it

**Files:**
- Create: `apps/mobile/app/sign-in.tsx`
- Modify: `apps/mobile/app/(tabs)/index.tsx:125` (redirect target)

**Interfaces:**
- Consumes: `mintDeviceKey` (Task 1); `clerkPublishableKey`, `defaultInstanceUrl` (Task 2); `useSettingsContext().save` from `@/lib/settings-context`; Clerk hooks from `@clerk/clerk-expo`.

- [ ] **Step 1: Repoint the unconfigured guard**

In `apps/mobile/app/(tabs)/index.tsx`, change line 125 from `<Redirect href="/settings" />` to `<Redirect href="/sign-in" />`.

- [ ] **Step 2: Build the sign-in screen**

Create `apps/mobile/app/sign-in.tsx`. It must:
- Render brand header + two primary actions and a manual fallback link.
- **Continue with Google:** use clerk-expo's SSO hook (`useSSO().startSSOFlow({ strategy: "oauth_google", redirectUrl })` where `redirectUrl = AuthSession.makeRedirectUri({ scheme: "openmind" })` from `expo-auth-session`). On `createdSessionId`, call `setActive({ session: createdSessionId })`.
- **Email code:** use `useSignIn()` — `signIn.create({ identifier: email })`, `prepareFirstFactor({ strategy: "email_code", emailAddressId })`, then a second step `attemptFirstFactor({ strategy: "email_code", code })` → `setActive({ session: result.createdSessionId })`. (Verify the exact call shape against the installed `@clerk/clerk-expo` version — use context7/Clerk docs for `useSignIn` email_code; the two-step create→attempt pattern is stable.)
- After a Clerk session is active, run the shared connect step:

```tsx
import { useAuth } from "@clerk/clerk-expo";
// ...
const { getToken, signOut } = useAuth();
const { save } = useSettingsContext();

async function connectAfterClerk() {
  const token = await getToken();
  if (!token) { /* show error */ return; }
  const res = await mintDeviceKey(defaultInstanceUrl, token, "Mobile");
  await signOut(); // the omk_ key is now the durable credential
  if (!res.ok || !res.key) { /* show error: "Couldn't finish sign-in" */ return; }
  await save({ instanceUrl: defaultInstanceUrl, token: res.key });
  router.replace("/");
}
```

- Manual fallback link: `<Link href="/settings">Connect a different instance or enter a code</Link>`.
- If `clerkPublishableKey` is unset (self-hoster build), hide the Clerk buttons and show only the manual link + a short note.
- Never log `token` or `res.key`. Style with `@/lib/theme` tokens; match the visual language of `app/link.tsx`. Loading + error states like `link.tsx`.

- [ ] **Step 3: Verify + commit**

From `apps/mobile`: `./node_modules/.bin/tsc --noEmit` clean; `./node_modules/.bin/expo export --platform web` builds.

```bash
git add apps/mobile/app/sign-in.tsx "apps/mobile/app/(tabs)/index.tsx"
git commit -m "feat(mobile): native Clerk sign-in screen; route guard to it"
```

---

### Task 4: Docs

**Files:**
- Modify: `apps/mobile/README.md` (or create a short `docs/mobile-auth.md` if the README lacks an auth section)

**Interfaces:** none.

- [ ] **Step 1: Document the Clerk mobile setup**

Add a section covering: the Clerk dashboard steps (add native application on `clerk.openmind.gilla.fun`, enable Google social connection, allowlist the `openmind://` redirect), the required EAS env vars (`EXPO_PUBLIC_CLERK_PUBLISHABLE_KEY`, optional `EXPO_PUBLIC_INSTANCE_URL`), and that self-hosters on another instance use the manual device-key/QR path.

- [ ] **Step 2: Commit**

```bash
git add apps/mobile/README.md
git commit -m "docs(mobile): Clerk sign-in setup + EAS env"
```

---

### Task 5: Build + user Clerk config + on-device verify

- [ ] **Step 1 (user):** In the Clerk dashboard (`clerk.openmind.gilla.fun`): add a native application, enable the Google social connection, allowlist `openmind://` (+ any Clerk native redirect). Copy the publishable key.
- [ ] **Step 2 (user):** Set `EXPO_PUBLIC_CLERK_PUBLISHABLE_KEY` (and optionally `EXPO_PUBLIC_INSTANCE_URL=https://openmind.gilla.fun`) as EAS env vars in the **preview** environment.
- [ ] **Step 3:** Merge to main, then build: `eas build -p android --profile preview` (and iOS when ready). Android build is fully autonomous (EAS-managed keystore).
- [ ] **Step 4 (on device):** Install the build; verify Continue-with-Google returns to the app connected, email-code works, saving/list/search work afterward (device key in effect), manual code entry still works, and sign-out returns to the sign-in screen.
- [ ] **Step 5:** Update `TODO.md`; note iOS build/submit as a follow-up (TestFlight).
