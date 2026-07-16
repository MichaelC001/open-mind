"use client";

import { tokens } from "@openmind/ui";
import { useRouter } from "next/navigation";
import { useState, useTransition } from "react";

const { color, font } = tokens;

/**
 * Toggle whether a feed-sourced item is kept in the library independent of
 * its feed. PATCH `{kept}` through the same-origin proxy, then
 * `router.refresh()` to re-read server state — mirrors PinButton.
 */
export function KeepButton({ itemId, kept }: { itemId: string; kept: boolean }) {
  const router = useRouter();
  const [pending, startTransition] = useTransition();
  const [error, setError] = useState<string | null>(null);

  function toggle() {
    setError(null);
    startTransition(async () => {
      try {
        const res = await fetch(`/api/items/${itemId}`, {
          method: "PATCH",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({ kept: !kept }),
        });
        if (!res.ok) {
          setError("Could not update. Please try again.");
          return;
        }
        router.refresh();
      } catch {
        setError("Could not update. Please try again.");
      }
    });
  }

  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
      {error ? (
        <span aria-live="polite">
          <span style={{ fontFamily: font.mono, fontSize: "0.72rem", color: color.danger }}>
            {error}
          </span>
        </span>
      ) : null}
      <button
        type="button"
        onClick={toggle}
        disabled={pending}
        aria-pressed={kept}
        aria-label={kept ? "Unkeep this item" : "Keep this item"}
        style={{
          display: "inline-flex",
          alignItems: "center",
          gap: 6,
          fontFamily: font.mono,
          fontSize: "0.78rem",
          color: kept ? color.green : color.inkMuted,
          background: "none",
          border: "none",
          padding: 0,
          cursor: pending ? "default" : "pointer",
          opacity: pending ? 0.5 : 1,
        }}
      >
        <span aria-hidden style={{ fontSize: "0.85rem", lineHeight: 1 }}>
          {kept ? "◆" : "◇"}
        </span>
        {kept ? "Kept — unkeep" : "Keep"}
      </button>
    </span>
  );
}
