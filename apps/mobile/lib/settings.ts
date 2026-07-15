// Persisted connection settings ({ instanceUrl, token }) backed by expo-secure-store
// on native. The token is a secret and is never logged.
//
// expo-secure-store has no web implementation, so on web (used only for the
// dev/preview surface) we fall back to localStorage. This keeps `expo export
// --platform web` and the web preview working without a native keychain.
import { Platform } from "react-native";
import * as SecureStore from "expo-secure-store";

export type Settings = {
  instanceUrl: string;
  token: string;
};

const INSTANCE_URL_KEY = "openmind.instanceUrl";
const TOKEN_KEY = "openmind.token";

const isWeb = Platform.OS === "web";

// The iOS share extension (targets/share) runs in its own process and cannot
// read SecureStore, so the settings are mirrored into the App Group's shared
// UserDefaults suite where the extension picks them up. The native module only
// exists in dev/EAS builds with @bacons/apple-targets — everywhere else
// ExtensionStorage is a silent no-op.
const APP_GROUP = "group.fun.gilla.openmind";

function shareExtensionStorage(): {
  set: (key: string, value?: string) => void;
} | null {
  if (Platform.OS !== "ios") return null;
  try {
    const { ExtensionStorage } = require("@bacons/apple-targets");
    return new ExtensionStorage(APP_GROUP);
  } catch {
    return null;
  }
}

function mirrorToShareExtension(settings: Settings | null): void {
  const storage = shareExtensionStorage();
  if (!storage) return;
  try {
    storage.set("instanceUrl", settings?.instanceUrl);
    storage.set("token", settings?.token);
  } catch {
    // Mirroring is best-effort; the extension degrades to its
    // "connect in the app first" message.
  }
}

async function getItem(key: string): Promise<string | null> {
  if (isWeb) {
    try {
      return globalThis.localStorage?.getItem(key) ?? null;
    } catch {
      return null;
    }
  }
  try {
    return await SecureStore.getItemAsync(key);
  } catch {
    // A keychain read failure is treated as "not stored" so callers degrade to
    // the setup flow rather than wedging on an unresolved read.
    return null;
  }
}

async function setItem(key: string, value: string): Promise<void> {
  if (isWeb) {
    try {
      globalThis.localStorage?.setItem(key, value);
    } catch {
      // ignore — preview surface only
    }
    return;
  }
  await SecureStore.setItemAsync(key, value);
}

async function deleteItem(key: string): Promise<void> {
  if (isWeb) {
    try {
      globalThis.localStorage?.removeItem(key);
    } catch {
      // ignore — preview surface only
    }
    return;
  }
  await SecureStore.deleteItemAsync(key);
}

/** Read the stored settings, or null if not fully configured. */
export async function getSettings(): Promise<Settings | null> {
  const [instanceUrl, token] = await Promise.all([
    getItem(INSTANCE_URL_KEY),
    getItem(TOKEN_KEY),
  ]);
  if (!instanceUrl || !token) return null;
  const settings = { instanceUrl, token };
  // Re-mirror on read so installs that stored settings before the share
  // extension existed still populate the App Group without re-connecting.
  mirrorToShareExtension(settings);
  return settings;
}

/** Persist settings. Trailing slashes on the instance URL are trimmed. */
export async function setSettings(settings: Settings): Promise<void> {
  const instanceUrl = settings.instanceUrl.trim().replace(/\/+$/, "");
  const token = settings.token.trim();
  await Promise.all([
    setItem(INSTANCE_URL_KEY, instanceUrl),
    setItem(TOKEN_KEY, token),
  ]);
  mirrorToShareExtension({ instanceUrl, token });
}

/** Remove all stored settings (sign out). */
export async function clearSettings(): Promise<void> {
  await Promise.all([deleteItem(INSTANCE_URL_KEY), deleteItem(TOKEN_KEY)]);
  mirrorToShareExtension(null);
}
