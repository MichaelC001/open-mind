"use client";

import { useEffect, useState } from "react";
import { tokens } from "@openmind/ui";

const KEY = "openmind.colourHintDismissed";

/**
 * One-time inline hint teaching colour search. Renders nothing on the server
 * and until mount (avoids a flash for returning users), then shows only when
 * the localStorage flag is absent. `dismissedBySearch` is passed true when a
 * colour search is already active, which also permanently dismisses it.
 */
export function ColourHint({ dismissedBySearch }: { dismissedBySearch: boolean }) {
  const [show, setShow] = useState(false);

  useEffect(() => {
    if (dismissedBySearch) {
      localStorage.setItem(KEY, "1");
      return;
    }
    if (localStorage.getItem(KEY) !== "1") setShow(true);
  }, [dismissedBySearch]);

  if (!show) return null;

  const dismiss = () => {
    localStorage.setItem(KEY, "1");
    setShow(false);
  };

  return (
    <span
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 8,
        fontFamily: tokens.font.sans,
        fontSize: 12,
        color: tokens.color.inkMuted,
      }}
    >
      New — every save keeps its colours. Tap one to find things that match.
      <button
        type="button"
        onClick={dismiss}
        aria-label="Dismiss colour hint"
        style={{
          border: "none",
          background: "transparent",
          color: tokens.color.inkFaint,
          cursor: "pointer",
          fontSize: 14,
          lineHeight: 1,
          padding: 0,
        }}
      >
        ×
      </button>
    </span>
  );
}
