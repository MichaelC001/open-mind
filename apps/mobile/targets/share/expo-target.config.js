/** @type {import('@bacons/apple-targets/app.plugin').ConfigFunction} */
module.exports = (config) => ({
  type: "share",
  // The target name must differ from the main app target ("Openmind") — EAS
  // keys provisioning profiles by target name, and a collision signs the main
  // app with the extension's profile. displayName is what the share sheet shows.
  name: "OpenmindShare",
  displayName: "Openmind",
  deploymentTarget: "15.1",
  frameworks: ["UIKit", "UniformTypeIdentifiers"],
  // Must be present (even empty): the plugin only syncs the main app's App
  // Groups into a share target when an entitlements object is defined. The
  // shared UserDefaults suite carries { instanceUrl, token } for the extension.
  entitlements: {},
});
