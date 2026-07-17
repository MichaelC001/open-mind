import * as Clipboard from "expo-clipboard";
import { openBrowserAsync } from "expo-web-browser";
import { useCallback, useState } from "react";
import {
  ActionSheetIOS,
  Alert,
  Modal,
  Platform,
  Pressable,
  Share,
  StyleSheet,
  Text,
  View,
} from "react-native";
import type { Item } from "./api";
import { colors, fonts, radius, spacing } from "./theme";

export type ItemActionHandlers = {
  onOpen: (item: Item) => void;
  onPin?: (item: Item) => void;
  onKeep?: (item: Item) => void;
  onDelete?: (item: Item) => void;
};

type SheetOption = {
  label: string;
  style?: "destructive" | "cancel";
  run?: () => void | Promise<void>;
};

function buildOptions(item: Item, handlers: ItemActionHandlers): SheetOption[] {
  const pinned = !!item.pinnedAt;
  const kept = !!item.keptAt;
  const options: SheetOption[] = [{ label: "Open", run: () => handlers.onOpen(item) }];

  if (handlers.onPin) {
    options.push({
      label: pinned ? "Unpin from desk" : "Pin to desk",
      run: () => handlers.onPin?.(item),
    });
  }

  if (handlers.onKeep) {
    options.push({
      label: kept ? "Unkeep" : "Keep in library",
      run: () => handlers.onKeep?.(item),
    });
  }

  if (item.url) {
    options.push({
      label: "Open original",
      run: () => void openBrowserAsync(item.url),
    });
    options.push({
      label: "Copy link",
      run: () => void Clipboard.setStringAsync(item.url),
    });
    options.push({
      label: "Share…",
      run: () =>
        void Share.share({
          message: item.title?.trim() ? `${item.title.trim()}\n${item.url}` : item.url,
          url: item.url,
        }),
    });
  }

  if (handlers.onDelete) {
    options.push({
      label: "Delete",
      style: "destructive",
      run: () => handlers.onDelete?.(item),
    });
  }

  options.push({ label: "Cancel", style: "cancel" });
  return options;
}

/**
 * iOS: native ActionSheet. Android/web: in-app bottom sheet (Alert is capped
 * at 3 buttons on Android, which is too few for pin/open/copy/share/delete).
 */
export function showItemActions(
  item: Item,
  handlers: ItemActionHandlers,
  presentAndroid?: (opts: { title: string; options: SheetOption[] }) => void,
): void {
  const title = item.title?.trim() || item.url || "Item";
  const options = buildOptions(item, handlers);

  if (Platform.OS === "ios") {
    const destructiveButtonIndex = options.findIndex((o) => o.style === "destructive");
    ActionSheetIOS.showActionSheetWithOptions(
      {
        title,
        options: options.map((o) => o.label),
        cancelButtonIndex: options.length - 1,
        ...(destructiveButtonIndex >= 0 ? { destructiveButtonIndex } : {}),
      },
      (index) => {
        const opt = options[index];
        if (opt?.run) void opt.run();
      },
    );
    return;
  }

  if (presentAndroid) {
    presentAndroid({ title, options });
    return;
  }

  // Fallback when no host modal is wired — should not happen in app screens.
  const first = options.find((o) => o.style !== "cancel" && o.run);
  if (first?.run) void first.run();
}

/** Confirm then delete — shared by Library, Desk, and detail. */
export function confirmDelete(item: Item, onConfirm: () => void | Promise<void>): void {
  const label = item.title?.trim() || item.url || "this item";
  Alert.alert(`Delete "${label}"?`, "This can't be undone.", [
    { text: "Cancel", style: "cancel" },
    {
      text: "Delete",
      style: "destructive",
      onPress: () => void onConfirm(),
    },
  ]);
}

type SheetState = { title: string; options: SheetOption[] } | null;

/**
 * Host for the Android/web action sheet. Call `present` from long-press
 * handlers; render once near the screen root.
 */
export function useAndroidActionSheet() {
  const [sheet, setSheet] = useState<SheetState>(null);

  const present = useCallback((next: { title: string; options: SheetOption[] }) => {
    setSheet(next);
  }, []);

  const close = useCallback(() => setSheet(null), []);

  const node =
    Platform.OS === "ios" ? null : (
      <Modal visible={!!sheet} transparent animationType="fade" onRequestClose={close}>
        <View style={styles.backdrop}>
          <Pressable style={StyleSheet.absoluteFill} onPress={close} />
          <View style={styles.sheet}>
            {sheet ? <Text style={styles.sheetTitle}>{sheet.title}</Text> : null}
            {sheet?.options.map((opt, i) => {
              const isCancel = opt.style === "cancel";
              const isDestructive = opt.style === "destructive";
              return (
                <Pressable
                  key={`${opt.label}-${i}`}
                  style={({ pressed }) => [
                    styles.sheetRow,
                    isCancel && styles.sheetCancel,
                    pressed && styles.sheetRowPressed,
                  ]}
                  onPress={() => {
                    close();
                    if (opt.run) void opt.run();
                  }}
                >
                  <Text
                    style={[
                      styles.sheetRowText,
                      isDestructive && styles.sheetDestructive,
                      isCancel && styles.sheetCancelText,
                    ]}
                  >
                    {opt.label}
                  </Text>
                </Pressable>
              );
            })}
          </View>
        </View>
      </Modal>
    );

  return { present, node };
}

const styles = StyleSheet.create({
  backdrop: {
    flex: 1,
    backgroundColor: "rgba(28,26,22,0.45)",
    justifyContent: "flex-end",
  },
  sheet: {
    backgroundColor: colors.paper,
    borderTopLeftRadius: radius.overlay,
    borderTopRightRadius: radius.overlay,
    paddingBottom: spacing.xxl,
    paddingTop: spacing.md,
  },
  sheetTitle: {
    fontFamily: fonts.serifBold,
    fontSize: 16,
    color: colors.ink,
    paddingHorizontal: spacing.xl,
    paddingBottom: spacing.md,
  },
  sheetRow: {
    paddingHorizontal: spacing.xl,
    paddingVertical: spacing.md + 2,
    borderTopWidth: StyleSheet.hairlineWidth,
    borderTopColor: colors.hairline,
  },
  sheetRowPressed: { backgroundColor: colors.canvas },
  sheetRowText: { fontFamily: fonts.sans, fontSize: 16, color: colors.ink },
  sheetDestructive: { color: colors.danger, fontFamily: fonts.sansSemiBold },
  sheetCancel: { marginTop: spacing.sm },
  sheetCancelText: { color: colors.inkMuted, textAlign: "center" },
});
