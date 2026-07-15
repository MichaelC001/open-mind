"use client";

import { tokens } from "@openmind/ui";
import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { findAnchor } from "../lib/anchors";
import type { CreateHighlightResponse, Highlight } from "../lib/types";

const { color, font } = tokens;

const CONTEXT_CHARS = 64;

/** Sum of text-node lengths under `root` up to (node, nodeOffset). */
function textOffsetWithin(root: HTMLElement, node: Node, nodeOffset: number): number {
  let offset = 0;
  let found = false;
  function walk(n: Node) {
    if (found) return;
    if (n === node) {
      offset += nodeOffset;
      found = true;
      return;
    }
    if (n.nodeType === Node.TEXT_NODE) {
      offset += (n.textContent ?? "").length;
    } else {
      n.childNodes.forEach(walk);
    }
  }
  walk(root);
  return offset;
}

/** Absolute offset into the full body string for a DOM (node, offset) pair. */
function absoluteOffset(node: Node, nodeOffset: number): number | null {
  const el = (node.nodeType === Node.ELEMENT_NODE ? (node as HTMLElement) : node.parentElement)?.closest<HTMLElement>(
    "[data-p-start]",
  );
  if (!el) return null;
  const pStart = Number(el.dataset.pStart);
  return pStart + textOffsetWithin(el, node, nodeOffset);
}

type PendingSelection = {
  top: number;
  left: number;
  start: number;
  end: number;
};

type Anchored = Highlight & { range: { start: number; end: number } };

/** Split into paragraphs the same way the reader page does, tracking each
 * paragraph's start offset in the original body string. */
function paragraphize(body: string): { text: string; start: number }[] {
  const out: { text: string; start: number }[] = [];
  let cursor = 0;
  for (const raw of body.split(/\n\n+/)) {
    const text = raw.trim();
    if (!text) continue;
    const idx = body.indexOf(text, cursor);
    const start = idx >= 0 ? idx : cursor;
    out.push({ text, start });
    cursor = start + text.length;
  }
  return out;
}

function renderParagraph(
  para: { text: string; start: number },
  anchored: Anchored[],
  onOpen: (quoteItemId: string) => void,
): ReactNode[] {
  const local = anchored
    .map((h) => ({
      start: Math.max(0, h.range.start - para.start),
      end: Math.min(para.text.length, h.range.end - para.start),
      quoteItemId: h.quoteItemId,
    }))
    .filter((h) => h.start < h.end)
    .sort((a, b) => a.start - b.start);

  if (local.length === 0) return [para.text];

  const nodes: ReactNode[] = [];
  let cursor = 0;
  local.forEach((h, i) => {
    if (h.start < cursor) return; // skip overlapping highlight, first one wins
    if (h.start > cursor) nodes.push(para.text.slice(cursor, h.start));
    nodes.push(
      <mark
        key={i}
        onClick={() => onOpen(h.quoteItemId)}
        style={{
          background: color.noteSurface,
          color: color.ink,
          cursor: "pointer",
          borderRadius: 2,
          padding: "0 1px",
        }}
      >
        {para.text.slice(h.start, h.end)}
      </mark>,
    );
    cursor = h.end;
  });
  if (cursor < para.text.length) nodes.push(para.text.slice(cursor));
  return nodes;
}

export function HighlightableBody({ body, itemId }: { body: string; itemId: string }) {
  const router = useRouter();
  const containerRef = useRef<HTMLDivElement>(null);
  const [highlights, setHighlights] = useState<Highlight[]>([]);
  const [pending, setPending] = useState<PendingSelection | null>(null);
  const [saving, setSaving] = useState(false);
  const [saveFailed, setSaveFailed] = useState(false);

  useEffect(() => {
    let cancelled = false;
    fetch(`/api/items/${itemId}/highlights`)
      .then((res) => (res.ok ? (res.json() as Promise<Highlight[]>) : []))
      .then((data) => {
        if (!cancelled) setHighlights(data);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [itemId]);

  const paragraphs = paragraphize(body);

  const anchored: Anchored[] = highlights
    .map((h) => {
      const range = findAnchor(body, {
        exact: h.exact,
        prefix: h.prefix,
        suffix: h.suffix,
        offsetHint: h.offsetHint,
      });
      return range ? { ...h, range } : null;
    })
    .filter((h): h is Anchored => h !== null);

  function handleMouseUp() {
    const sel = window.getSelection();
    if (!sel || sel.isCollapsed || sel.rangeCount === 0) {
      setPending(null);
      setSaveFailed(false);
      return;
    }
    const text = sel.toString();
    if (!text.trim() || !containerRef.current) {
      setPending(null);
      setSaveFailed(false);
      return;
    }
    const range = sel.getRangeAt(0);
    if (!containerRef.current.contains(range.commonAncestorContainer)) {
      setPending(null);
      setSaveFailed(false);
      return;
    }
    const start = absoluteOffset(range.startContainer, range.startOffset);
    const end = absoluteOffset(range.endContainer, range.endOffset);
    if (start == null || end == null || end <= start) {
      setPending(null);
      setSaveFailed(false);
      return;
    }
    const rect = range.getBoundingClientRect();
    const containerRect = containerRef.current.getBoundingClientRect();
    setSaveFailed(false);
    setPending({
      top: rect.top - containerRect.top - 36,
      left: rect.left - containerRect.left,
      start,
      end,
    });
  }

  async function saveHighlight() {
    if (!pending || saving) return;
    setSaving(true);
    setSaveFailed(false);
    const { start, end } = pending;
    const exact = body.slice(start, end);
    const prefix = body.slice(Math.max(0, start - CONTEXT_CHARS), start);
    const suffix = body.slice(end, end + CONTEXT_CHARS);
    try {
      const res = await fetch(`/api/items/${itemId}/highlights`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ exact, prefix, suffix, offsetHint: start }),
      });
      if (!res.ok) {
        throw new Error(`highlight save failed: ${res.status}`);
      }
      const data = (await res.json()) as CreateHighlightResponse;
      setHighlights((prev) => [...prev, data.highlight]);
      setPending(null);
      window.getSelection()?.removeAllRanges();
    } catch {
      setSaveFailed(true);
    } finally {
      setSaving(false);
    }
  }

  return (
    <div ref={containerRef} style={{ position: "relative" }} onMouseUp={handleMouseUp}>
      {pending ? (
        <button
          type="button"
          onClick={saveHighlight}
          disabled={saving}
          style={{
            position: "absolute",
            top: pending.top,
            left: pending.left,
            zIndex: 2,
            font: `500 11px/1 ${font.mono}`,
            letterSpacing: ".02em",
            color: saveFailed ? color.terracotta : color.ink,
            background: color.noteSurface,
            border: `1px solid ${saveFailed ? color.terracotta : color.hairline}`,
            borderRadius: 20,
            padding: "6px 12px",
            cursor: saving ? "default" : "pointer",
            opacity: saving ? 0.6 : 1,
            boxShadow: "0 8px 20px -8px rgba(0,0,0,.4)",
          }}
        >
          {saveFailed ? "Failed — retry" : "Highlight"}
        </button>
      ) : null}
      {paragraphs.map((para, i) => (
        <p
          key={i}
          data-p-start={para.start}
          className="serif"
          style={{
            fontSize: 19,
            lineHeight: 1.85,
            color: color.ink,
            margin: "0 0 1.5rem",
            whiteSpace: "pre-wrap",
          }}
        >
          {renderParagraph(para, anchored, (quoteItemId) => router.push(`/item/${quoteItemId}`))}
        </p>
      ))}
    </div>
  );
}
