import type { ReactNode } from "react";
import Link from "next/link";
import { tokens } from "@openmind/ui";
import { getLenses } from "../lib/lenses";
import { formatBytes, getMe } from "../lib/me";
import { MobileNav } from "./MobileNav";
import { lensDot } from "../lib/lens-format";

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
} as const;

const softDivider = { height: 1, background: "rgba(28,26,22,.09)" } as const;

export async function Shell({
  children,
  activeLensId,
  activeDesk,
  activeDrift,
  activeFeed,
}: {
  children: ReactNode;
  activeLensId?: string;
  activeDesk?: boolean;
  activeDrift?: boolean;
  activeFeed?: boolean;
}) {
  const [lenses, me] = await Promise.all([getLenses(), getMe()]);
  const mindActive = !activeLensId && !activeDesk && !activeDrift && !activeFeed;
  // Self-hosted accounts may have no e-mail; fall back to a neutral label.
  const accountName = me?.email ? me.email.split("@")[0] : "You";
  const accountInitial = accountName[0].toUpperCase();
  // Rendered twice: in the desktop aside and inside the mobile drawer.
  const sidebar = (
    <>
        {/* Wordmark + 3-line cobalt logo mark — links home (The Mind) */}
        <Link
          href="/"
          style={{
            display: "flex",
            alignItems: "center",
            gap: 9,
            padding: "0 6px 24px",
            textDecoration: "none",
            color: "inherit",
          }}
        >
          <div
            style={{
              width: 24,
              height: 24,
              borderRadius: 6,
              background: tokens.color.cobalt,
              position: "relative",
              flex: "none",
            }}
          >
            <div
              style={{
                position: "absolute",
                inset: "6px 6px auto 6px",
                height: 2,
                background: tokens.color.paper,
              }}
            />
            <div
              style={{
                position: "absolute",
                inset: "11px 6px auto 6px",
                height: 2,
                background: "rgba(244,240,230,.6)",
              }}
            />
            <div
              style={{
                position: "absolute",
                inset: "16px 9px auto 6px",
                height: 2,
                background: "rgba(244,240,230,.4)",
              }}
            />
          </div>
          <span
            className="serif"
            style={{ fontSize: 20, fontWeight: 600, letterSpacing: "-.01em" }}
          >
            Openmind
          </span>
        </Link>

        {/* The Mind — the home library. Active unless viewing a lens, feed, drift or the desk. */}
        <Link
          href="/"
          style={{
            ...navBase,
            textDecoration: "none",
            background: mindActive ? "rgba(27,63,209,.1)" : "transparent",
            color: mindActive ? tokens.color.cobalt : tokens.color.ink,
          }}
        >
          <span style={{ fontSize: 15, width: 16 }}>◧</span> The Mind
        </Link>

        {/* Feed — the reverse-chron river of everything your subscriptions brought in. */}
        <Link
          href="/feed"
          style={{
            ...navBase,
            textDecoration: "none",
            background: activeFeed ? "rgba(27,63,209,.1)" : "transparent",
            color: activeFeed ? tokens.color.cobalt : tokens.color.ink,
          }}
        >
          <span style={{ fontSize: 15, width: 16 }}>≋</span> Feed
        </Link>

        {/* Desk — the pinboard of what you're working with. */}
        <Link
          href="/desk"
          style={{
            ...navBase,
            textDecoration: "none",
            background: activeDesk ? "rgba(27,63,209,.1)" : "transparent",
            color: activeDesk ? tokens.color.cobalt : tokens.color.ink,
          }}
        >
          <span style={{ fontSize: 15, width: 16 }}>◵</span> Desk
        </Link>

        {/* Drift — calm, finite resurfacing of forgotten saves. */}
        <Link
          href="/drift"
          style={{
            ...navBase,
            textDecoration: "none",
            background: activeDrift ? "rgba(27,63,209,.1)" : "transparent",
            color: activeDrift ? tokens.color.cobalt : tokens.color.ink,
          }}
        >
          <span style={{ fontSize: 15, width: 16 }}>❍</span> Drift
        </Link>

        <div style={{ ...softDivider, margin: "16px 8px" }} />

        <div
          className="meta"
          style={{ display: "flex", alignItems: "center", padding: "2px 10px 8px" }}
        >
          Lenses
        </div>
        {lenses.map((lens) => {
          const active = lens.id === activeLensId;
          return (
            <Link
              key={lens.id}
              href={`/lens/${lens.id}`}
              title={lens.name}
              style={{
                ...navBase,
                textDecoration: "none",
                background: active ? "rgba(27,63,209,.1)" : "transparent",
                color: active ? tokens.color.cobalt : tokens.color.ink,
              }}
            >
              <span className="dot" style={{ background: lensDot(lens.rule, tokens.color.cobalt) }} />
              <span
                style={{ whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}
              >
                {lens.name}
              </span>
            </Link>
          );
        })}
        <Link href="/lens/new" style={{ ...navBase, textDecoration: "none", color: tokens.color.inkMuted }}>
          <span style={{ fontSize: 15, width: 16 }}>+</span> New lens
        </Link>

        {/* Account row */}
        <div
          style={{
            marginTop: "auto",
            display: "flex",
            alignItems: "center",
            gap: 10,
            padding: 8,
            borderRadius: 11,
            border: `1px solid ${tokens.color.hairline}`,
            background: tokens.color.paper,
          }}
        >
          <div
            style={{
              width: 30,
              height: 30,
              borderRadius: "50%",
              background: `linear-gradient(135deg,${tokens.color.cobalt},${tokens.color.green})`,
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              color: tokens.color.paper,
              fontFamily: tokens.font.sans,
              fontSize: 13,
              fontWeight: 600,
              lineHeight: 1,
              flex: "none",
            }}
          >
            {accountInitial}
          </div>
          <div style={{ minWidth: 0, flex: 1 }}>
            <div
              style={{
                fontFamily: tokens.font.sans,
                fontSize: 12.5,
                fontWeight: 600,
                lineHeight: 1.1,
                whiteSpace: "nowrap",
                overflow: "hidden",
                textOverflow: "ellipsis",
              }}
            >
              {accountName}
            </div>
            <div
              className="meta"
              style={{
                textTransform: "none",
                letterSpacing: ".02em",
                color: tokens.color.inkFaintAlt,
                marginTop: 3,
              }}
            >
              {me?.email || "Owner · signed in"}
            </div>
          </div>
        </div>

        {/* Library stats — self-hosting has no quota, so counts, not a meter */}
        {me && (
          <div
            style={{
              padding: "14px 10px 2px",
              marginTop: 12,
              borderTop: "1px solid rgba(28,26,22,.09)",
            }}
          >
            <div className="meta" style={{ marginBottom: 7 }}>
              Local · self-hosted
            </div>
            <div
              className="meta"
              style={{
                textTransform: "none",
                letterSpacing: ".02em",
                color: tokens.color.inkFaintAlt,
              }}
            >
              {me.itemCount === 1 ? "1 item" : `${me.itemCount} items`} ·{" "}
              {formatBytes(me.storageBytes)} archived
            </div>
          </div>
        )}
    </>
  );

  return (
    <div className="shell">
      {/* Mobile-only sticky bar + drawer; display:none on desktop. */}
      <MobileNav sidebar={sidebar} />
      <aside className="shell-sidebar">{sidebar}</aside>
      {/* Fluid main column */}
      <div className="shell-main">{children}</div>
    </div>
  );
}
