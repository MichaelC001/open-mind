import { tokens } from "@openmind/ui";

const { color } = tokens;

// Shown in the main column while a page in this group resolves. Its job is to
// occupy the same shape the real page will — masthead band, then a card grid —
// so the swap doesn't shift anything, and to make a click register instantly
// instead of leaving the previous page frozen while the server round trip runs.
//
// Rendering this at all depends on Shell being a layout: as a per-page
// component the boundary would have taken the sidebar down with it.
function Line({ w, h = 12 }: { w: number | string; h?: number }) {
  return <div className="om-skel" style={{ width: w, height: h, borderRadius: 4 }} />;
}

export default function Loading() {
  return (
    <div aria-busy="true" aria-label="Loading">
      <div
        style={{
          padding: "18px 28px 16px",
          borderBottom: `1px solid ${color.hairline}`,
          background: color.header,
        }}
      >
        <Line w={210} h={26} />
        <div style={{ marginTop: 10 }}>
          <Line w={290} h={11} />
        </div>
      </div>

      <div style={{ position: "relative", flex: 1 }}>
        <div className="paper-texture" style={{ position: "absolute", inset: 0, pointerEvents: "none" }} />
        <div
          style={{
            position: "relative",
            padding: "22px 28px 40px",
            display: "grid",
            gridTemplateColumns: "repeat(auto-fill,minmax(240px,1fr))",
            gap: 18,
          }}
        >
          {Array.from({ length: 8 }, (_, i) => (
            <div
              key={i}
              style={{
                background: color.cardSurface,
                border: `1px solid ${color.hairline}`,
                borderRadius: 12,
                overflow: "hidden",
              }}
            >
              <div className="om-skel" style={{ aspectRatio: "16 / 9" }} />
              <div style={{ padding: "13px 14px 16px", display: "flex", flexDirection: "column", gap: 9 }}>
                <Line w="45%" h={9} />
                <Line w="92%" h={15} />
                <Line w="66%" h={15} />
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
