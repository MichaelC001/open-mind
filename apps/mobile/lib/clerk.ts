import { Platform } from "react-native";
import * as SecureStore from "expo-secure-store";
import type { TokenCache } from "@clerk/clerk-expo";

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
