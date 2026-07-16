/** Distance at or below this threshold is treated as a "close match" rather than merely "related". */
export const CLOSE_MATCH_THRESHOLD = 0.25;

/** Label a related-item hint from its vector distance. Boundary (== threshold) counts as a close match. */
export function relatedHint(distance: number): "close match" | "related" {
  return distance <= CLOSE_MATCH_THRESHOLD ? "close match" : "related";
}
