// findAnchor locates a highlight's text-quote anchor in the article body.
// Strategy: prefix+exact+suffix as one string first (strongest signal); then
// exact alone, choosing the occurrence nearest offsetHint. Null means the
// text no longer exists in the body — the highlight simply isn't painted.
export function findAnchor(
  body: string,
  h: { exact: string; prefix: string; suffix: string; offsetHint: number },
): { start: number; end: number } | null {
  if (!h.exact) return null;

  const full = h.prefix + h.exact + h.suffix;
  const fullStart = nearestOccurrence(body, full, h.offsetHint - h.prefix.length);
  if (fullStart !== null) {
    const start = fullStart + h.prefix.length;
    return { start, end: start + h.exact.length };
  }

  const start = nearestOccurrence(body, h.exact, h.offsetHint);
  if (start === null) return null;
  return { start, end: start + h.exact.length };
}

/** Index of the occurrence of `needle` in `haystack` closest to `hint`, or null if none. */
function nearestOccurrence(haystack: string, needle: string, hint: number): number | null {
  let best: number | null = null;
  let from = 0;
  for (;;) {
    const idx = haystack.indexOf(needle, from);
    if (idx < 0) break;
    if (best === null || Math.abs(idx - hint) < Math.abs(best - hint)) best = idx;
    from = idx + 1;
  }
  return best;
}
