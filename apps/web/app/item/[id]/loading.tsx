import { tokens } from "@openmind/ui";

const { color } = tokens;

// Covers /item/[id] and, as the parent boundary, /item/[id]/read too. Both are
// the same shape: one centred article card on the canvas, so a single skeleton
// serves both.
//
// This route is the destination of every grid card click — the most travelled
// navigation in the app — and until now it had no boundary at all, so a click
// left the grid frozen for the whole origin round trip. The item page is
// deliberately outside the (app) route group (no sidebar), which is exactly why
// app/(app)/loading.tsx never covered it.
function Block({ w, h, radius = 4 }: { w: number | string; h: number; radius?: number }) {
  return <div className="om-skel" style={{ width: w, height: h, borderRadius: radius }} />;
}

export default function Loading() {
  return (
    <main
      aria-busy="true"
      aria-label="Loading"
      style={{
        minHeight: "100vh",
        background: color.canvas,
        display: "flex",
        justifyContent: "center",
        alignItems: "flex-start",
        padding: "clamp(16px, 4vw, 44px) clamp(12px, 3vw, 24px)",
      }}
    >
      <article
        style={{
          width: 980,
          maxWidth: "100%",
          background: color.cardSurface,
          borderRadius: 14,
          border: `1px solid ${color.hairline}`,
          overflow: "hidden",
          boxShadow: "0 2px 6px rgba(28,26,22,.06), 0 30px 70px -42px rgba(28,26,22,.55)",
        }}
      >
        {/* The same terracotta hairline the real page wears, so the swap is silent. */}
        <div
          aria-hidden
          style={{
            height: 2,
            background: `linear-gradient(90deg, ${color.terracotta}, ${color.terracotta} 38%, transparent)`,
          }}
        />
        <header className="item-chrome">
          <Block w={104} h={11} />
          <Block w={132} h={11} />
        </header>

        <div style={{ padding: "clamp(18px, 3vw, 30px) clamp(18px, 4vw, 46px) 44px" }}>
          {/* Lead image well — 16/9, matching ReaderImage. */}
          <div className="om-skel" style={{ width: "100%", aspectRatio: "16 / 9", borderRadius: 11 }} />

          {/* Mono kicker, then the two-line serif title. */}
          <div style={{ marginTop: 22 }}>
            <Block w={260} h={10} />
          </div>
          <div style={{ marginTop: 14, display: "flex", flexDirection: "column", gap: 10 }}>
            <Block w="86%" h={28} radius={6} />
            <Block w="54%" h={28} radius={6} />
          </div>

          {/* Body lines. Uneven widths read as prose rather than a table. */}
          <div style={{ marginTop: 28, display: "flex", flexDirection: "column", gap: 11 }}>
            {["97%", "92%", "95%", "78%", "89%", "94%", "61%"].map((w, i) => (
              <Block key={i} w={w} h={13} />
            ))}
          </div>
        </div>
      </article>
    </main>
  );
}
