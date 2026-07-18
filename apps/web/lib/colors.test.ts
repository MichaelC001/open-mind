import { describe, expect, it } from "vitest";
import { colorSearchHref } from "./colors";

describe("colorSearchHref", () => {
  it("encodes a hex term with its leading #", () => {
    expect(colorSearchHref("#1B3FD1")).toBe("/?color=%231B3FD1");
  });
  it("passes a named colour through", () => {
    expect(colorSearchHref("cobalt")).toBe("/?color=cobalt");
  });
  it("encodes spaces in a named colour", () => {
    expect(colorSearchHref("dark blue")).toBe("/?color=dark%20blue");
  });
});
