import type { CSSProperties } from "react";
import Link from "next/link";
import { colorSearchHref } from "../lib/colors";

/**
 * Palette renders a row of colour dots — the signature brand detail. Each hex in
 * `colors` becomes one `.dot` span (styled in globals.css). Pass `size` to scale
 * the dots for detail-page swatches. Pass `colorLinks` to make each dot a colour
 * search link (used on cards + the detail rail so the dots teach colour search).
 */
export function Palette({
  colors,
  size,
  colorLinks,
}: {
  colors: string[];
  size?: number;
  colorLinks?: boolean;
}) {
  const style = (c: string): CSSProperties =>
    size !== undefined
      ? { background: c, width: size, height: size, borderRadius: size / 2 }
      : { background: c };
  if (colorLinks) {
    return (
      <>
        {colors.map((c, i) => (
          <Link
            key={`${c}-${i}`}
            href={colorSearchHref(c)}
            className="dot dot-link"
            style={style(c)}
            title="Find saves this colour"
            aria-label={`Find saves matching ${c}`}
          />
        ))}
      </>
    );
  }
  return (
    <>
      {colors.map((c, i) => (
        <span key={`${c}-${i}`} className="dot" style={style(c)} />
      ))}
    </>
  );
}
