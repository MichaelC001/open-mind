import { useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { Alert } from "react-native";
import { deleteItem, setKept, setPinned, type Item } from "./api";
import { confirmDelete } from "./item-actions";
import { queryKeys } from "./query";

type LibraryData = { items: Item[]; understood?: unknown };

/** Patch an item across every list cache that might hold it. */
function patchItemInCaches(
  qc: ReturnType<typeof useQueryClient>,
  id: string,
  patch: Partial<Item>,
) {
  const apply = (items: Item[] | undefined) =>
    items?.map((it) => (it.id === id ? { ...it, ...patch } : it));

  qc.setQueriesData<Item[]>({ queryKey: ["feed"] }, apply);
  qc.setQueriesData<Item[]>({ queryKey: queryKeys.desk() }, (prev) => {
    if (!prev) return prev;
    // Unpinning removes from desk immediately.
    if (patch.pinnedAt === null) return prev.filter((it) => it.id !== id);
    // Pinning: if the item isn't on desk yet, leave lists to invalidate
    // (we may not have the full item here). Just patch badge fields if present.
    return apply(prev) ?? prev;
  });
  qc.setQueriesData<LibraryData>({ queryKey: ["items"] }, (prev) =>
    prev ? { ...prev, items: apply(prev.items) ?? prev.items } : prev,
  );
  qc.setQueriesData<LibraryData>({ queryKey: ["search"] }, (prev) =>
    prev ? { ...prev, items: apply(prev.items) ?? prev.items } : prev,
  );
  qc.setQueryData(queryKeys.item(id), (prev: Item | undefined) =>
    prev ? { ...prev, ...patch } : prev,
  );
}

/** Invalidate list caches after a mutation so Library / Feed / Desk stay in sync. */
export function useInvalidateLists() {
  const qc = useQueryClient();
  return useCallback(() => {
    void qc.invalidateQueries({ queryKey: ["items"] });
    void qc.invalidateQueries({ queryKey: ["search"] });
    void qc.invalidateQueries({ queryKey: ["feed"] });
    void qc.invalidateQueries({ queryKey: queryKeys.desk() });
  }, [qc]);
}

/** Pin toggle with optimistic cache patch + list invalidation. */
export function usePinItem() {
  const invalidate = useInvalidateLists();
  const qc = useQueryClient();

  return useCallback(
    async (item: Item) => {
      const next = !item.pinnedAt;
      const pinnedAt = next ? new Date().toISOString() : null;
      patchItemInCaches(qc, item.id, { pinnedAt });
      // When pinning, prepend onto desk cache so Desk updates without waiting.
      if (next) {
        qc.setQueriesData<Item[]>({ queryKey: queryKeys.desk() }, (prev) => {
          if (!prev) return prev;
          if (prev.some((it) => it.id === item.id)) {
            return prev.map((it) => (it.id === item.id ? { ...it, pinnedAt } : it));
          }
          return [{ ...item, pinnedAt }, ...prev];
        });
      }
      const res = await setPinned(item.id, next);
      if (!res.ok) {
        patchItemInCaches(qc, item.id, { pinnedAt: item.pinnedAt ?? null });
        if (next) {
          // Roll back the optimistic desk prepend.
          qc.setQueriesData<Item[]>({ queryKey: queryKeys.desk() }, (prev) =>
            prev?.filter((it) => it.id !== item.id),
          );
        }
        Alert.alert("Couldn't update pin", "Please try again.");
        return;
      }
      invalidate();
    },
    [invalidate, qc],
  );
}

/** Keep toggle with optimistic cache patch + list invalidation. */
export function useKeepItem() {
  const invalidate = useInvalidateLists();
  const qc = useQueryClient();

  return useCallback(
    async (item: Item) => {
      const next = !item.keptAt;
      const keptAt = next ? new Date().toISOString() : null;
      patchItemInCaches(qc, item.id, { keptAt });
      const res = await setKept(item.id, next);
      if (!res.ok) {
        patchItemInCaches(qc, item.id, { keptAt: item.keptAt ?? null });
        Alert.alert("Couldn't update", "Please try again.");
        return;
      }
      invalidate();
    },
    [invalidate, qc],
  );
}

/** Confirm + delete, then drop from list caches immediately. */
export function useDeleteItem() {
  const invalidate = useInvalidateLists();
  const qc = useQueryClient();

  return useCallback(
    (item: Item, after?: () => void) => {
      confirmDelete(item, async () => {
        const res = await deleteItem(item.id);
        if (!res.ok) {
          Alert.alert("Couldn't delete", "Please try again.");
          return;
        }
        void qc.removeQueries({ queryKey: queryKeys.item(item.id) });
        // Drop from list caches immediately so back-nav doesn't flash the gone card.
        qc.setQueriesData<Item[]>({ queryKey: ["feed"] }, (prev) =>
          prev?.filter((it) => it.id !== item.id),
        );
        qc.setQueriesData<Item[]>({ queryKey: queryKeys.desk() }, (prev) =>
          prev?.filter((it) => it.id !== item.id),
        );
        qc.setQueriesData<LibraryData>({ queryKey: ["items"] }, (prev) =>
          prev ? { ...prev, items: prev.items.filter((it) => it.id !== item.id) } : prev,
        );
        qc.setQueriesData<LibraryData>({ queryKey: ["search"] }, (prev) =>
          prev ? { ...prev, items: prev.items.filter((it) => it.id !== item.id) } : prev,
        );
        invalidate();
        after?.();
      });
    },
    [invalidate, qc],
  );
}
