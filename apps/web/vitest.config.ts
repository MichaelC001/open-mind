import { defineConfig } from "vitest/config";

// Minimal config mirroring apps/dock's vitest setup — this app is Next.js, so
// no bundler plugin is needed for the pure-TS unit tests under lib/.
export default defineConfig({
  test: {
    environment: "node",
    include: ["lib/**/*.test.ts"],
  },
});
