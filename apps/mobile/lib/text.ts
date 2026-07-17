// Plain-text markdown stripping for previews that don't render markdown (card
// summaries, feed rows). Enrichment/feed summaries sometimes carry markdown
// syntax (from AI output or a feed's raw description) that would otherwise
// show up as literal asterisks/hashes in a plain <Text>.
export function stripMarkdown(text: string | undefined): string {
  if (!text) return "";
  return text
    .replace(/```[\s\S]*?```/g, (m) => m.replace(/```/g, ""))
    .replace(/`([^`]+)`/g, "$1")
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
    .replace(/^#{1,6}\s+/gm, "")
    .replace(/(\*\*\*|___)([^*_]+)\1/g, "$2")
    .replace(/(\*\*|__)([^*_]+)\1/g, "$2")
    .replace(/(?<![\w*])\*([^*\n]+)\*(?!\w)/g, "$1")
    .replace(/(?<![\w_])_([^_\n]+)_(?!\w)/g, "$1")
    .replace(/^>\s?/gm, "")
    .replace(/^[-*+]\s+/gm, "")
    .trim();
}
