// routeForNotificationData is a pure function and touches neither module, but
// importing "../notifications" still pulls in expo-notifications at the top
// level. Its real implementation registers a native push-token listener as an
// import-time side effect, which logs asynchronously after this file's tests
// complete and fails the whole run ("Cannot log after tests are done") when
// several suites share a worker — so it's stubbed out rather than loaded for
// real, same as expo-file-system is stubbed in shared-pending.test.ts.
jest.mock("expo-notifications", () => ({
  AndroidImportance: { DEFAULT: 3 },
  setNotificationChannelAsync: jest.fn(),
  getPermissionsAsync: jest.fn(),
  requestPermissionsAsync: jest.fn(),
  getExpoPushTokenAsync: jest.fn(),
  setNotificationHandler: jest.fn(),
}));
jest.mock("expo-constants", () => ({
  __esModule: true,
  default: { expoConfig: { extra: { eas: { projectId: "test-project-id" } } } },
}));

import * as Notifications from "expo-notifications";
import { routeForNotificationData } from "../notifications";

// Regression test for the whole-branch review's I2 finding: with no handler
// installed, expo-notifications silently suppresses any notification that
// arrives while the app is in the foreground — which is essentially always,
// for both notifyDrained and any server push landing while the app is open.
describe("foreground notification handler", () => {
  it("is installed at import time and shows banner + list without sound or badge", async () => {
    expect(Notifications.setNotificationHandler).toHaveBeenCalledTimes(1);
    const handler = (Notifications.setNotificationHandler as jest.Mock).mock.calls[0][0];
    await expect(handler.handleNotification()).resolves.toEqual({
      shouldShowBanner: true,
      shouldShowList: true,
      shouldPlaySound: false,
      shouldSetBadge: false,
    });
  });
});

describe("routeForNotificationData", () => {
  it("routes an item notification to the real item detail screen", () => {
    expect(routeForNotificationData({ item_id: "abc" })).toBe("/item/abc");
  });

  // Mobile has no /lens screen at all (Lenses aren't built on this
  // platform), so a digest notification falls back to the Library tab
  // rather than a route that doesn't exist.
  it("routes a lens/digest notification to the Library tab, not a lens screen", () => {
    expect(routeForNotificationData({ lens_id: "design" })).toBe("/");
  });

  it("routes a single-feed river notification to the feed tab", () => {
    expect(routeForNotificationData({ feed_id: "f1" })).toBe("/feed");
  });

  it("routes a mixed-feed roll-up (no recognised key) to the feed tab", () => {
    expect(routeForNotificationData({})).toBe("/feed");
  });

  it("routes to the Library tab on lens_id presence alone, regardless of value type", () => {
    expect(routeForNotificationData({ lens_id: 42 })).toBe("/");
  });

  it("routes to the feed tab on feed_id presence alone, regardless of value type", () => {
    expect(routeForNotificationData({ feed_id: null })).toBe("/feed");
  });

  it("returns null for a payload it cannot understand", () => {
    expect(routeForNotificationData(null)).toBeNull();
    expect(routeForNotificationData(undefined)).toBeNull();
    expect(routeForNotificationData("nonsense")).toBeNull();
    expect(routeForNotificationData(42)).toBeNull();
    expect(routeForNotificationData({ item_id: 42 })).toBeNull();
    expect(routeForNotificationData({ item_id: null })).toBeNull();
    expect(routeForNotificationData({ item_id: { nested: true } })).toBeNull();
  });
});
