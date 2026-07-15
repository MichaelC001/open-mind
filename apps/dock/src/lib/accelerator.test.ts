import { describe, expect, it } from "vitest";
import { captureToAccelerator, normaliseDisplay, DEFAULT_QUICK_SAVE, DEFAULT_QUICK_FIND } from "./accelerator";

const base = { key: "s", code: "KeyS", metaKey: false, ctrlKey: false, altKey: false, shiftKey: false };

describe("captureToAccelerator", () => {
  it("meta+shift+letter", () => {
    expect(captureToAccelerator({ ...base, metaKey: true, shiftKey: true })).toBe("CmdOrCtrl+Shift+S");
  });
  it("ctrl+alt+digit", () => {
    expect(captureToAccelerator({ ...base, key: "1", code: "Digit1", ctrlKey: true, altKey: true })).toBe("CmdOrCtrl+Alt+1");
  });
  it("f-key with modifier", () => {
    expect(captureToAccelerator({ ...base, key: "F5", code: "F5", altKey: true })).toBe("Alt+F5");
  });
  it("rejects modifier-only", () => {
    expect(captureToAccelerator({ ...base, key: "Shift", code: "ShiftLeft", shiftKey: true })).toBeNull();
  });
  it("rejects bare key", () => {
    expect(captureToAccelerator(base)).toBeNull();
  });
  it("default constant sanity", () => {
    expect(DEFAULT_QUICK_SAVE).toBe("CmdOrCtrl+Shift+S");
  });
  it("space maps to Space token", () => {
    expect(captureToAccelerator({ ...base, key: " ", code: "Space", metaKey: true })).toBe("CmdOrCtrl+Space");
  });
  it("quick find constant", () => {
    expect(DEFAULT_QUICK_FIND).toBe("CmdOrCtrl+Shift+O");
  });
});

describe("normaliseDisplay", () => {
  it("maps macOS super display form to CmdOrCtrl", () => {
    expect(normaliseDisplay("shift+super+KeyS")).toBe("CmdOrCtrl+Shift+S");
  });
  it("maps a bare ctrl display form to CmdOrCtrl", () => {
    expect(normaliseDisplay("ctrl+shift+KeyO")).toBe("CmdOrCtrl+Shift+O");
  });
  it("maps digit and F-key tokens", () => {
    expect(normaliseDisplay("ctrl+alt+Digit1")).toBe("CmdOrCtrl+Alt+1");
    expect(normaliseDisplay("alt+F5")).toBe("Alt+F5");
  });
  it("maps Space and passes unknown tokens through unchanged", () => {
    expect(normaliseDisplay("super+Space")).toBe("CmdOrCtrl+Space");
    expect(normaliseDisplay("weirdmod+KeyS")).toBe("weirdmod+S");
  });
});
