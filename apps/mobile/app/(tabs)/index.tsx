import { Redirect, useFocusEffect, useRouter } from "expo-router";
import { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  FlatList,
  Pressable,
  RefreshControl,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { ItemCard } from "@/components/ItemCard";
import { listItems, searchItems, type Item, type UnderstoodQuery } from "@/lib/api";
import { useCaptureQueue } from "@/lib/capture-queue-context";
import { useSettingsContext } from "@/lib/settings-context";
import { colors, fonts, radius, spacing } from "@/lib/theme";

const SEARCH_DEBOUNCE_MS = 300;

type LoadState =
  | { kind: "loading" }
  | { kind: "ready"; items: Item[]; understood?: UnderstoodQuery }
  | { kind: "unreachable" }
  | { kind: "search-offline" }
  | { kind: "rejected" }
  | { kind: "error" };

export default function LibraryScreen() {
  const router = useRouter();
  const { settings, configured, loading } = useSettingsContext();
  const { pendingCount, flush } = useCaptureQueue();
  const [state, setState] = useState<LoadState>({ kind: "loading" });
  const [refreshing, setRefreshing] = useState(false);
  const [query, setQuery] = useState("");
  const [debouncedQuery, setDebouncedQuery] = useState("");
  const [searchFocused, setSearchFocused] = useState(false);
  const requestSeq = useRef(0);

  useEffect(() => {
    const t = setTimeout(() => setDebouncedQuery(query.trim()), SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(t);
  }, [query]);

  const load = useCallback(
    async (isRefresh: boolean, q: string) => {
      if (!settings) return;
      const seq = ++requestSeq.current;
      if (isRefresh) setRefreshing(true);
      else setState({ kind: "loading" });

      if (q.length === 0) {
        const res = await listItems(50);
        if (seq !== requestSeq.current) return;
        if (res.ok) {
          setState({ kind: "ready", items: res.items });
        } else if (res.status === 0) {
          setState({ kind: "unreachable" });
        } else if (res.status === 401) {
          setState({ kind: "rejected" });
        } else {
          setState({ kind: "error" });
        }
      } else {
        const res = await searchItems({ q, parse: true });
        if (seq !== requestSeq.current) return;
        if (res.ok) {
          setState({
            kind: "ready",
            items: res.results.map((r) => r.item),
            understood: res.understood,
          });
        } else if (res.status === 0) {
          setState({ kind: "search-offline" });
        } else if (res.status === 401) {
          setState({ kind: "rejected" });
        } else {
          setState({ kind: "error" });
        }
      }
      if (isRefresh) setRefreshing(false);
    },
    [settings],
  );

  // Keep a ref so focus-flush can reload the current query without re-binding
  // when the debounce ticks (avoids a double fetch with the effect below).
  const debouncedQueryRef = useRef(debouncedQuery);
  debouncedQueryRef.current = debouncedQuery;

  useFocusEffect(
    useCallback(() => {
      void flush();
      if (settings) void load(false, debouncedQueryRef.current);
    }, [settings, load, flush]),
  );

  // Re-run when the debounced query changes while the screen is mounted.
  useEffect(() => {
    if (settings) void load(false, debouncedQuery);
  }, [settings, debouncedQuery, load]);

  const onOpen = useCallback(
    (item: Item) => {
      router.push(`/item/${item.id}`);
    },
    [router],
  );

  // Unconfigured guard: with no token stored, land on Settings.
  if (!loading && !configured) return <Redirect href="/settings" />;

  const count = state.kind === "ready" ? state.items.length : 0;
  const searching = debouncedQuery.length > 0;

  return (
    <SafeAreaView style={styles.safe} edges={["top", "left", "right"]}>
      <View style={styles.topHairline} />
      <View style={styles.header}>
        <Text style={styles.title}>The Mind</Text>
        <Text style={styles.subtitle}>
          {searching
            ? `${count} ${count === 1 ? "match" : "matches"}`
            : `${count} ${count === 1 ? "gathering" : "gatherings"} · organised by the machine`}
          {pendingCount > 0 ? ` · ${pendingCount} queued` : ""}
        </Text>
        <View style={styles.searchCard}>
          <TextInput
            style={[styles.searchInput, searchFocused && styles.searchInputFocused]}
            value={query}
            onChangeText={setQuery}
            onFocus={() => setSearchFocused(true)}
            onBlur={() => setSearchFocused(false)}
            placeholder="Search by fragment, vibe, colour…"
            placeholderTextColor={colors.inkFaint}
            autoCapitalize="none"
            autoCorrect={false}
            returnKeyType="search"
            clearButtonMode="while-editing"
          />
        </View>
        {state.kind === "ready" && state.understood ? (
          <UnderstoodRow understood={state.understood} q={debouncedQuery} />
        ) : null}
      </View>
      <Body
        state={state}
        settings={settings}
        refreshing={refreshing}
        searching={searching}
        onRefresh={() => void load(true, debouncedQuery)}
        onRetry={() => void load(false, debouncedQuery)}
        onOpen={onOpen}
      />
    </SafeAreaView>
  );
}

function UnderstoodRow({ understood, q }: { understood: UnderstoodQuery; q: string }) {
  const chips: string[] = [];
  const text = understood.text?.trim();
  if (text && text !== q) chips.push(text);
  if (understood.color) chips.push(understood.color);
  for (const t of understood.types ?? []) chips.push(t);
  if (chips.length === 0) return null;
  return (
    <View style={styles.understoodRow}>
      <Text style={styles.understoodLabel}>understood</Text>
      {chips.map((chip) => (
        <View key={chip} style={styles.chip}>
          <Text style={styles.chipText}>{chip}</Text>
        </View>
      ))}
    </View>
  );
}

type BodyProps = {
  state: LoadState;
  settings: ReturnType<typeof useSettingsContext>["settings"];
  refreshing: boolean;
  searching: boolean;
  onRefresh: () => void;
  onRetry: () => void;
  onOpen: (item: Item) => void;
};

function Body({ state, settings, refreshing, searching, onRefresh, onRetry, onOpen }: BodyProps) {
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
  if (state.kind === "search-offline") {
    return <Message text="Search needs a connection." onRetry={onRetry} />;
  }
  if (state.kind === "rejected") {
    return <Message text="Token rejected — check Settings." onRetry={onRetry} />;
  }
  if (state.kind === "error") {
    return (
      <Message
        text={searching ? "Couldn't run that search." : "Couldn't load your library."}
        onRetry={onRetry}
      />
    );
  }

  return (
    <FlatList
      data={state.items}
      keyExtractor={(item) => item.id}
      renderItem={({ item }) => <ItemCard item={item} settings={settings} onPress={onOpen} />}
      contentContainerStyle={styles.list}
      ItemSeparatorComponent={() => <View style={styles.separator} />}
      refreshControl={
        <RefreshControl
          refreshing={refreshing}
          onRefresh={onRefresh}
          tintColor={colors.cobalt}
        />
      }
      ListEmptyComponent={
        <Message
          text={searching ? "No matches — try a different fragment." : "Nothing saved yet — capture something."}
        />
      }
    />
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
  topHairline: { height: 2, backgroundColor: colors.terracotta },
  header: { paddingHorizontal: spacing.xl, paddingTop: spacing.lg, paddingBottom: spacing.md },
  title: { fontFamily: fonts.serifBold, fontSize: 27, color: colors.ink },
  subtitle: {
    fontFamily: fonts.mono,
    fontSize: 12,
    color: colors.inkFaint,
    marginTop: spacing.xs,
  },
  searchCard: {
    marginTop: spacing.md,
    backgroundColor: colors.paper,
    borderRadius: radius.card,
    padding: 3,
  },
  searchInput: {
    borderWidth: 1,
    borderColor: colors.hairline,
    borderRadius: radius.button,
    backgroundColor: colors.cardSurface,
    paddingHorizontal: spacing.md,
    paddingVertical: spacing.sm + 2,
    fontFamily: fonts.sans,
    fontSize: 15,
    color: colors.ink,
  },
  searchInputFocused: { borderColor: colors.cobalt, borderWidth: 1.5 },
  understoodRow: {
    flexDirection: "row",
    flexWrap: "wrap",
    alignItems: "center",
    gap: spacing.sm,
    marginTop: spacing.sm,
  },
  understoodLabel: {
    fontFamily: fonts.mono,
    fontSize: 10,
    letterSpacing: 0.6,
    textTransform: "uppercase",
    color: colors.inkFaint,
  },
  chip: {
    borderWidth: 1,
    borderColor: colors.hairline,
    borderRadius: radius.pill,
    paddingHorizontal: spacing.sm,
    paddingVertical: 2,
    backgroundColor: colors.paper,
  },
  chipText: { fontFamily: fonts.mono, fontSize: 11, color: colors.inkMuted },
  list: { paddingHorizontal: spacing.xl, paddingBottom: spacing.xxl, flexGrow: 1 },
  separator: { height: 14 },
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
});
