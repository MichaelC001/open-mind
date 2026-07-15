import { describe, expect, it } from "vitest";
import { confirmReduce, parseTags, type ConfirmState } from "./save-confirm";

describe("parseTags", () => {
  it("splits on comma and trims", () => {
    expect(parseTags(" a, B ,,c ")).toEqual(["a", "B", "c"]);
  });
  it("handles empty string", () => {
    expect(parseTags("")).toEqual([]);
  });
  it("handles only whitespace", () => {
    expect(parseTags("   ")).toEqual([]);
  });
  it("handles only commas", () => {
    expect(parseTags(",,,")).toEqual([]);
  });
  it("single tag", () => {
    expect(parseTags("hello")).toEqual(["hello"]);
  });
});

describe("confirmReduce", () => {
  it("starts hidden", () => {
    const state = { kind: "hidden" as const };
    expect(state.kind).toBe("hidden");
  });

  it("hidden → saved → confirming", () => {
    let state: ConfirmState = { kind: "hidden" };

    state = confirmReduce(state, { type: "saved", itemId: "item1", title: "Test Item" });
    expect(state).toEqual({ kind: "confirming", itemId: "item1", title: "Test Item", tags: "" });
  });

  it("confirming: type-tags updates tags", () => {
    let state: ConfirmState = { kind: "confirming", itemId: "item1", title: "Test", tags: "" };

    state = confirmReduce(state, { type: "type-tags", tags: "tag1, tag2" });
    expect(state).toEqual({ kind: "confirming", itemId: "item1", title: "Test", tags: "tag1, tag2" });
  });

  it("confirming: submit → saving-tags", () => {
    let state: ConfirmState = { kind: "confirming", itemId: "item1", title: "Test", tags: "tag1, tag2" };

    state = confirmReduce(state, { type: "submit" });
    expect(state).toEqual({ kind: "saving-tags", itemId: "item1", title: "Test", tags: "tag1, tag2" });
  });

  it("saving-tags: submit-ok → done", () => {
    let state: ConfirmState = { kind: "saving-tags", itemId: "item1", title: "Test", tags: "tag1, tag2" };

    state = confirmReduce(state, { type: "submit-ok" });
    expect(state).toEqual({ kind: "done" });
  });

  it("saving-tags: submit-failed → confirming (preserves tags)", () => {
    let state: ConfirmState = { kind: "saving-tags", itemId: "item1", title: "Test", tags: "tag1, tag2" };

    state = confirmReduce(state, { type: "submit-failed" });
    expect(state).toEqual({ kind: "confirming", itemId: "item1", title: "Test", tags: "tag1, tag2" });
  });

  it("confirming: dismiss → hidden", () => {
    let state: ConfirmState = { kind: "confirming", itemId: "item1", title: "Test", tags: "tag1" };

    state = confirmReduce(state, { type: "dismiss" });
    expect(state).toEqual({ kind: "hidden" });
  });

  it("confirming: idle-timeout → hidden", () => {
    let state: ConfirmState = { kind: "confirming", itemId: "item1", title: "Test", tags: "tag1" };

    state = confirmReduce(state, { type: "idle-timeout" });
    expect(state).toEqual({ kind: "hidden" });
  });

  it("saved while confirming replaces payload", () => {
    let state: ConfirmState = { kind: "confirming", itemId: "item1", title: "First", tags: "old-tags" };

    state = confirmReduce(state, { type: "saved", itemId: "item2", title: "Second" });
    expect(state).toEqual({ kind: "confirming", itemId: "item2", title: "Second", tags: "" });
  });

  it("done state ignores events", () => {
    let state: ConfirmState = { kind: "done" };

    const unchanged = confirmReduce(state, { type: "dismiss" });
    expect(unchanged).toEqual({ kind: "done" });
  });

  it("hidden state ignores non-saved events", () => {
    let state: ConfirmState = { kind: "hidden" };

    const unchanged = confirmReduce(state, { type: "submit" });
    expect(unchanged).toEqual({ kind: "hidden" });
  });
});
