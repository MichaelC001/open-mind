import type { ReactNode } from "react";
import { tokens } from "@openmind/ui";
import { getLenses } from "../lib/lenses";
import { formatBytes, getAccount } from "../lib/account";
import { authMode } from "../lib/auth-mode";
import { ClerkAccountRow } from "./ClerkAccountRow";
import { TokenAccountRow } from "./TokenAccountRow";
import { ShellDrawer } from "./ShellDrawer";
import { ShellNav } from "./ShellNav";

// Rendered by app/(app)/layout.tsx, not by individual pages. That placement is
// the point: React keeps a layout mounted across navigations within the group,
// so the sidebar (and the /lenses + /account fetches behind it) is built once
// per session rather than re-rendered and re-sent inside every page's payload.
// Active-nav state moved to ShellNav, which reads it from the pathname.
export async function Shell({ children }: { children: ReactNode }) {
  // Both are independent and neither throws, so fetch them concurrently rather
  // than making the sidebar wait on two sequential round trips.
  const [lenses, account] = await Promise.all([getLenses(), getAccount()]);
  return (
    <div style={{ display: "flex", height: "100vh", overflow: "hidden" }}>
      <ShellDrawer>
      <aside
        style={{
          width: 230,
          flex: "none",
          borderRight: `1px solid ${tokens.color.hairline}`,
          background: tokens.color.panel,
          display: "flex",
          flexDirection: "column",
          padding: "22px 16px",
        }}
      >
        {/* Wordmark + 3-line cobalt logo mark */}
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 9,
            padding: "0 6px 24px",
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
        </div>

        <ShellNav lenses={lenses} />

        {/* Account row. Clerk mode delegates the avatar and every account
            action to Clerk's UserButton; token mode has no identity provider,
            so it shows a neutral label plus a cookie sign-out. Neither invents
            a name — the old hardcoded one shipped to every self-hoster. */}
        {authMode === "clerk" ? <ClerkAccountRow /> : <TokenAccountRow />}

        {/* Real usage, not a meter. A progress bar needs a denominator, and
            self-hosting has no storage quota — the old fixed 34% bar against a
            made-up 9 GB ceiling was showing every self-hoster invented numbers.
            Counts come from GET /account; omitted entirely if it's unreachable
            rather than falling back to placeholders. */}
        {account && (
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
              {account.itemCount.toLocaleString("en-GB")}
              {account.itemCount === 1 ? " item" : " items"}
              {account.assetBytes > 0 && ` · ${formatBytes(account.assetBytes)}`}
            </div>
          </div>
        )}
      </aside>
      </ShellDrawer>

      {/* Fluid main column */}
      <div
        style={{
          flex: 1,
          display: "flex",
          flexDirection: "column",
          minWidth: 0,
          overflow: "auto",
          background: tokens.color.paper,
        }}
      >
        {children}
      </div>
    </div>
  );
}
