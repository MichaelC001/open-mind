// React surface over the durable capture queue: pending count for badges,
// flush helper, and AppState / NetInfo / focus-driven sync.
import NetInfo from "@react-native-community/netinfo";
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { AppState, type AppStateStatus } from "react-native";
import {
  enqueue as enqueueRaw,
  flushQueue,
  listQueued,
  subscribeQueue,
  type QueuedCapture,
} from "./capture-queue";

type CaptureQueueContextValue = {
  pending: QueuedCapture[];
  pendingCount: number;
  flushing: boolean;
  refresh: () => Promise<void>;
  enqueue: (input: { url?: string; note?: string }) => Promise<{ id: string; deduped: boolean }>;
  flush: () => Promise<{ sent: number; remaining: number }>;
};

const CaptureQueueContext = createContext<CaptureQueueContextValue | null>(null);

export function CaptureQueueProvider({ children }: { children: React.ReactNode }) {
  const [pending, setPending] = useState<QueuedCapture[]>([]);
  const [flushing, setFlushing] = useState(false);
  const wasConnected = useRef<boolean | null>(null);

  const refresh = useCallback(async () => {
    setPending(await listQueued());
  }, []);

  const flush = useCallback(async () => {
    setFlushing(true);
    try {
      return await flushQueue();
    } finally {
      setFlushing(false);
      setPending(await listQueued());
    }
  }, []);

  const enqueue = useCallback(async (input: { url?: string; note?: string }) => {
    const result = await enqueueRaw(input);
    setPending(await listQueued());
    return result;
  }, []);

  useEffect(() => {
    void refresh();
    return subscribeQueue(setPending);
  }, [refresh]);

  // Flush when the app returns to the foreground.
  useEffect(() => {
    const onChange = (next: AppStateStatus) => {
      if (next === "active") void flush();
    };
    const sub = AppState.addEventListener("change", onChange);
    // Initial mount flush.
    void flush();
    return () => sub.remove();
  }, [flush]);

  // Flush when connectivity returns.
  useEffect(() => {
    const unsub = NetInfo.addEventListener((state) => {
      const connected = !!(state.isConnected && state.isInternetReachable !== false);
      if (wasConnected.current === false && connected) {
        void flush();
      }
      wasConnected.current = connected;
    });
    return () => unsub();
  }, [flush]);

  const value = useMemo<CaptureQueueContextValue>(
    () => ({
      pending,
      pendingCount: pending.length,
      flushing,
      refresh,
      enqueue,
      flush,
    }),
    [pending, flushing, refresh, enqueue, flush],
  );

  return (
    <CaptureQueueContext.Provider value={value}>{children}</CaptureQueueContext.Provider>
  );
}

export function useCaptureQueue(): CaptureQueueContextValue {
  const ctx = useContext(CaptureQueueContext);
  if (!ctx) throw new Error("useCaptureQueue must be used within a CaptureQueueProvider");
  return ctx;
}
