import { describe, expect, it } from "vitest";
import { findAnchor } from "./anchors";

const body = "One two three four five six seven eight nine ten.";

describe("findAnchor", () => {
  it("matches prefix+exact+suffix exactly", () => {
    expect(findAnchor(body, { exact: "three four", prefix: "two ", suffix: " five", offsetHint: 8 }))
      .toEqual({ start: 8, end: 18 });
  });
  it("falls back to exact-only when context drifted", () => {
    expect(findAnchor(body, { exact: "three four", prefix: "CHANGED ", suffix: " NOPE", offsetHint: 8 }))
      .toEqual({ start: 8, end: 18 });
  });
  it("returns null when the text vanished", () => {
    expect(findAnchor(body, { exact: "gone completely", prefix: "", suffix: "", offsetHint: 0 })).toBeNull();
  });
  it("picks the occurrence nearest the hint when exact repeats", () => {
    const b = "alpha X beta X gamma";
    expect(findAnchor(b, { exact: "X", prefix: "", suffix: "", offsetHint: 12 })).toEqual({ start: 13, end: 14 });
    expect(findAnchor(b, { exact: "X", prefix: "", suffix: "", offsetHint: 0 })).toEqual({ start: 6, end: 7 });
  });
});
