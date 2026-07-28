import { describe, expect, it } from "vitest";
import { diffNotifySettings, type NotifyFormValues } from "./notify-settings-diff";

const base: NotifyFormValues = {
  digest: "push",
  feedRiver: "off",
  lifecycle: "push",
  quietHours: "",
  timezone: "Europe/London",
  dailyCap: 10,
};

describe("diffNotifySettings", () => {
  it("returns an empty patch when nothing changed", () => {
    expect(diffNotifySettings(base, { ...base })).toEqual({});
  });

  it("includes only the field that changed", () => {
    expect(diffNotifySettings(base, { ...base, digest: "both" })).toEqual({ notifyDigest: "both" });
  });

  it("includes quiet hours when it's newly set", () => {
    expect(diffNotifySettings(base, { ...base, quietHours: "22:00-07:00" })).toEqual({
      notifyQuietHours: "22:00-07:00",
    });
  });

  it("includes quiet hours as an explicit empty string when cleared", () => {
    const loaded: NotifyFormValues = { ...base, quietHours: "22:00-07:00" };
    const patch = diffNotifySettings(loaded, { ...loaded, quietHours: "" });
    expect(patch).toEqual({ notifyQuietHours: "" });
  });

  it("omits quiet hours when it stayed empty", () => {
    expect(diffNotifySettings(base, { ...base })).not.toHaveProperty("notifyQuietHours");
  });

  it("includes timezone only when it changed", () => {
    expect(diffNotifySettings(base, { ...base, timezone: "America/New_York" })).toEqual({
      notifyTimezone: "America/New_York",
    });
  });

  it("includes the daily cap only when it changed", () => {
    expect(diffNotifySettings(base, { ...base, dailyCap: 25 })).toEqual({ notifyDailyCap: 25 });
  });

  it("omits the daily cap when every other field changed but it didn't", () => {
    const patch = diffNotifySettings(base, { ...base, digest: "off", timezone: "UTC" });
    expect(patch).not.toHaveProperty("notifyDailyCap");
  });

  it("includes every field that changed and nothing else", () => {
    const current: NotifyFormValues = {
      digest: "both",
      feedRiver: "off",
      lifecycle: "email",
      quietHours: "06:00-08:00",
      timezone: "Europe/London",
      dailyCap: 50,
    };
    expect(diffNotifySettings(base, current)).toEqual({
      notifyDigest: "both",
      notifyLifecycle: "email",
      notifyQuietHours: "06:00-08:00",
      notifyDailyCap: 50,
    });
  });
});
