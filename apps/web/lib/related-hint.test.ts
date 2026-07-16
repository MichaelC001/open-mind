import { describe, expect, it } from "vitest";
import { CLOSE_MATCH_THRESHOLD, relatedHint } from "./related-hint";

describe("relatedHint", () => {
  it("treats distance below the threshold as a close match", () => {
    expect(relatedHint(0.24)).toBe("close match");
  });

  it("treats distance at the threshold boundary as a close match", () => {
    expect(relatedHint(0.25)).toBe("close match");
  });

  it("treats distance above the threshold as related", () => {
    expect(relatedHint(0.26)).toBe("related");
  });

  it("exports the threshold used by the boundary", () => {
    expect(CLOSE_MATCH_THRESHOLD).toBe(0.25);
  });
});
