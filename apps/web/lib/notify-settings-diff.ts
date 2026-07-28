// PatchSettings (apps/api/internal/api/settings.go) treats an omitted key as
// "leave unchanged" and an explicit "" as "clear to default" — but it has no
// "same value, skip it" check of its own: applyPref unconditionally upserts
// whatever non-empty value it's given. That means the client is the only
// place that can decide a field wasn't touched. Sending all six preferences
// on every save would turn every documented default the user never
// configured (push / off / push / "" / browser-guessed timezone / 10) into
// an explicit stored row the moment they saved anything else in this form —
// so this module diffs the loaded snapshot against the current form values
// and only includes a key when it actually changed.

export type ChannelPref = "off" | "push" | "email" | "both";

export interface NotifyFormValues {
  digest: ChannelPref;
  feedRiver: ChannelPref;
  lifecycle: ChannelPref;
  quietHours: string;
  timezone: string;
  dailyCap: number;
}

export interface NotifySettingsPatch {
  notifyDigest?: ChannelPref;
  notifyFeedRiver?: ChannelPref;
  notifyLifecycle?: ChannelPref;
  notifyQuietHours?: string;
  notifyTimezone?: string;
  notifyDailyCap?: number;
}

export function diffNotifySettings(loaded: NotifyFormValues, current: NotifyFormValues): NotifySettingsPatch {
  const patch: NotifySettingsPatch = {};
  if (current.digest !== loaded.digest) patch.notifyDigest = current.digest;
  if (current.feedRiver !== loaded.feedRiver) patch.notifyFeedRiver = current.feedRiver;
  if (current.lifecycle !== loaded.lifecycle) patch.notifyLifecycle = current.lifecycle;
  // Quiet hours isn't special-cased here: it's included whenever it changed,
  // same as every other field. "Changed to blank" just happens to produce
  // "" as the new value — it's the server's clear-vs-omit distinction that
  // makes that meaningful, not anything this diff needs to know about.
  if (current.quietHours !== loaded.quietHours) patch.notifyQuietHours = current.quietHours;
  if (current.timezone !== loaded.timezone) patch.notifyTimezone = current.timezone;
  // notifyDailyCap has no clear-to-default path (an int has no empty-string
  // escape hatch) — a known gap on the server side, not this module's to
  // fix. It follows the same only-if-changed rule as everything else.
  if (current.dailyCap !== loaded.dailyCap) patch.notifyDailyCap = current.dailyCap;
  return patch;
}
