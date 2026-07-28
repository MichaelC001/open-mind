// Shares the current connection settings across screens and exposes reload /
// sign-out helpers. Screens read `configured` to gate content and the
// unconfigured guard uses it to redirect to Settings.
import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { unregisterPushDevice } from "./api";
import { getStoredPushToken, setStoredPushToken } from "./notifications";
import {
  clearSettings,
  getSettings,
  setSettings as persistSettings,
  type Settings,
} from "./settings";

type SettingsContextValue = {
  settings: Settings | null;
  loading: boolean;
  configured: boolean;
  reload: () => Promise<void>;
  save: (settings: Settings) => Promise<void>;
  signOut: () => Promise<void>;
};

const SettingsContext = createContext<SettingsContextValue | null>(null);

// Releases this device's push registration under the *outgoing* credentials,
// before those credentials are overwritten (an account switch through
// save()) or deleted (signOut()). The Expo push token is per-install, not
// per-account: skip this and the row stays owned by the account that's
// leaving, so the next account's own registration permanently 409s
// (UpsertPushDevice's cross-tenant guard) with no self-service way to clear
// it — the exact failure this exists to close.
//
// Best-effort, and must use the caller-supplied outgoing settings rather than
// whatever is current by the time this resolves — the token belongs to the
// account signing out or being replaced, and the server scopes the delete by
// the caller's own user_id, so an unregister call made with the new
// credentials would silently no-op. unregisterPushDevice itself never
// throws (it catches its own network errors), but log both failure shapes —
// a thrown error and a resolved `{ok: false}` — so a stuck registration is
// at least visible, even though neither can block the caller. The local
// record is cleared regardless of outcome, so this device's own state
// doesn't keep claiming a registration the outgoing account may no longer
// own.
async function releasePushToken(outgoing: Settings): Promise<void> {
  try {
    const stored = await getStoredPushToken();
    if (stored) {
      const res = await unregisterPushDevice(stored, outgoing);
      if (!res.ok) {
        console.error(`releasing push device failed with status ${res.status}`);
      }
    }
  } catch (err) {
    console.error(err);
  }
  await setStoredPushToken(null);
}

export function SettingsProvider({ children }: { children: React.ReactNode }) {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [loading, setLoading] = useState(true);

  const reload = useCallback(async () => {
    try {
      const next = await getSettings();
      setSettings(next);
    } finally {
      // Always resolve loading so the routing guard can act — otherwise a read
      // failure would leave the app stuck on the loading state indefinitely.
      setLoading(false);
    }
  }, []);

  const save = useCallback(
    async (next: Settings) => {
      // "Connect with code" and "Validate & save" both stay visible while
      // signed in, so save() is also how one account switches to another on
      // the same install without ever tapping sign-out. Only release when
      // the credentials are genuinely changing — compared against the
      // in-memory settings, not persisted storage — so re-validating or
      // re-saving the same instance/token untouched never tears down a
      // perfectly good registration. `settings` is null on first sign-in,
      // when there is nothing yet to release.
      if (settings && (settings.instanceUrl !== next.instanceUrl || settings.token !== next.token)) {
        await releasePushToken(settings);
      }
      await persistSettings(next);
      await reload();
    },
    [reload, settings],
  );

  const signOut = useCallback(async () => {
    // Release this device's push registration before wiping the credentials
    // that identify it to the server — see releasePushToken for why this
    // matters and what "best-effort" means here. Ordered first: it needs the
    // instance URL and auth token that clearSettings() is about to delete,
    // and a failure here (offline, instance unreachable) must never block
    // sign-out itself — someone signing out on a plane still needs to sign
    // out.
    if (settings) {
      await releasePushToken(settings);
    }
    await clearSettings();
    setSettings(null);
  }, [settings]);

  useEffect(() => {
    void reload();
  }, [reload]);

  const value = useMemo<SettingsContextValue>(
    () => ({ settings, loading, configured: settings !== null, reload, save, signOut }),
    [settings, loading, reload, save, signOut],
  );

  return <SettingsContext.Provider value={value}>{children}</SettingsContext.Provider>;
}

export function useSettingsContext(): SettingsContextValue {
  const ctx = useContext(SettingsContext);
  if (!ctx) throw new Error("useSettingsContext must be used within a SettingsProvider");
  return ctx;
}
