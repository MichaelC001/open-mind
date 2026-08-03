import { describe, expect, it } from "vitest";
import { cardKind, fallbackTitle, isTextForward, typeGradient, typeLabel } from "./cards";

describe("repo card type", () => {
  it("normalises repo to itself", () => {
    expect(cardKind("repo")).toBe("repo");
  });
  it("labels it Repo", () => {
    expect(typeLabel.repo).toBe("Repo");
  });
  it("has a gradient", () => {
    expect(typeGradient.repo).toContain("linear-gradient");
  });
  it("still falls back to article for unknown types", () => {
    expect(cardKind("gizmo")).toBe("article");
  });
});

describe("fallbackTitle", () => {
  it("de-slugifies the last path segment", () => {
    expect(fallbackTitle("https://www.etsy.com/codeascraft/kafka-app-there-is-a-skill")).toBe(
      "kafka app there is a skill",
    );
  });
  it("ignores the query string that bot-protection redirects tack on", () => {
    expect(fallbackTitle("https://themodesse.com/products/loop-up-top?utm_source=facebook&fbclid=x")).toBe(
      "loop up top",
    );
  });
  it("ignores a trailing slash", () => {
    expect(fallbackTitle("https://example.com/some-post/")).toBe("some post");
  });
  it("drops a file extension", () => {
    expect(fallbackTitle("https://example.com/docs/getting-started.html")).toBe("getting started");
  });
  it("treats underscores as word breaks", () => {
    expect(fallbackTitle("https://example.com/a_b_c")).toBe("a b c");
  });
  it("falls back to the hostname when there is no path", () => {
    expect(fallbackTitle("https://www.etsy.com/")).toBe("etsy.com");
  });
  it("returns null for uploads, which have no meaningful url", () => {
    expect(fallbackTitle("/assets/abc123")).toBeNull();
  });
  it("returns null for an unparseable url", () => {
    expect(fallbackTitle("not a url")).toBeNull();
  });
  it("returns null when there is no url at all", () => {
    expect(fallbackTitle(undefined)).toBeNull();
  });
  it("returns null when a segment de-slugifies to nothing", () => {
    expect(fallbackTitle("https://example.com/---")).toBeNull();
  });
});

describe("isTextForward", () => {
  it("treats repo as text-forward (README-style body reads like an article)", () => {
    expect(isTextForward("repo")).toBe(true);
  });
  it("excludes non-text-forward kinds, e.g. image", () => {
    expect(isTextForward("image")).toBe(false);
  });
});
