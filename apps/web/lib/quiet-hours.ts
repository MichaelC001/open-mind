// The server stores quiet hours as a single "HH:MM-HH:MM" string (or "" for
// none) and treats an explicit "" as "clear this field" versus an omitted
// field meaning "leave it alone" — see PatchSettings in apps/api. The two
// time-of-day inputs on the settings page only ever see one half each, so
// this module owns turning that pair into the wire string and back.

export interface QuietHoursRange {
  start: string;
  end: string;
}

const QUIET_HOURS_PATTERN = /^([01]\d|2[0-3]):([0-5]\d)-([01]\d|2[0-3]):([0-5]\d)$/;

export function parseQuietHours(value: string): QuietHoursRange {
  const match = QUIET_HOURS_PATTERN.exec(value);
  if (!match) return { start: "", end: "" };
  return { start: `${match[1]}:${match[2]}`, end: `${match[3]}:${match[4]}` };
}

// Either side blank collapses to "" rather than a half-written range, since
// the server has no representation for "quiet hours starting at 22:00 with
// no end" and would reject anything that isn't a full HH:MM-HH:MM pair.
export function composeQuietHours(start: string, end: string): string {
  if (!start || !end) return "";
  return `${start}-${end}`;
}
