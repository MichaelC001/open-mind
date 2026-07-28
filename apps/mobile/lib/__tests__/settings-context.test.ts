// settings-context.tsx renders a React context provider (JSX lives in the
// .tsx source), but this file stays .test.ts per the existing test-file
// convention — it drives the provider with react-test-renderer and
// React.createElement rather than JSX, and exercises signOut() through a
// harness component that captures the context value.
//
// ./settings, ./notifications, and ./api are mocked wholesale: ./settings
// wraps expo-secure-store (a native module unavailable under Jest), and
// ./notifications imports expo-notifications at module scope purely as an
// import-time side effect (see notifications.test.ts's comment on the same
// issue) — neither is needed to exercise signOut()'s own orchestration.
const mockGetSettings = jest.fn();
const mockClearSettings = jest.fn();
const mockPersistSettings = jest.fn();
jest.mock("../settings", () => ({
  getSettings: (...args: unknown[]) => mockGetSettings(...args),
  clearSettings: (...args: unknown[]) => mockClearSettings(...args),
  setSettings: (...args: unknown[]) => mockPersistSettings(...args),
}));

const mockGetStoredPushToken = jest.fn();
const mockSetStoredPushToken = jest.fn();
jest.mock("../notifications", () => ({
  getStoredPushToken: (...args: unknown[]) => mockGetStoredPushToken(...args),
  setStoredPushToken: (...args: unknown[]) => mockSetStoredPushToken(...args),
}));

const mockUnregisterPushDevice = jest.fn();
jest.mock("../api", () => ({
  unregisterPushDevice: (...args: unknown[]) => mockUnregisterPushDevice(...args),
}));

import * as React from "react";
import * as TestRenderer from "react-test-renderer";
import { SettingsProvider, useSettingsContext } from "../settings-context";

const STORED_SETTINGS = { instanceUrl: "https://openmind.example.com", token: "omk_current" };

type Captured = ReturnType<typeof useSettingsContext>;

function Harness({ onValue }: { onValue: (v: Captured) => void }) {
  const value = useSettingsContext();
  onValue(value);
  return null;
}

async function renderHarness(): Promise<{ latest: () => Captured }> {
  let latest: Captured | null = null;
  await TestRenderer.act(async () => {
    TestRenderer.create(
      React.createElement(SettingsProvider, null, [
        React.createElement(Harness, { key: "h", onValue: (v: Captured) => (latest = v) }),
      ]),
    );
    // Flush the provider's mount-time reload() microtask queue.
    await Promise.resolve();
    await Promise.resolve();
  });
  return { latest: () => latest as Captured };
}

beforeEach(() => {
  jest.clearAllMocks();
  mockGetSettings.mockResolvedValue(STORED_SETTINGS);
  mockClearSettings.mockResolvedValue(undefined);
  mockSetStoredPushToken.mockResolvedValue(undefined);
});

test("sign-out unregisters the stored push token, in the auth-token-still-available order, before clearing settings", async () => {
  mockGetStoredPushToken.mockResolvedValue("expo-token-1");
  mockUnregisterPushDevice.mockResolvedValue({ ok: true, status: 204 });

  const { latest } = await renderHarness();
  await TestRenderer.act(async () => {
    await latest().signOut();
  });

  expect(mockUnregisterPushDevice).toHaveBeenCalledWith("expo-token-1", STORED_SETTINGS);
  expect(mockSetStoredPushToken).toHaveBeenCalledWith(null);
  expect(mockClearSettings).toHaveBeenCalledTimes(1);

  // The unregister call needs the instance URL and auth token that
  // clearSettings() is about to delete, so it must run first.
  const unregisterOrder = mockUnregisterPushDevice.mock.invocationCallOrder[0];
  const clearOrder = mockClearSettings.mock.invocationCallOrder[0];
  expect(unregisterOrder).toBeLessThan(clearOrder);

  expect(latest().configured).toBe(false);
});

