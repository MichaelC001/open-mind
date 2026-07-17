import { Redirect, useFocusEffect, useRouter } from "expo-router";
import { useCallback, useState } from "react";
import {
  ActivityIndicator,
  FlatList,
  Pressable,
  RefreshControl,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { listFeedItems, setKept, type Item } from "@/lib/api";
import { useSettingsContext } from "@/lib/settings-context";
import { colors, fonts, radius, spacing } from "@/lib/theme";
import { stripMarkdown } from "@/lib/text";

type LoadState =
  | { kind: "loading" }
  | { kind: "ready"; items: Item[] }
  | { kind: "unreachable" }
  | { kind: "rejected" }
  | { kind: "error" };

/** Coarse relative-time label ("just now", "3h ago", "5d ago") from an ISO timestamp. */
function relativeTime(iso: string | undefined): string {
  if (!iso) return "";
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return "";
  const seconds = Math.max(0, Math.floor((Date.now() - then) / 1000));
  if (seconds < 60) return "just now";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  if (days < 7) return `${days}d ago`;
  const weeks = Math.floor(days / 7);
  if (weeks < 5) return `${weeks}w ago`;
  const months = Math.floor(days / 30);
  if (months < 12) return `${months}mo ago`;
  const years = Math.floor(days / 365);
  return `${years}y ago`;
}

export default function FeedScreen() {
  const router = useRouter();
  const { settings, configured, loading } = useSettingsContext();
  const [state, setState] = useState<LoadState>({ kind: "loading" });
  const [refreshing, setRefreshing] = useState(false);
  const [pendingKeepIds, setPendingKeepIds] = useState<Set<string>>(new Set());
  const [keepError, setKeepError] = useState<"rejected" | "error" | null>(null);

  const load = useCallback(
    async (isRefresh: boolean) => {
      if (!settings) return;
      if (isRefresh) setRefreshing(true);
      else setState({ kind: "loading" });
      const res = await listFeedItems(50);
      if (res.ok) {
        setState({ kind: "ready", items: res.items });
      } else if (res.status === 0) {
        setState({ kind: "unreachable" });
      } else if (res.status === 401) {
        setState({ kind: "rejected" });
      } else {
        setState({ kind: "error" });
      }
      if (isRefresh) setRefreshing(false);
    },
    [settings],
  );

  useFocusEffect(
    useCallback(() => {
      if (settings) void load(false);
    }, [settings, load]),
  );

  const onToggleKeep = useCallback(async (item: Item) => {
    const nextKept = !item.keptAt;
    setKeepError(null);
    setPendingKeepIds((prev) => new Set(prev).add(item.id));
    const res = await setKept(item.id, nextKept);
    setPendingKeepIds((prev) => {
      const next = new Set(prev);
      next.delete(item.id);
      return next;
    });
    if (!res.ok) {
      setKeepError(res.status === 401 ? "rejected" : "error");
      return;
    }
    setState((prev) => {
      if (prev.kind !== "ready") return prev;
      return {
        kind: "ready",
        items: prev.items.map((it) =>
          it.id === item.id ? { ...it, keptAt: nextKept ? new Date().toISOString() : null } : it,
        ),
      };
    });
  }, []);

  const onOpen = useCallback(
    (item: Item) => {
      router.push(`/item/${item.id}`);
    },
    [router],
  );

  // Unconfigured guard: with no token stored, land on Settings.
  if (!loading && !configured) return <Redirect href="/settings" />;

  const count = state.kind === "ready" ? state.items.length : 0;

  return (
    <SafeAreaView style={styles.safe} edges={["top", "left", "right"]}>
      <View style={styles.topHairline} />
      <View style={styles.header}>
        <Text style={styles.title}>Feed</Text>
        <Text style={styles.subtitle}>
          {count} {count === 1 ? "item" : "items"} · from your subscribed feeds
        </Text>
        {keepError ? (
          <Text style={styles.keepErrorText}>
            {keepError === "rejected"
              ? "Token rejected — check Settings."
              : "Couldn't update — try again."}
          </Text>
        ) : null}
      </View>
      <Body
        state={state}
        refreshing={refreshing}
        pendingKeepIds={pendingKeepIds}
        onRefresh={() => void load(true)}
        onRetry={() => void load(false)}
        onToggleKeep={onToggleKeep}
        onOpen={onOpen}
      />
    </SafeAreaView>
  );
}

type BodyProps = {
  state: LoadState;
  refreshing: boolean;
  pendingKeepIds: Set<string>;
  onRefresh: () => void;
  onRetry: () => void;
  onToggleKeep: (item: Item) => void;
  onOpen: (item: Item) => void;
};

function Body({
  state,
  refreshing,
  pendingKeepIds,
  onRefresh,
  onRetry,
  onToggleKeep,
  onOpen,
}: BodyProps) {
  if (state.kind === "loading") {
    return (
      <View style={styles.centre}>
        <ActivityIndicator color={colors.cobalt} />
      </View>
    );
  }

  if (state.kind === "unreachable") {
    return (
      <Message
        text="Instance unreachable — check your connection or the URL in Settings."
        onRetry={onRetry}
      />
    );
  }
  if (state.kind === "rejected") {
    return <Message text="Token rejected — check Settings." onRetry={onRetry} />;
  }
  if (state.kind === "error") {
    return <Message text="Couldn't load your feed." onRetry={onRetry} />;
  }

  return (
    <FlatList
      data={state.items}
      keyExtractor={(item) => item.id}
      renderItem={({ item }) => (
        <FeedRow
          item={item}
          pending={pendingKeepIds.has(item.id)}
          onToggleKeep={() => onToggleKeep(item)}
          onOpen={() => onOpen(item)}
        />
      )}
      contentContainerStyle={styles.list}
      ItemSeparatorComponent={() => <View style={styles.separator} />}
      refreshControl={
        <RefreshControl refreshing={refreshing} onRefresh={onRefresh} tintColor={colors.cobalt} />
      }
      ListEmptyComponent={
        <Message text="Nothing in your feed yet — subscribe to feeds from the web app's Feeds page." />
      }
    />
  );
}

function FeedRow({
  item,
  pending,
  onToggleKeep,
  onOpen,
}: {
  item: Item;
  pending: boolean;
  onToggleKeep: () => void;
  onOpen: () => void;
}) {
  const kept = !!item.keptAt;
  const title = stripMarkdown(item.title?.trim()) || item.url || "Untitled";
  const summary = stripMarkdown(item.summary);
  return (
    <View style={styles.row}>
      <Pressable style={styles.rowText} onPress={onOpen} hitSlop={4}>
        <Text style={styles.rowTitle} numberOfLines={2}>
          {title}
        </Text>
        {summary ? (
          <Text style={styles.rowSummary} numberOfLines={2}>
            {summary}
          </Text>
        ) : null}
        <Text style={styles.rowMeta}>{relativeTime(item.createdAt)}</Text>
      </Pressable>
      <Pressable
        style={({ pressed }) => [
          styles.keepButton,
          kept && styles.keepButtonActive,
          pressed && styles.keepButtonPressed,
        ]}
        onPress={onToggleKeep}
        disabled={pending}
      >
        <Text style={[styles.keepButtonText, kept && styles.keepButtonTextActive]}>
          {kept ? "Kept" : "Keep"}
        </Text>
      </Pressable>
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
  topHairline: { height: 2, backgroundColor: colors.gold },
  header: { paddingHorizontal: spacing.xl, paddingTop: spacing.lg, paddingBottom: spacing.md },
  title: { fontFamily: fonts.serifBold, fontSize: 27, color: colors.ink },
  subtitle: {
    fontFamily: fonts.mono,
    fontSize: 12,
    color: colors.inkFaint,
    marginTop: spacing.xs,
  },
  list: { paddingHorizontal: spacing.xl, paddingBottom: spacing.xxl, flexGrow: 1 },
  separator: { height: 14 },
  centre: { flex: 1, alignItems: "center", justifyContent: "center", padding: spacing.xl },
  messageText: {
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
  retryButtonText: { color: colors.cobalt, fontSize: 14, fontWeight: "600" },
  row: {
    flexDirection: "row",
    alignItems: "center",
    gap: spacing.md,
    borderRadius: radius.card,
    borderWidth: 1,
    borderColor: colors.hairline,
    backgroundColor: colors.cardSurface,
    padding: spacing.md,
  },
  rowText: { flex: 1, gap: 3 },
  rowTitle: { fontFamily: fonts.serifBold, fontSize: 16, color: colors.ink, lineHeight: 21 },
  rowSummary: { fontFamily: fonts.sans, fontSize: 12.5, color: colors.inkMuted, lineHeight: 17 },
  rowMeta: {
    fontFamily: fonts.mono,
    fontSize: 10.5,
    color: colors.inkFaint,
    marginTop: 2,
  },
  keepButton: {
    borderWidth: 1,
    borderColor: colors.cobalt,
    borderRadius: radius.button,
    paddingHorizontal: spacing.md,
    paddingVertical: spacing.sm,
  },
  keepButtonActive: { backgroundColor: colors.cobalt },
  keepButtonPressed: { opacity: 0.7 },
  keepButtonText: { fontFamily: fonts.sansMedium, fontSize: 12.5, color: colors.cobalt },
  keepErrorText: {
    fontFamily: fonts.sans,
    fontSize: 13,
    color: colors.danger,
    marginTop: spacing.sm,
  },
  keepButtonTextActive: { color: colors.paper },
});
