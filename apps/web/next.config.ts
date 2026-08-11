import type { NextConfig } from "next";
import path from "node:path";

const nextConfig: NextConfig = {
  output: "standalone",
  // Monorepo: trace files from the repo root so workspace packages
  // (@openmind/ui, @openmind/api-client) are bundled into the standalone output.
  outputFileTracingRoot: path.join(__dirname, "../.."),
  transpilePackages: ["@openmind/ui", "@openmind/api-client"],
  experimental: {
    // Next 15 defaults the client router cache to dynamic:0, which throws away
    // a prefetched payload the instant it lands — every route here is dynamic
    // (apiFetch reads cookies), so prefetching was being wasted and back/forward
    // re-hit the origin. 30s makes a prefetch actually reusable without letting
    // a stale library sit on screen. `static` is the window that prefetch={true}
    // links use; 300 is the default, pinned here so the pair reads together.
    staleTimes: { dynamic: 30, static: 300 },
  },
};

export default nextConfig;
