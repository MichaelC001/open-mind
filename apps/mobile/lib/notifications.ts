// Push registration and deep-link routing for Openmind mobile. Registration
// must only ever be triggered by an explicit user action (the Notifications
// toggle in Settings) — never on app launch. iOS grants exactly one system
// permission prompt; once it comes back denied, the only way back is sending
// the user into system Settings, so burning that prompt on a cold start
// before the user has any context for why the app wants it is unrecoverable.
import AsyncStorage from "@react-native-async-storage/async-storage";
import Constants from "expo-constants";
import * as Notifications from "expo-notifications";
import { Platform } from "react-native";
import { colors } from "./theme";

export type RegisterResult =
  | { ok: true; token: string }
  | { ok: false; reason: "denied" | "unsupported" | "error" };

const PUSH_TOKEN_KEY = "openmind.pushToken";

/**
 * The Expo push token this device last successfully registered with the
 * server, or null if push has never been enabled (or was disabled since).
 * The Settings screen uses this to render the toggle's initial state and to
 * know which token to send to POST /push-devices/unregister.
 */
export async function getStoredPushToken(): Promise<string | null> {
  try {
    return await AsyncStorage.getItem(PUSH_TOKEN_KEY);
  } catch {
    return null;
  }
}

/** Persist (or clear, with null) the last-registered push token. */
export async function setStoredPushToken(token: string | null): Promise<void> {
  try {
    if (token) {
      await AsyncStorage.setItem(PUSH_TOKEN_KEY, token);
    } else {
      await AsyncStorage.removeItem(PUSH_TOKEN_KEY);
    }
  } catch {
    // Best-effort — worst case the toggle mis-renders on next launch and the
    // user just flips it again.
  }
}

/**
 * Requests notification permission (if not already granted) and returns an
 * Expo push token. Call this only from the Settings toggle's onValueChange,
 * never from a mount effect or app-launch path.
 */
export async function registerForPushAsync(): Promise<RegisterResult> {
  try {
    if (Platform.OS === "android") {
      // Android silently drops notifications with no channel configured, so
      // this must run before the permission prompt. lightColor mirrors the
      // cobalt brand accent from lib/theme.ts.
      await Notifications.setNotificationChannelAsync("default", {
        name: "Openmind",
        importance: Notifications.AndroidImportance.DEFAULT,
        lightColor: colors.cobalt,
      });
    }

    const existing = await Notifications.getPermissionsAsync();
    let status = existing.status;
    if (status !== "granted") {
      const requested = await Notifications.requestPermissionsAsync();
      status = requested.status;
    }
    if (status !== "granted") {
      return { ok: false, reason: "denied" };
    }

    const projectId =
      Constants.expoConfig?.extra?.eas?.projectId ?? Constants.easConfig?.projectId;
    if (!projectId) {
      return { ok: false, reason: "unsupported" };
    }

    const { data } = await Notifications.getExpoPushTokenAsync({ projectId });
    return { ok: true, token: data };
  } catch {
    return { ok: false, reason: "error" };
  }
}

/**
 * Maps a push notification's data payload onto an expo-router path. Returns
 * null for anything unrecognised, which the caller treats as "just open the
 * app" (no navigation).
 *
 * Route mapping (mobile's real app/ tree has no /lens or /feed/<id> routes —
 * see task-11 correction):
 *  - item_id (string)  -> /item/<id>, the only genuine deep link.
 *  - lens_id (present) -> "/", the Library tab. A digest notification has no
 *    lens screen to open on mobile at all (Lenses aren't built here), so this
 *    is a deliberate fallback, not an oversight.
 *  - feed_id (present), or no recognised key at all -> "/feed", the feed tab.
 */
export function routeForNotificationData(data: unknown): string | null {
  if (typeof data !== "object" || data === null) {
    return null;
  }
  const payload = data as Record<string, unknown>;

  if ("item_id" in payload) {
    return typeof payload.item_id === "string" ? `/item/${payload.item_id}` : null;
  }
  if ("lens_id" in payload) {
    return "/";
  }
  if ("feed_id" in payload) {
    return "/feed";
  }
  return "/feed";
}
