# Openmind mobile

A thin Expo (SDK 57) client for [Openmind](../../README.md): capture links and notes
into your instance from your phone — especially via the **share sheet** (share any
link/text from another app → it lands in Capture, pre-filled) — plus a minimal
library view and token login. All enrichment stays server-side; the app only talks
to your instance's `/api/*` with a Bearer token.

Screens: **Library** (recent items), **Capture** (paste a URL / jot a note → Save),
**Settings** (instance URL + API token).

## Prerequisites

- Node 18+ and a running Openmind instance you can reach from the phone.
- An **API token** for that instance (the same `OPENMIND_TOKEN` you use for the
  browser extension — see the root `docs/self-hosting.md`).

## Install

```bash
cd apps/mobile
npm install
```

This app has its own lockfile and is **not** part of the pnpm workspace — always
install from inside `apps/mobile`. Use `./node_modules/.bin/expo install <lib>` to
add Expo-compatible libraries so versions match SDK 57.

## Run the app (Expo Go)

```bash
cd apps/mobile
npx expo start        # or: ./node_modules/.bin/expo start
```

Scan the QR code with **Expo Go** (iOS/Android). The Library, Capture, and Settings
screens all work in Expo Go.

On first launch nothing is configured, so you land on **Settings**:

1. Enter your **instance URL** (e.g. `https://openmind.example.com`).
2. Paste your **API token**.
3. Tap **Validate & save** — the app calls `GET /api/auth/check` and stores the
   token in the device keychain (`expo-secure-store`) on success.

Then Capture and Library become usable.

## Share sheet — requires a dev build

Sharing is native code and **cannot run in Expo Go** — you need a custom dev build:

```bash
cd apps/mobile
npx expo run:ios       # or: npx expo run:android
# (or build with EAS: npx eas build --profile development)
```

The two platforms take different routes:

- **iOS — inline save, no app switch.** A native Swift share extension
  (`targets/share/`, built with
  [`@bacons/apple-targets`](https://github.com/EvanBacon/expo-apple-targets))
  POSTs the shared URL/text straight to `POST {instanceUrl}/api/items` and
  shows a small "Saved to Openmind" card over the host app. It reads
  `{ instanceUrl, token }` from the App Group (`group.fun.gilla.openmind`)
  shared UserDefaults, which the app mirrors from Settings — so **connect the
  app once before using the share sheet**. Activation rules (one URL, web
  pages, plain text) live in `targets/share/Info.plist`. Swift edits don't
  need a new prebuild; config changes do (`npx expo prebuild --clean -p ios`).
- **Android — opens the app.** [`expo-share-intent`](https://github.com/achorein/expo-share-intent)
  handles `text/*` SEND intents (iOS side disabled in `app.json`); the app
  opens on **Capture** pre-filled with the shared URL/text; tap **Save**.

> Note: the token is mirrored into App Group UserDefaults (not the keychain) so
> the extension can authenticate. On-device it is sandboxed to this app group;
> treat a self-host token leak as revocable via a new token.

> Do not commit the generated `ios/` and `android/` folders; regenerate with
> `expo prebuild` / `expo run:*` before an EAS build.

## Web preview

`npx expo start --web` (or `npx expo export --platform web`) runs the app through
react-native-web for quick UI checks. The share-intent native module is absent on
web and no-ops there; `expo-secure-store` falls back to `localStorage`. The web
target is a preview surface only — ship the native app for real use.

## Verify

```bash
cd apps/mobile
./node_modules/.bin/tsc --noEmit           # types
./node_modules/.bin/expo export --platform web   # web bundle builds
```
