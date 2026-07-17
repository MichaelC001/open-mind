import { useQuery } from "@tanstack/react-query";
import { Redirect, useRouter } from "expo-router";
import { useCallback, useEffect, useState } from "react";
import {
  ActivityIndicator,
  FlatList,
  Pressable,
  RefreshControl,
  SectionList,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { ItemCard } from "@/components/ItemCard";
import { ApiError, listItems, searchItems, type Item, type UnderstoodQuery } from "@/lib/api";
import { cardKind, KNOWN_KINDS, typeLabelPlural } from "@/lib/cards";
import { useCaptureQueue } from "@/lib/capture-queue-context";
import { showItemActions, useAndroidActionSheet } from "@/lib/item-actions";
import { useDeleteItem, usePinItem } from "@/lib/mutations";
import { queryKeys } from "@/lib/query";
import { useSettingsContext } from "@/lib/settings-context";
import { colors, fonts, radius, spacing, type CardKind } from "@/lib/theme";
import { useSoftFocusRefetch } from "@/lib/use-soft-focus-refetch";

/** Group items by cardType, in a stable KNOWN_KINDS order, dropping empty groups. */
function groupByKind(items: Item[]): { kind: CardKind; items: Item[] }[] {
  const byKind = new Map<CardKind, Item[]>();
  for (const item of items) {
    const k = cardKind(item.cardType);
    const list = byKind.get(k);
    if (list) list.push(item);
    else byKind.set(k, [item]);
  }
  return KNOWN_KINDS.filter((k) => byKind.has(k)).map((k) => ({ kind: k, items: byKind.get(k)! }));
}

const SEARCH_DEBOUNCE_MS = 300;
const LIST_LIMIT = 50;

type LibraryData = { items: Item[]; understood?: UnderstoodQuery };

export default function LibraryScreen() {
  const router = useRouter();
  const { settings, configured, loading } = useSettingsContext();
  const { pendingCount, flush } = useCaptureQueue();
  const [query, setQuery] = useState("");
  const [debouncedQuery, setDebouncedQuery] = useState("");
  const [searchFocused, setSearchFocused] = useState(false);
  const [grouped, setGrouped] = useState(false);
  const pinItem = usePinItem();
  const deleteItem = useDeleteItem();
  const { present, node: actionSheet } = useAndroidActionSheet();

  useEffect(() => {
    const t = setTimeout(() => setDebouncedQuery(query.trim()), SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(t);
  }, [query]);

  const searching = debouncedQuery.length > 0;

  const listQuery = useQuery({
    queryKey: searching ? queryKeys.search(debouncedQuery) : queryKeys.items(LIST_LIMIT),
    enabled: !!settings && configured,
    queryFn: async (): Promise<LibraryData> => {
      if (!searching) {
        const res = await listItems(LIST_LIMIT);
        if (!res.ok) throw new ApiError(res.status);
        return { items: res.items };
      }
      const res = await searchItems({ q: debouncedQuery, parse: true });
      if (!res.ok) throw new ApiError(res.status);
      return {
        items: res.results.map((r) => r.item),
        understood: res.understood,
      };
    },
    // Keep prior results visible while a new search key loads.
    placeholderData: (prev) => prev,
  });

  useSoftFocusRefetch(listQuery, () => {
    void flush();
  });

  const onOpen = useCallback(
    (item: Item) => {
      router.push(`/item/${item.id}`);
    },
    [router],
  );

  const onLongPress = useCallback(
    (item: Item) => {
      showItemActions(
        item,
        {
          onOpen,
          onPin: (it) => void pinItem(it),
          onDelete: (it) => deleteItem(it),
        },
        present,
      );
    },
    [onOpen, pinItem, deleteItem, present],
  );

  if (!loading && !configured) return <Redirect href="/settings" />;

  const items = listQuery.data?.items ?? [];
  const count = items.length;
  const errStatus = listQuery.error instanceof ApiError ? listQuery.error.status : undefined;

  return (
    <SafeAreaView style={styles.safe} edges={["top", "left", "right"]}>
      {actionSheet}
      <View style={styles.topHairline} />
      <View style={styles.header}>
        <View style={styles.titleRow}>
          <Text style={styles.title}>The Mind</Text>
          {!searching ? (
            <Pressable
              style={({ pressed }) => [styles.groupToggle, pressed && styles.groupTogglePressed]}
              onPress={() => setGrouped((g) => !g)}
            >
              <Text style={styles.groupToggleText}>{grouped ? "List" : "Group by type"}</Text>
            </Pressable>
          ) : null}
        </View>
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
        {listQuery.data?.understood ? (
          <UnderstoodRow understood={listQuery.data.understood} q={debouncedQuery} />
        ) : null}
      </View>
      <Body
        isPending={listQuery.isPending && !listQuery.data}
        isError={listQuery.isError}
        errStatus={errStatus}
        searching={searching}
        items={items}
        settings={settings}
        refreshing={listQuery.isRefetching && !listQuery.isPending}
        grouped={grouped && !searching}
        onRefresh={() => void listQuery.refetch()}
        onRetry={() => void listQuery.refetch()}
        onOpen={onOpen}
        onLongPress={onLongPress}
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
  isPending: boolean;
  isError: boolean;
  errStatus?: number;
  searching: boolean;
  items: Item[];
  settings: ReturnType<typeof useSettingsContext>["settings"];
  refreshing: boolean;
  grouped: boolean;
  onRefresh: () => void;
  onRetry: () => void;
  onOpen: (item: Item) => void;
  onLongPress: (item: Item) => void;
};

function Body({
  isPending,
  isError,
  errStatus,
  searching,
  items,
  settings,
  refreshing,
  grouped,
  onRefresh,
  onRetry,
  onOpen,
  onLongPress,
}: BodyProps) {
  if (isPending) {
    return (
      <View style={styles.centre}>
        <ActivityIndicator color={colors.cobalt} />
      </View>
    );
  }

  if (isError && items.length === 0) {
    if (errStatus === 0) {
      return (
        <Message
          text={
            searching
              ? "Search needs a connection."
              : "Instance unreachable — check your connection or the URL in Settings."
          }
          onRetry={onRetry}
        />
      );
    }
    if (errStatus === 401) {
      return <Message text="Token rejected — check Settings." onRetry={onRetry} />;
    }
    return (
      <Message
        text={searching ? "Couldn't run that search." : "Couldn't load your library."}
        onRetry={onRetry}
      />
    );
  }

  const emptyMessage = (
    <Message
      text={searching ? "No matches — try a different fragment." : "Nothing saved yet — capture something."}
    />
  );
  const refreshControl = (
    <RefreshControl refreshing={refreshing} onRefresh={onRefresh} tintColor={colors.cobalt} />
  );

  if (grouped) {
    const sections = groupByKind(items).map(({ kind, items: sectionItems }) => ({
      title: `${typeLabelPlural[kind]} · ${sectionItems.length}`,
      data: sectionItems,
    }));
    return (
      <SectionList
        sections={sections}
        keyExtractor={(item) => item.id}
        renderItem={({ item }) => (
          <ItemCard item={item} settings={settings} onPress={onOpen} onLongPress={onLongPress} />
        )}
        renderSectionHeader={({ section }) => (
          <Text style={styles.sectionHeader}>{section.title}</Text>
        )}
        contentContainerStyle={styles.list}
        ItemSeparatorComponent={() => <View style={styles.separator} />}
        refreshControl={refreshControl}
        ListEmptyComponent={emptyMessage}
      />
    );
  }

  return (
    <FlatList
      data={items}
      keyExtractor={(item) => item.id}
      renderItem={({ item }) => (
        <ItemCard item={item} settings={settings} onPress={onOpen} onLongPress={onLongPress} />
      )}
      contentContainerStyle={styles.list}
      ItemSeparatorComponent={() => <View style={styles.separator} />}
      refreshControl={refreshControl}
      ListEmptyComponent={emptyMessage}
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
  titleRow: { flexDirection: "row", alignItems: "center", justifyContent: "space-between" },
  title: { fontFamily: fonts.serifBold, fontSize: 27, color: colors.ink },
  groupToggle: {
    borderWidth: 1,
    borderColor: colors.hairline,
    borderRadius: radius.pill,
    paddingHorizontal: spacing.md,
    paddingVertical: 6,
    backgroundColor: colors.paper,
  },
  groupTogglePressed: { opacity: 0.7 },
  groupToggleText: { fontFamily: fonts.mono, fontSize: 11, color: colors.inkMuted },
  sectionHeader: {
    fontFamily: fonts.monoMedium,
    fontSize: 11,
    letterSpacing: 0.6,
    textTransform: "uppercase",
    color: colors.inkFaint,
    backgroundColor: colors.canvas,
    paddingTop: spacing.md,
    paddingBottom: spacing.sm,
  },
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
