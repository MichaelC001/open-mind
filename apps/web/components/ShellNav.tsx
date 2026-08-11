"use client";
// The sidebar's navigation. Split out of Shell as a client component for one
// reason: Shell now lives in the (app) layout, which React keeps mounted across
// navigations, so it cannot receive per-page `active` props any more. Active
// state is derived from the pathname instead, which is also how the drawer
// already knew when to close.
//
// Primary destinations are prefetched with prefetch={true} (a FULL prefetch).
// These routes are all dynamic — they read cookies through apiFetch — and for a
// dynamic route the default "auto" prefetch only warms the shell, so the click
// still paid a full origin round trip. A full prefetch fetches the page data
// too and is reusable for staleTimes.static (5 min), which is what turns a
// sidebar click from "wait for the server" into an instant swap.
import Link from "next/link";
import { usePathname } from "next/navigation";
import { tokens } from "@openmind/ui";
import { lensDot } from "../lib/lens-format";
import type { Lens } from "../lib/types";

const navBase = {
  display: "flex",
  alignItems: "center",
  gap: 10,
  padding: "7px 10px",
  borderRadius: 8,
  fontFamily: tokens.font.sans,
  fontSize: 13,
  fontWeight: 500,
  lineHeight: 1,
  textDecoration: "none",
} as const;

const PRIMARY = [
  { href: "/", glyph: "◧", label: "The Mind" },
  { href: "/feed", glyph: "≋", label: "Feed" },
  { href: "/desk", glyph: "◵", label: "Desk" },
  { href: "/drift", glyph: "❍", label: "Drift" },
  { href: "/places", glyph: "⌖", label: "Places" },
] as const;

function tone(active: boolean) {
  return {
    background: active ? "rgba(27,63,209,.1)" : "transparent",
    color: active ? tokens.color.cobalt : tokens.color.ink,
  };
}

export function ShellNav({ lenses }: { lenses: Lens[] }) {
  const pathname = usePathname();

  return (
    <>
      {PRIMARY.map(({ href, glyph, label }) => (
        <Link key={href} href={href} prefetch style={{ ...navBase, ...tone(pathname === href) }}>
          <span style={{ fontSize: 15, width: 16 }}>{glyph}</span> {label}
        </Link>
      ))}

      <div style={{ height: 1, background: "rgba(28,26,22,.09)", margin: "16px 8px" }} />

      <div className="meta" style={{ display: "flex", alignItems: "center", padding: "2px 10px 8px" }}>
        Lenses
      </div>
      {/* Lenses are left on the default prefetch: the list is unbounded, and
          fully prefetching every one of them would put a request per lens on
          the wire on every page load. */}
      {lenses.map((lens) => (
        <Link
          key={lens.id}
          href={`/lens/${lens.id}`}
          title={lens.name}
          style={{ ...navBase, ...tone(pathname === `/lens/${lens.id}`) }}
        >
          <span className="dot" style={{ background: lensDot(lens.rule, tokens.color.cobalt) }} />
          <span style={{ whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>
            {lens.name}
          </span>
        </Link>
      ))}
      <Link href="/lens/new" style={{ ...navBase, color: tokens.color.inkMuted }}>
        <span style={{ fontSize: 15, width: 16 }}>+</span> New lens
      </Link>
    </>
  );
}
