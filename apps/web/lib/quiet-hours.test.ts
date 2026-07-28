import { describe, expect, it } from "vitest";
import { composeQuietHours, parseQuietHours } from "./quiet-hours";

describe("composeQuietHours", () => {
  it("joins two times into an HH:MM-HH:MM range", () => {
    expect(composeQuietHours("22:00", "07:00")).toBe("22:00-07:00");
  });

  it("clears to empty when the start is blank", () => {
    expect(composeQuietHours("", "07:00")).toBe("");
  });

  it("clears to empty when the end is blank", () => {
    expect(composeQuietHours("22:00", "")).toBe("");
  });

  it("clears to empty when both are blank", () => {
    expect(composeQuietHours("", "")).toBe("");
  });
});

describe("parseQuietHours", () => {
  it("splits a valid range into start and end", () => {
    expect(parseQuietHours("22:00-07:00")).toEqual({ start: "22:00", end: "07:00" });
  });

  it("returns blank fields for an empty string", () => {
    expect(parseQuietHours("")).toEqual({ start: "", end: "" });
  });

  it("returns blank fields for a malformed range", () => {
    expect(parseQuietHours("not-a-range")).toEqual({ start: "", end: "" });
  });

  it("returns blank fields for an out-of-bounds hour", () => {
    expect(parseQuietHours("25:00-07:00")).toEqual({ start: "", end: "" });
  });

  it("round-trips through compose and parse", () => {
    const composed = composeQuietHours("06:30", "14:45");
    expect(parseQuietHours(composed)).toEqual({ start: "06:30", end: "14:45" });
  });
});
