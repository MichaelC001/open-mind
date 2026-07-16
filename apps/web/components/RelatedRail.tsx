"use client";

import { tokens } from "@openmind/ui";
import { useRouter } from "next/navigation";
import { useEffect, useState, type CSSProperties, type ReactNode } from "react";
import { cardKind, domainOf, typeLabel } from "../lib/cards";
import type { RelatedItem } from "../lib/types";

const { color, font } = tokens;

const CLOSE_MATCH_THRESHOLD = 0.25;

const rowStyle: CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: 8,
  padding: "7px 0",
};

const rowTextBtn: CSSProperties = {
  flex: 1,
  minWidth: 0,
  display: "flex",
  flexDirection: "column",
  gap: 2,
  background: "none",
  border: "none",
  padding: 0,
  textAlign: "left",
  cursor: "pointer",
};

const rowTitle: CSSProperties = {
  fontFamily: font.sans,
  fontSize: 13,
  color: color.ink,
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
};

const addToggle: CSSProperties = {
  font: `500 10px/1 ${font.mono}`,
  letterSpacing: ".02em",
  color: color.cobalt,
  background: "none",
  border: "none",
  padding: 0,
  cursor: "pointer",
  flexShrink: 0,
};

/** Best label for a related item: title, else the domain, else the raw URL/note snippet. */
function labelFor(item: RelatedItem["item"]): string {
  return item.title || domainOf(item.url) || item.url;
}

/** Divider + content collapse together — nothing renders when the rail is empty. */
function Section({ children }: { children: ReactNode }) {
  return (
    <>
      <div style={{ height: 1, background: color.hairline, margin: "18px 0" }} />
      {children}
    </>
  );
}

export function RelatedRail({ itemId, onLinked }: { itemId: string; onLinked?: () => void }) {
  const router = useRouter();
  const [related, setRelated] = useState<RelatedItem[] | null>(null);
  const [loadFailed, setLoadFailed] = useState(false);
  const [loadAttempt, setLoadAttempt] = useState(0);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoadFailed(false);
    fetch(`/api/items/${itemId}/related`)
      .then(async (res) => {
        if (!res.ok) throw new Error(`failed to load related items: ${res.status}`);
        return (await res.json()) as RelatedItem[];
      })
      .then((data) => {
        if (!cancelled) setRelated(data);
      })
      .catch((err) => {
        console.error("failed to load related items", { itemId, err });
        if (!cancelled) setLoadFailed(true);
      });
    return () => {
      cancelled = true;
    };
  }, [itemId, loadAttempt]);

  async function linkRow(toId: string) {
    if (!related) return;
    const prev = related;
    setError(null);
    setRelated(related.filter((r) => r.item.id !== toId));
    try {
      const res = await fetch(`/api/items/${itemId}/links`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ toId }),
      });
      if (!res.ok) throw new Error(`link failed: ${res.status}`);
      onLinked?.();
    } catch (err) {
      console.error("related item link failed", { itemId, toId, err });
      setRelated(prev);
      setError("Could not add link. Please try again.");
    }
  }

  if (loadFailed) {
    return (
      <Section>
        <button
          type="button"
          onClick={() => setLoadAttempt((n) => n + 1)}
          style={{
            display: "block",
            font: `500 11px/1 ${font.mono}`,
            letterSpacing: ".02em",
            color: color.inkFaint,
            background: "none",
            border: `1px solid ${color.hairline}`,
            borderRadius: 20,
            padding: "6px 12px",
            cursor: "pointer",
          }}
        >
          Couldn&apos;t load related items — retry
        </button>
      </Section>
    );
  }

  if (!related || related.length === 0) return null;

  return (
    <Section>
      <div className="meta" style={{ color: color.inkFaintAlt }}>
        Related
      </div>
      <div style={{ marginTop: 6 }}>
        {related.map((row) => {
          const hint = row.distance <= CLOSE_MATCH_THRESHOLD ? "close match" : "related";
          return (
            <div key={row.item.id} style={rowStyle}>
              <button
                type="button"
                style={rowTextBtn}
                onClick={() => router.push(`/item/${row.item.id}`)}
              >
                <span style={rowTitle}>{labelFor(row.item)}</span>
                <span className="meta">
                  {typeLabel[cardKind(row.item.cardType)]} · {hint}
                </span>
              </button>
              <button type="button" onClick={() => linkRow(row.item.id)} style={addToggle}>
                + Link
              </button>
            </div>
          );
        })}
      </div>
      {error ? (
        <p
          aria-live="polite"
          style={{ fontFamily: font.mono, fontSize: "0.72rem", color: color.danger, margin: "8px 0 0" }}
        >
          {error}
        </p>
      ) : null}
    </Section>
  );
}
