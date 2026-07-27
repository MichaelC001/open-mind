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

  const save = useCallback(async (next: Settings) => {
    await persistSettings(next);
    await reload();
  }, [reload]);

  const signOut = useCallback(async () => {
    // The Expo push token is per-install, not per-account: it isn't reissued
    // just because a different person signs in. Leaving it registered here
    // would both keep delivering the outgoing account's notifications to
    // whoever uses this device next, and permanently 409 that person's own
    // registration attempt (UpsertPushDevice's cross-tenant guard treats the
    // token as still owned by the account that registered it, since nothing
    // else told the server otherwise). Unregistering on sign-out is what
    // makes that guard's account-switch story hold in practice rather than
    // just in theory.
    //
    // Best-effort and ordered first: it needs the instance URL and auth token
    // that clearSettings() is about to delete, and a failure here (offline,
    // instance unreachable) must never block sign-out itself — someone
    // signing out on a plane still needs to sign out.
    try {
      const stored = await getStoredPushToken();
      if (stored && settings) {
        await unregisterPushDevice(stored, settings);
      }
    } catch (err) {
      console.error(err);
    }
    // Clear the local record regardless of whether the server call above
    // succeeded, so the app's own state (and the toggle's next render) is
    // consistent either way.
    await setStoredPushToken(null);
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