test("a failing unregister is logged but still lets sign-out complete", async () => {
  mockGetStoredPushToken.mockResolvedValue("expo-token-1");
  // The real unregisterPushDevice catches its own network errors and never
  // rejects — this is the shape production actually produces, unlike a
  // thrown error.
  mockUnregisterPushDevice.mockResolvedValue({ ok: false, status: 0 });
  const consoleError = jest.spyOn(console, "error").mockImplementation(() => {});

  const { latest } = await renderHarness();
  await TestRenderer.act(async () => {
    await latest().signOut();
  });

  expect(mockUnregisterPushDevice).toHaveBeenCalledWith("expo-token-1", STORED_SETTINGS);
  expect(consoleError).toHaveBeenCalled();
  // Sign-out must not be blocked by the failure: local state still clears.
  expect(mockSetStoredPushToken).toHaveBeenCalledWith(null);
  expect(mockClearSettings).toHaveBeenCalledTimes(1);
  expect(latest().configured).toBe(false);

  consoleError.mockRestore();
});

test("no stored push token means unregister is never attempted, and sign-out still completes", async () => {
  mockGetStoredPushToken.mockResolvedValue(null);

  const { latest } = await renderHarness();
  await TestRenderer.act(async () => {
    await latest().signOut();
  });

  expect(mockUnregisterPushDevice).not.toHaveBeenCalled();
  expect(mockSetStoredPushToken).toHaveBeenCalledWith(null);
  expect(mockClearSettings).toHaveBeenCalledTimes(1);
  expect(latest().configured).toBe(false);
});

// save() is reachable while already signed in ("Connect with code" and
// "Validate & save" both stay visible), so it is the other path — besides
// signOut — through which one account's credentials can replace another's on
// the same install. These tests cover the same release behaviour signOut has
// above, plus the guard that skips it when the credentials aren't actually
// changing.
const OTHER_ACCOUNT_SETTINGS = { instanceUrl: "https://openmind.example.com", token: "omk_other" };

test("save() releases the outgoing token when the credentials change to a different account", async () => {
  mockGetStoredPushToken.mockResolvedValue("expo-token-1");
  mockUnregisterPushDevice.mockResolvedValue({ ok: true, status: 204 });
  mockGetSettings.mockResolvedValueOnce(STORED_SETTINGS).mockResolvedValueOnce(OTHER_ACCOUNT_SETTINGS);

  const { latest } = await renderHarness();
  await TestRenderer.act(async () => {
    await latest().save(OTHER_ACCOUNT_SETTINGS);
  });

  // Released with the *outgoing* settings — the token belongs to the
  // account being replaced, and the server scopes the delete by the
  // caller's own user_id, so calling with the new credentials would no-op.
  expect(mockUnregisterPushDevice).toHaveBeenCalledWith("expo-token-1", STORED_SETTINGS);
  expect(mockSetStoredPushToken).toHaveBeenCalledWith(null);
  expect(mockPersistSettings).toHaveBeenCalledWith(OTHER_ACCOUNT_SETTINGS);
});

test("save() does not release when saving unchanged credentials", async () => {
  const { latest } = await renderHarness();
  await TestRenderer.act(async () => {
    await latest().save(STORED_SETTINGS);
  });

  expect(mockGetStoredPushToken).not.toHaveBeenCalled();
  expect(mockUnregisterPushDevice).not.toHaveBeenCalled();
  expect(mockSetStoredPushToken).not.toHaveBeenCalled();
  expect(mockPersistSettings).toHaveBeenCalledWith(STORED_SETTINGS);
});

test("a failing release still lets save() complete", async () => {
  mockGetStoredPushToken.mockResolvedValue("expo-token-1");
  mockUnregisterPushDevice.mockResolvedValue({ ok: false, status: 0 });
  mockGetSettings.mockResolvedValueOnce(STORED_SETTINGS).mockResolvedValueOnce(OTHER_ACCOUNT_SETTINGS);
  const consoleError = jest.spyOn(console, "error").mockImplementation(() => {});

  const { latest } = await renderHarness();
  await TestRenderer.act(async () => {
    await latest().save(OTHER_ACCOUNT_SETTINGS);
  });

  expect(mockUnregisterPushDevice).toHaveBeenCalledWith("expo-token-1", STORED_SETTINGS);
  expect(consoleError).toHaveBeenCalled();
  expect(mockSetStoredPushToken).toHaveBeenCalledWith(null);
  expect(mockPersistSettings).toHaveBeenCalledWith(OTHER_ACCOUNT_SETTINGS);

  consoleError.mockRestore();
});
