import { useQuery } from "@tanstack/react-query";
import { Redirect, useRouter } from "expo-router";
import { useCallback } from "react";
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
import { ItemCard } from "@/components/ItemCard";
import { ApiError, listDesk, type Item } from "@/lib/api";
import { showItemActions, useAndroidActionSheet } from "@/lib/item-actions";
import { useDeleteItem, usePinItem } from "@/lib/mutations";
import { queryKeys } from "@/lib/query";
import { useSettingsContext } from "@/lib/settings-context";
import { colors, fonts, radius, spacing } from "@/lib/theme";
import { useSoftFocusRefetch } from "@/lib/use-soft-focus-refetch";

export default function DeskScreen() {
  const router = useRouter();
  const { settings, configured, loading } = useSettingsContext();
  const pinItem = usePinItem();
  const deleteItem = useDeleteItem();
  const { present, node: actionSheet } = useAndroidActionSheet();

  const deskQuery = useQuery({
    queryKey: queryKeys.desk(),
    enabled: !!settings && configured,
    queryFn: async () => {
      const res = await listDesk();
      if (!res.ok) throw new ApiError(res.status);
      return res.items;
    },
  });

  useSoftFocusRefetch(deskQuery);

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

  const items = deskQuery.data ?? [];
  const count = items.length;
  const errStatus = deskQuery.error instanceof ApiError ? deskQuery.error.status : undefined;

  return (
    <SafeAreaView style={styles.safe} edges={["top", "left", "right"]}>
      {actionSheet}
      <View style={styles.topHairline} />
      <View style={styles.header}>
        <Text style={styles.title}>Desk</Text>
        <Text style={styles.subtitle}>
          {count} {count === 1 ? "pin" : "pins"} · what you're working with
        </Text>
      </View>
      <Body
        isPending={deskQuery.isPending && !deskQuery.data}
        isError={deskQuery.isError}
        errStatus={errStatus}
        items={items}
        settings={settings}
        refreshing={deskQuery.isRefetching && !deskQuery.isPending}
        onRefresh={() => void deskQuery.refetch()}
        onRetry={() => void deskQuery.refetch()}
        onOpen={onOpen}
        onLongPress={onLongPress}
      />
    </SafeAreaView>
  );
}

type BodyProps = {
  isPending: boolean;
  isError: boolean;
  errStatus?: number;
  items: Item[];
  settings: ReturnType<typeof useSettingsContext>["settings"];
  refreshing: boolean;
  onRefresh: () => void;
  onRetry: () => void;
  onOpen: (item: Item) => void;
  onLongPress: (item: Item) => void;
};

function Body({
  isPending,
  isError,
  errStatus,
  items,
  settings,
  refreshing,
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
          text="Instance unreachable — check your connection or the URL in Settings."
          onRetry={onRetry}
        />
      );
    }
    if (errStatus === 401) {
      return <Message text="Token rejected — check Settings." onRetry={onRetry} />;
    }
    return <Message text="Couldn't load your desk." onRetry={onRetry} />;
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
      refreshControl={
        <RefreshControl refreshing={refreshing} onRefresh={onRefresh} tintColor={colors.cobalt} />
      }
      ListEmptyComponent={
        <Message text="Nothing pinned yet — long-press a card and choose Pin to desk." />
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
