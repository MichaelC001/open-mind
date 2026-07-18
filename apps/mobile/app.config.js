// Dynamic Expo config. app.json remains the source of truth for everything;
// this wrapper only injects the Android Google Maps API key from the
// GOOGLE_MAPS_ANDROID_API_KEY env var at build time, so the key never lives in
// git (the repo is public). react-native-maps on Android needs it to render;
// without it (local dev, or a self-hoster who hasn't set one) the map is
// simply blank on Android and the rest of the app is unaffected.
const appJson = require("./app.json");

module.exports = () => {
  const expo = appJson.expo;
  const mapsKey = process.env.GOOGLE_MAPS_ANDROID_API_KEY;
  return {
    ...expo,
    android: {
      ...expo.android,
      ...(mapsKey
        ? { config: { ...(expo.android.config ?? {}), googleMaps: { apiKey: mapsKey } } }
        : {}),
    },
  };
};
