"use client";

import { tokens } from "@openmind/ui";
import Link from "next/link";
import { useCallback, useEffect, useMemo, useState } from "react";
import { domainOf } from "../lib/cards";
import { relativeTime } from "../lib/relative-time";
import type { Feed, Item } from "../lib/types";

const { color, font } = tokens;

/** Keep/Kept toggle for a single feed-river row. PATCHes `{kept}` through the items proxy. */
function KeepToggle({ itemId, kept, onChange }: { itemId: string; kept: boolean; onChange: (kept: boolean) => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function toggle() {
    const next = !kept;
    setBusy(true);
    setError(null);
    try {
      const res = await fetch(`/api/items/${itemId}`, {
        method: "PATCH",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ kept: next }),
      });
      if (!res.ok) throw new Error(`kept update failed: ${res.status}`);
      onChange(next);
    } catch (err) {
      console.error("feed river keep toggle failed", { itemId, err });
      setError("Couldn't update. Try again.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
      {error ? (
        <span aria-live="polite" style={{ fontFamily: font.mono, fontSize: "0.68rem", color: color.danger }}>
          {error}
        </span>
      ) : null}
      <button
        type="button"
        onClick={toggle}
        disabled={busy}
        aria-pressed={kept}
        aria-label={kept ? "Unkeep this item" : "Keep this item"}
        style={{
          flex: "none",
          fontFamily: font.mono,
          fontSize: 11,
          letterSpacing: ".03em",
          color: kept ? color.green : color.inkFaintAlt,
          background: kept ? "color-mix(in srgb, " + color.green + " 12%, transparent)" : "transparent",
          border: `1px solid ${kept ? "color-mix(in srgb, " + color.green + " 34%, transparent)" : color.hairline}`,
          borderRadius: 999,
          padding: "5px 11px",
          cursor: busy ? "default" : "pointer",
          opacity: busy ? 0.6 : 1,
        }}
      >
        {kept ? "Kept" : "Keep"}
      </button>
    </span>
  );
}

function Row({ item, feedTitle, onKeptChange }: { item: Item; feedTitle: string; onKeptChange: (kept: boolean) => void }) {
  const domain = domainOf(item.url);
  return (
    <li
      style={{
        display: "flex",
        alignItems: "flex-start",
        gap: 16,
        padding: "16px 18px",
        background: color.cardSurface,
        border: `1px solid ${color.hairline}`,
        borderRadius: 12,
      }}
    >
      <div style={{ flex: 1, minWidth: 0 }}>
        <div className="meta" style={{ color: color.terracotta }}>
          {feedTitle}
        </div>
        <Link
          href={`/item/${item.id}`}
          className="serif"
          style={{
            display: "block",
            fontSize: 16,
            fontWeight: 600,
            color: color.ink,
            marginTop: 4,
            textDecoration: "none",
          }}
        >
          {item.title || domain || item.url}
        </Link>
        {item.summary ? (
          <p
            style={{
              fontFamily: font.sans,
              fontSize: 13,
              lineHeight: 1.5,
              color: color.inkMuted,
              margin: "6px 0 0",
              maxWidth: "72ch",
              overflow: "hidden",
              display: "-webkit-box",
              WebkitLineClamp: 2,
              WebkitBoxOrient: "vertical",
            }}
          >
            {item.summary}
          </p>
        ) : null}
        <div
          className="meta"
          style={{ marginTop: 8, textTransform: "none", letterSpacing: ".02em", color: color.inkFaintAlt }}
        >
          {relativeTime(item.createdAt)}
          {domain ? ` · ${domain}` : ""}
        </div>
      </div>
      <KeepToggle itemId={item.id} kept={!!item.keptAt} onChange={onKeptChange} />
    </li>
  );
}

/**
 * The Feed river: a reverse-chron list of everything your subscriptions
 * brought in. Fetches `/api/feed` (feed-originated items) and `/api/feeds`
 * (id→title map, doubling as per-feed filter chips). Empty and load-failure
 * states follow the house pattern used elsewhere (see RelatedRail).
 */
export function FeedRiver() {
  const [items, setItems] = useState<Item[] | null>(null);
  const [feeds, setFeeds] = useState<Feed[] | null>(null);
  const [activeFeedId, setActiveFeedId] = useState<string | undefined>(undefined);
  const [loadFailed, setLoadFailed] = useState(false);
  const [loadAttempt, setLoadAttempt] = useState(0);

  useEffect(() => {
    let cancelled = false;
    fetch("/api/feeds")
      .then(async (res) => {
        if (!res.ok) throw new Error(`failed to load feeds: ${res.status}`);
        return (await res.json()) as Feed[];
      })
      .then((data) => {
        if (!cancelled) setFeeds(data);
      })
      .catch((err) => {
        console.error("failed to load feeds for feed river", err);
        // Chips and titles degrade gracefully — the river itself still loads.
        if (!cancelled) setFeeds([]);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    setLoadFailed(false);
    const params = new URLSearchParams();
    if (activeFeedId) params.set("feedId", activeFeedId);
    const qs = params.toString();
    fetch(`/api/feed${qs ? `?${qs}` : ""}`)
      .then(async (res) => {
        if (!res.ok) throw new Error(`failed to load feed: ${res.status}`);
        return (await res.json()) as Item[];
      })
      .then((data) => {
        if (!cancelled) setItems(data);
      })
      .catch((err) => {
        console.error("failed to load feed river", err);
        if (!cancelled) setLoadFailed(true);
      });
    return () => {
      cancelled = true;
    };
  }, [activeFeedId, loadAttempt]);

  const titleFor = useCallback(
    (feedId?: string | null) => {
      if (!feedId || !feeds) return "Feed";
      return feeds.find((f) => f.id === feedId)?.title?.trim() || "Feed";
    },
    [feeds],
  );

  const setKept = useCallback((itemId: string, kept: boolean) => {
    setItems((prev) =>
      prev
        ? prev.map((it) => (it.id === itemId ? { ...it, keptAt: kept ? new Date().toISOString() : null } : it))
        : prev,
    );
  }, []);

  const chips = useMemo(() => feeds ?? [], [feeds]);

  if (loadFailed) {
    return (
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
          padding: "8px 14px",
          cursor: "pointer",
        }}
      >
        Couldn&apos;t load the feed — retry
      </button>
    );
  }

  if (items && items.length === 0 && !activeFeedId) {
    return (
      <div
        style={{
          maxWidth: 560,
          padding: "26px 22px",
          background: color.cardSurface,
          border: `1.5px dashed ${color.hairline}`,
          borderRadius: 12,
        }}
      >
        <div className="serif" style={{ fontSize: 18, fontWeight: 600, color: color.ink, marginBottom: 8 }}>
          Nothing here yet
        </div>
        <p style={{ fontFamily: font.sans, fontSize: 13.5, lineHeight: 1.55, color: color.inkMuted, margin: 0 }}>
          <Link href="/feeds" style={{ color: color.cobalt, textDecoration: "none" }}>
            Subscribe to something worth reading
          </Link>
          {" "}— new posts will show up here as they come in.
        </p>
      </div>
    );
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 18 }}>
      {chips.length > 0 ? (
        <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
          <button
            type="button"
            onClick={() => setActiveFeedId(undefined)}
            className="chip"
            aria-current={!activeFeedId ? "page" : undefined}
            style={
              !activeFeedId
                ? { background: color.ink, color: color.paper, borderColor: color.ink }
                : undefined
            }
          >
            All
          </button>
          {chips.map((feed) => {
            const isActive = feed.id === activeFeedId;
            return (
              <button
                key={feed.id}
                type="button"
                onClick={() => setActiveFeedId(feed.id)}
                className="chip"
                aria-current={isActive ? "page" : undefined}
                style={isActive ? { background: color.ink, color: color.paper, borderColor: color.ink } : undefined}
              >
                {feed.title?.trim() || domainOf(feed.siteUrl || feed.url) || "Feed"}
              </button>
            );
          })}
        </div>
      ) : null}

      {items === null ? (
        <p className="meta" style={{ textTransform: "none", letterSpacing: ".02em", color: color.inkFaintAlt }}>
          Loading…
        </p>
      ) : items.length === 0 ? (
        <p className="meta" style={{ textTransform: "none", letterSpacing: ".02em", color: color.inkFaintAlt }}>
          Nothing from this feed yet.
        </p>
      ) : (
        <ul style={{ listStyle: "none", margin: 0, padding: 0, display: "flex", flexDirection: "column", gap: 10 }}>
          {items.map((item) => (
            <Row
              key={item.id}
              item={item}
              feedTitle={titleFor(item.feedId)}
              onKeptChange={(kept) => setKept(item.id, kept)}
            />
          ))}
        </ul>
      )}
    </div>
  );
}
