import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";

/** Default stale window — tab switches / back-nav reuse cache instead of spinning. */
export const QUERY_STALE_MS = 60_000;

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: QUERY_STALE_MS,
        gcTime: 5 * 60_000,
        retry: 1,
        refetchOnWindowFocus: false,
        refetchOnReconnect: true,
      },
    },
  });
}

export function QueryProvider({ children }: { children: ReactNode }) {
  // One client per app mount — survives tab switches; cleared on full remount.
  const [client] = useState(makeQueryClient);
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

/** Stable query keys for library / feed / desk / item detail. */
export const queryKeys = {
  items: (limit: number) => ["items", limit] as const,
  search: (q: string) => ["search", q] as const,
  feed: (limit: number) => ["feed", limit] as const,
  desk: () => ["desk"] as const,
  item: (id: string) => ["item", id] as const,
  itemPlaces: (id: string) => ["item", id, "places"] as const,
  places: ["places"] as const,
};
