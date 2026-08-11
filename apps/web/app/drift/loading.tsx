import { tokens } from "@openmind/ui";

const { color } = tokens;

// Drift is the one full-screen, immersive route and is deliberately outside the
// (app) group (no sidebar), so app/(app)/loading.tsx never covered it. It
// server-fetches its whole batch before DriftFlow can render anything, which
// made it one of the longest waits in the app with nothing on screen.
//
// Kept deliberately quiet — a single centred card standing in for the one card
// Drift shows at a time. Drift's own entrance animation does the rest.
export default function Loading() {
  return (
    <main
      aria-busy="true"
      aria-label="Loading"
      style={{
        minHeight: "100vh",
        background: color.canvas,
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        gap: 22,
        padding: "clamp(16px, 4vw, 44px)",
      }}
    >
      <div
        className="om-skel"
        style={{ width: "min(420px, 88vw)", aspectRatio: "3 / 4", borderRadius: 14 }}
      />
      <div className="om-skel" style={{ width: 132, height: 10, borderRadius: 4 }} />
    </main>
  );
}
