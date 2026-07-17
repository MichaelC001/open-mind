import { Ionicons } from "@expo/vector-icons";
import { Link, useFocusEffect, useLocalSearchParams } from "expo-router";
import { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  KeyboardAvoidingView,
  Platform,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { PressScale } from "@/components/PressScale";
import { listItems, saveItem } from "@/lib/api";
import { useCaptureQueue } from "@/lib/capture-queue-context";
import { useInvalidateLists } from "@/lib/mutations";
import { useSettingsContext } from "@/lib/settings-context";
import { colors, fonts, radius, spacing } from "@/lib/theme";

const URL_RE = /^https?:\/\//i;
const CLOCK_SKEW_MS = 30 * 1000;

type Status =
  | { kind: "idle" }
  | { kind: "saving" }
  | { kind: "saved"; recovered?: boolean }
  | { kind: "queued" }
  | { kind: "rejected" }
  | { kind: "error" };

// After a status-0 (network error/timeout) response, the POST may have
// actually landed. For URL saves we can check: list the newest items and look
// for this exact URL created since THIS attempt started. Anchoring to the
// attempt's start (not a rolling window) matters: the same URL saved
// deliberately twice in a row must not have its second, timed-out save
// "recovered" by finding the first copy. The skew buffer absorbs client/server
// clock drift.
async function wasRecentlySaved(url: string, attemptStartedAt: number): Promise<boolean> {
  const res = await listItems(10);
  if (!res.ok) return false;
  return res.items.some((item) => {
    if (item.url !== url || !item.createdAt) return false;
    const created = Date.parse(item.createdAt);
    return !Number.isNaN(created) && created >= attemptStartedAt - CLOCK_SKEW_MS;
  });
}

export default function CaptureScreen() {
  const { configured, loading } = useSettingsContext();
  const { pendingCount, flushing, enqueue, flush } = useCaptureQueue();
  const invalidateLists = useInvalidateLists();
  const params = useLocalSearchParams<{ shared?: string }>();
  const [text, setText] = useState("");
  const [status, setStatus] = useState<Status>({ kind: "idle" });
  const [focused, setFocused] = useState(false);

  // Prefill from a share-sheet intent (routed here by ShareIntentGate). Apply
  // each distinct shared value once so revisiting the tab doesn't clobber edits.
  const appliedShareRef = useRef<string | undefined>(undefined);
  useEffect(() => {
    const shared = typeof params.shared === "string" ? params.shared : undefined;
    if (shared && shared !== appliedShareRef.current) {
      appliedShareRef.current = shared;
      setText(shared);
      setStatus({ kind: "idle" });
    }
  }, [params.shared]);

  useFocusEffect(
    useCallback(() => {
      void flush();
    }, [flush]),
  );

  const trimmed = text.trim();
  const isUrl = URL_RE.test(trimmed);
  const saving = status.kind === "saving";
  const canSave = trimmed.length > 0 && !saving;

  async function onSave() {
    const value = text.trim();
    if (!value) return;
    const url = URL_RE.test(value);
    setStatus({ kind: "saving" });
    const attemptStartedAt = Date.now();
    const res = await saveItem(url ? { url: value } : { note: value });
    if (res.ok) {
      setText("");
      setStatus({ kind: "saved" });
      invalidateLists();
    } else if (res.status === 0) {
      if (url && (await wasRecentlySaved(value, attemptStartedAt))) {
        setText("");
        setStatus({ kind: "saved", recovered: true });
        invalidateLists();
        return;
      }
      // Durable offline queue — clear the field so the user can keep capturing.
      await enqueue(url ? { url: value } : { note: value });
      setText("");
      setStatus({ kind: "queued" });
    } else if (res.status === 401) {
      setStatus({ kind: "rejected" });
    } else {
      setStatus({ kind: "error" });
    }
  }

  async function onSyncNow() {
    const result = await flush();
    if (result.sent > 0) {
      invalidateLists();
    }
    if (result.sent > 0 && result.remaining === 0) {
      setStatus({ kind: "saved" });
    }
  }

  return (
    <SafeAreaView style={styles.safe} edges={["top", "left", "right"]}>
      <KeyboardAvoidingView
        style={styles.flex}
        behavior={Platform.OS === "ios" ? "padding" : undefined}
      >
        <ScrollView contentContainerStyle={styles.container} keyboardShouldPersistTaps="handled">
          <Text style={styles.title}>Capture</Text>
          <Text style={styles.subtitle}>Save a link or a note — enriches in place</Text>

          {loading ? null : configured ? (
            <>
              {pendingCount > 0 ? (
                <View style={styles.pendingStrip}>
                  <Text style={styles.pendingText}>
                    {pendingCount} waiting to sync
                  </Text>
                  <PressScale onPress={onSyncNow} disabled={flushing}>
                    <View style={[styles.syncButton, flushing && styles.buttonDisabled]}>
                      {flushing ? (
                        <ActivityIndicator color={colors.cobalt} size="small" />
                      ) : (
                        <Text style={styles.syncButtonText}>Sync now</Text>
                      )}
                    </View>
                  </PressScale>
                </View>
              ) : null}

              <View style={styles.field}>
                <Text style={styles.label}>{isUrl ? "LINK" : "URL OR NOTE"}</Text>
                <View style={styles.inputCard}>
                  <TextInput
                    style={[styles.input, focused && styles.inputFocused]}
                    value={text}
                    onChangeText={(next) => {
                      setText(next);
                      if (status.kind !== "idle" && status.kind !== "saving") {
                        setStatus({ kind: "idle" });
                      }
                    }}
                    onFocus={() => setFocused(true)}
                    onBlur={() => setFocused(false)}
                    placeholder="Paste a URL or jot a note…"
                    placeholderTextColor={colors.inkFaint}
                    multiline
                    autoCapitalize="none"
                    autoCorrect={false}
                    textAlignVertical="top"
                  />
                </View>
              </View>

              <StatusMessage status={status} />

              <PressScale onPress={onSave} disabled={!canSave}>
                <View style={[styles.primaryButton, !canSave && styles.buttonDisabled]}>
                  {saving ? (
                    <ActivityIndicator color={colors.paper} />
                  ) : (
                    <Text style={styles.primaryButtonText}>Save</Text>
                  )}
                </View>
              </PressScale>
            </>
          ) : (
            <View style={styles.placeholder}>
              <Text style={styles.placeholderText}>
                Connect to your Openmind instance before capturing.
              </Text>
              <Link href="/settings" style={styles.link}>
                Open Settings
              </Link>
            </View>
          )}
        </ScrollView>
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}

function StatusMessage({ status }: { status: Status }) {
  switch (status.kind) {
    case "saved":
      return (
        <View style={styles.savedRow}>
          <Ionicons name="checkmark-circle" size={16} color={colors.cobalt} />
          <Text style={[styles.status, { color: colors.cobalt }]}>
            {status.recovered ? "Saved — connection was slow." : "Saved — it'll appear in your Library."}
          </Text>
        </View>
      );
    case "queued":
      return (
        <View style={styles.savedRow}>
          <Ionicons name="cloud-upload-outline" size={16} color={colors.inkMuted} />
          <Text style={[styles.status, { color: colors.inkMuted }]}>
            Queued — will sync when you’re back online.
          </Text>
        </View>
      );
    case "rejected":
      return <Text style={[styles.status, { color: colors.danger }]}>Token rejected — check Settings.</Text>;
    case "error":
      return <Text style={[styles.status, { color: colors.danger }]}>Couldn't save — try again.</Text>;
    default:
      return null;
  }
}

const styles = StyleSheet.create({
  safe: { flex: 1, backgroundColor: colors.canvas },
  flex: { flex: 1 },
  container: { paddingHorizontal: spacing.xl, paddingTop: spacing.lg, paddingBottom: spacing.xxl },
  title: { fontFamily: fonts.serifBold, fontSize: 27, color: colors.ink },
  subtitle: {
    fontFamily: fonts.mono,
    fontSize: 12,
    color: colors.inkFaint,
    marginTop: spacing.xs,
    marginBottom: spacing.xl,
  },
  pendingStrip: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    gap: spacing.md,
    backgroundColor: colors.paper,
    borderWidth: 1,
    borderColor: colors.hairline,
    borderRadius: radius.card,
    paddingHorizontal: spacing.md,
    paddingVertical: spacing.sm,
    marginBottom: spacing.lg,
  },
  pendingText: { fontFamily: fonts.sansMedium, fontSize: 13, color: colors.inkMuted, flex: 1 },
  syncButton: {
    borderWidth: 1,
    borderColor: colors.cobalt,
    borderRadius: radius.button,
    paddingHorizontal: spacing.md,
    paddingVertical: spacing.xs + 2,
    minWidth: 84,
    alignItems: "center",
  },
  syncButtonText: { fontFamily: fonts.sansSemiBold, fontSize: 13, color: colors.cobalt },
  field: { marginBottom: spacing.lg },
  label: {
    fontFamily: fonts.monoMedium,
    fontSize: 10,
    letterSpacing: 0.8,
    textTransform: "uppercase",
    color: colors.inkMuted,
    marginBottom: spacing.sm,
  },
  inputCard: {
    backgroundColor: colors.paper,
    borderRadius: radius.card,
    padding: 3,
  },
  input: {
    borderWidth: 1,
    borderColor: colors.hairline,
    borderRadius: radius.button,
    backgroundColor: colors.cardSurface,
    paddingHorizontal: spacing.md,
    paddingVertical: spacing.md,
    fontFamily: fonts.sans,
    fontSize: 15,
    color: colors.ink,
    minHeight: 120,
  },
  inputFocused: { borderColor: colors.cobalt, borderWidth: 1.5 },
  savedRow: { flexDirection: "row", alignItems: "center", gap: spacing.xs, marginBottom: spacing.md },
  status: { fontFamily: fonts.sans, fontSize: 13, marginBottom: spacing.md, flexShrink: 1 },
  primaryButton: {
    backgroundColor: colors.cobalt,
    borderRadius: radius.button,
    paddingVertical: spacing.md,
    alignItems: "center",
    marginTop: spacing.sm,
  },
  primaryButtonText: { color: colors.paper, fontFamily: fonts.sansSemiBold, fontSize: 15 },
  buttonDisabled: { opacity: 0.4 },
  placeholder: {
    marginTop: spacing.xl,
    padding: spacing.lg,
    borderRadius: radius.card,
    borderWidth: 1,
    borderColor: colors.hairline,
    backgroundColor: colors.paper,
    gap: spacing.md,
  },
  placeholderText: { fontFamily: fonts.sans, fontSize: 14, color: colors.inkMuted, lineHeight: 20 },
  link: { fontFamily: fonts.sansSemiBold, fontSize: 14, color: colors.cobalt },
});
