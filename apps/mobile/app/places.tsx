// Stack screen: every place the pipeline has extracted, plotted on a map with
// coordinate-less places listed below (opened via a Google Maps search URL).
import { useQuery } from "@tanstack/react-query";
import { Stack, useRouter } from "expo-router";
import { useMemo } from "react";
import {
  ActivityIndicator,
  Linking,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from "react-native";
import MapView, { Marker, type Region } from "react-native-maps";
import { SafeAreaView } from "react-native-safe-area-context";
import { ApiError, listPlaces, type PlaceWithItem } from "@/lib/api";
import { queryKeys } from "@/lib/query";
import { colors, fonts, radius, spacing } from "@/lib/theme";

const WORLD_REGION: Region = {
  latitude: 20,
  longitude: 0,
  latitudeDelta: 90,
  longitudeDelta: 90,
};

function hasCoords(p: PlaceWithItem): p is PlaceWithItem & { lat: number; lng: number } {
  return typeof p.lat === "number" && typeof p.lng === "number";
}

function googleMapsSearchUrl(p: PlaceWithItem): string {
  const q = [p.name, p.hint].filter(Boolean).join(" ");
  return `https://www.google.com/maps/search/?api=1&query=${encodeURIComponent(q)}`;
}

export default function PlacesScreen() {
  const router = useRouter();

  const placesQuery = useQuery({
    queryKey: queryKeys.places,
    queryFn: async () => {
      const res = await listPlaces();
      if (!res.ok) throw new ApiError(res.status);
      return res.places;
    },
  });

  const places = placesQuery.data ?? [];
  const pinned = useMemo(() => places.filter(hasCoords), [places]);
  const unpinned = useMemo(() => places.filter((p) => !hasCoords(p)), [places]);

  const initialRegion: Region = pinned.length
    ? { latitude: pinned[0].lat, longitude: pinned[0].lng, latitudeDelta: 0.3, longitudeDelta: 0.3 }
    : WORLD_REGION;

  const errStatus = placesQuery.error instanceof ApiError ? placesQuery.error.status : undefined;

  return (
    <SafeAreaView style={styles.safe} edges={["left", "right", "bottom"]}>
      <Stack.Screen options={{ title: "Places" }} />
      <Body
        isPending={placesQuery.isPending && !placesQuery.data}
        isError={placesQuery.isError}
        errStatus={errStatus}
        pinned={pinned}
        unpinned={unpinned}
        initialRegion={initialRegion}
        onRetry={() => void placesQuery.refetch()}
        onOpenItem={(id) => router.push(`/item/${id}`)}
      />
    </SafeAreaView>
  );
}

function Body({
  isPending,
  isError,
  errStatus,
  pinned,
  unpinned,
  initialRegion,
  onRetry,
  onOpenItem,
}: {
  isPending: boolean;
  isError: boolean;
  errStatus?: number;
  pinned: (PlaceWithItem & { lat: number; lng: number })[];
  unpinned: PlaceWithItem[];
  initialRegion: Region;
  onRetry: () => void;
  onOpenItem: (id: string) => void;
}) {
  if (isPending) {
    return (
      <View style={styles.centre}>
        <ActivityIndicator color={colors.cobalt} />
      </View>
    );
  }

  if (isError && pinned.length === 0 && unpinned.length === 0) {
    const text =
      errStatus === 0
        ? "Instance unreachable — check your connection or the URL in Settings."
        : errStatus === 401
          ? "Token rejected — check Settings."
          : "Couldn't load your places.";
    return <Message text={text} onRetry={onRetry} />;
  }

  if (pinned.length === 0 && unpinned.length === 0) {
    return <Message text="No places saved yet — capture something with a location." />;
  }

  return (
    <View style={styles.flex}>
      <MapView style={styles.map} initialRegion={initialRegion}>
        {pinned.map((p) => (
          <Marker
            key={p.id}
            coordinate={{ latitude: p.lat, longitude: p.lng }}
            title={p.name}
            description={p.itemTitle}
            onCalloutPress={() => onOpenItem(p.itemId)}
          />
        ))}
      </MapView>
      {unpinned.length > 0 ? (
        <ScrollView style={styles.unpinnedList} contentContainerStyle={styles.unpinnedContent}>
          <Text style={styles.sectionHeader}>Without coordinates · {unpinned.length}</Text>
          {unpinned.map((p) => (
            <Pressable
              key={p.id}
              style={({ pressed }) => [styles.row, pressed && styles.rowPressed]}
              onPress={() => void Linking.openURL(googleMapsSearchUrl(p))}
            >
              <Text style={styles.rowTitle} numberOfLines={1}>
                {p.name}
              </Text>
              {p.address ? (
                <Text style={styles.rowSubtitle} numberOfLines={1}>
                  {p.address}
                </Text>
              ) : null}
            </Pressable>
          ))}
        </ScrollView>
      ) : null}
    </View>
  );
}

function Message({ text, onRetry }: { text: string; onRetry?: () => void }) {
  return (
    <View style={styles.centre}>
      <Text style={styles.messageText}>{text}</Text>
      {onRetry ? (
        <Pressable
          style={({ pressed }) => [styles.retryButton, pressed && styles.retryButtonPressed]}
          onPress={onRetry}
        >
          <Text style={styles.retryButtonText}>Try again</Text>
        </Pressable>
      ) : null}
    </View>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1, backgroundColor: colors.canvas },
  flex: { flex: 1 },
  map: { flex: 1 },
  centre: { flex: 1, alignItems: "center", justifyContent: "center", padding: spacing.xl },
  messageText: {
    fontFamily: fonts.sans,
    fontSize: 14,
    color: colors.inkMuted,
    lineHeight: 20,
    textAlign: "center",
  },
  retryButton: {
    marginTop: spacing.lg,
    borderWidth: 1,
    borderColor: colors.cobalt,
    borderRadius: radius.button,
    paddingHorizontal: spacing.xl,
    paddingVertical: spacing.sm,
  },
  retryButtonPressed: { opacity: 0.7 },
  retryButtonText: { color: colors.cobalt, fontFamily: fonts.sansSemiBold, fontSize: 14 },
  unpinnedList: { maxHeight: "35%", backgroundColor: colors.paper },
  unpinnedContent: { paddingHorizontal: spacing.xl, paddingVertical: spacing.md },
  sectionHeader: {
    fontFamily: fonts.monoMedium,
    fontSize: 11,
    letterSpacing: 0.6,
    textTransform: "uppercase",
    color: colors.inkFaint,
    marginBottom: spacing.sm,
  },
  row: {
    borderRadius: radius.card,
    borderWidth: 1,
    borderColor: colors.hairline,
    backgroundColor: colors.cardSurface,
    padding: spacing.md,
    marginBottom: spacing.sm,
  },
  rowPressed: { opacity: 0.7 },
  rowTitle: { fontFamily: fonts.sansSemiBold, fontSize: 14, color: colors.ink },
  rowSubtitle: { fontFamily: fonts.sans, fontSize: 12, color: colors.inkMuted, marginTop: 2 },
});
