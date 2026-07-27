// Force expo's lazily-installed global `fetch` getter (installed by
// jest-expo's own preset setup, before this file runs) to resolve now, during
// this file's own setup phase. Left lazy, whichever test file first happens
// to touch `fetch` triggers a synchronous-looking chain into
// expo-modules-core (jest-expo's setup.js has its own
// "TODO: this is an invalid dependency chain" admission for this) that ends
// up logging asynchronously — late enough, once enough test files are in the
// run, to land after some other file's environment has already torn down.
// Jest then reports "Cannot log after tests are done" and fails the whole
// run even though every individual test passed. Resolving it here, up front,
// for every file, removes the race instead of chasing which file "won" it.
void globalThis.fetch;

// Global AsyncStorage mock for every test.
jest.mock("@react-native-async-storage/async-storage", () =>
  require("@react-native-async-storage/async-storage/jest/async-storage-mock"),
);
