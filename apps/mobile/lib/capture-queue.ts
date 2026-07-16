// Durable offline capture queue. When POST /api/items fails with a network
// error (status 0), the Capture screen enqueues the payload here. Flush walks
// oldest-first and calls saveItem; successes are removed, 401 stops the walk
// so a bad token does not burn the queue.
import AsyncStorage from "@react-native-async-storage/async-storage";
import { saveItem } from "./api";

const QUEUE_KEY = "openmind.captureQueue";
const MAX_QUEUE = 100;

export type QueuedCapture = {
  id: string;
  url?: string;
  note?: string;
  createdAt: number;
  attempts: number;
};

type QueueListener = (items: QueuedCapture[]) => void;

const listeners = new Set<QueueListener>();

function notify(items: QueuedCapture[]) {
  for (const listener of listeners) listener(items);
}

export function subscribeQueue(listener: QueueListener): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

function newId(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}

async function readQueue(): Promise<QueuedCapture[]> {
  try {
    const raw = await AsyncStorage.getItem(QUEUE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as unknown;
    if (!Array.isArray(parsed)) return [];
    return parsed.filter(
      (row): row is QueuedCapture =>
        !!row &&
        typeof row === "object" &&
        typeof (row as QueuedCapture).id === "string" &&
        typeof (row as QueuedCapture).createdAt === "number",
    );
  } catch {
    return [];
  }
}

async function writeQueue(items: QueuedCapture[]): Promise<void> {
  await AsyncStorage.setItem(QUEUE_KEY, JSON.stringify(items));
  notify(items);
}

export async function listQueued(): Promise<QueuedCapture[]> {
  return readQueue();
}

/**
 * Enqueue a capture. URL saves dedupe against an already-pending URL (returns
 * the existing id). Cap is MAX_QUEUE — oldest entries are dropped with a
 * console warning when exceeded.
 */
export async function enqueue(input: {
  url?: string;
  note?: string;
}): Promise<{ id: string; deduped: boolean }> {
  const url = input.url?.trim();
  const note = input.note?.trim();
  if (!url && !note) throw new Error("enqueue requires url or note");

  let items = await readQueue();
  if (url) {
    const existing = items.find((q) => q.url === url);
    if (existing) return { id: existing.id, deduped: true };
  }

  const entry: QueuedCapture = {
    id: newId(),
    url: url || undefined,
    note: url ? undefined : note,
    createdAt: Date.now(),
    attempts: 0,
  };
  items = [...items, entry];
  if (items.length > MAX_QUEUE) {
    const dropped = items.length - MAX_QUEUE;
    console.warn(`[capture-queue] cap ${MAX_QUEUE} exceeded; dropping ${dropped} oldest`);
    items = items.slice(dropped);
  }
  await writeQueue(items);
  return { id: entry.id, deduped: false };
}

export async function removeQueued(id: string): Promise<void> {
  const items = await readQueue();
  const next = items.filter((q) => q.id !== id);
  if (next.length === items.length) return;
  await writeQueue(next);
}

let flushing = false;

/**
 * Flush the queue oldest-first. Returns how many were sent and how many remain.
 * Stops on 401 so a rejected token does not keep retrying every entry.
 */
export async function flushQueue(): Promise<{ sent: number; remaining: number }> {
  if (flushing) {
    const items = await readQueue();
    return { sent: 0, remaining: items.length };
  }
  flushing = true;
  let sent = 0;
  try {
    let items = await readQueue();
    for (const entry of [...items].sort((a, b) => a.createdAt - b.createdAt)) {
      const payload = entry.url ? { url: entry.url } : { note: entry.note ?? "" };
      const res = await saveItem(payload);
      if (res.ok) {
        sent += 1;
        items = items.filter((q) => q.id !== entry.id);
        await writeQueue(items);
        continue;
      }
      if (res.status === 401) {
        break;
      }
      items = items.map((q) =>
        q.id === entry.id ? { ...q, attempts: q.attempts + 1 } : q,
      );
      await writeQueue(items);
      // Network / server errors: stop this pass; next reconnect/focus retries.
      break;
    }
    return { sent, remaining: items.length };
  } finally {
    flushing = false;
  }
}
