import type { Settings } from "./settings";

/**
 * Resolve an item's leadImageUrl into an <Image> source.
 *
 * Uploaded assets are stored as `/assets/<id>` on the Go API. The mobile app
 * talks to the web origin, so those paths must go through the Next proxy at
 * `/api/assets/<id>` with a Bearer header. Absolute http(s) URLs are public
 * and used as-is.
 */
export function leadImageSource(
  leadImageUrl: string | undefined,
  settings: Settings | null,
): { uri: string; headers?: Record<string, string> } | undefined {
  if (!leadImageUrl) return undefined;
  if (leadImageUrl.startsWith("/assets/")) {
    if (!settings) return undefined;
    const id = leadImageUrl.slice("/assets/".length);
    return {
      uri: `${settings.instanceUrl}/api/assets/${id}`,
      headers: { Authorization: `Bearer ${settings.token}` },
    };
  }
  return { uri: leadImageUrl };
}
