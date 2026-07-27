package notify

import "time"

// NextDeliverable returns the earliest time at or after now that falls outside
// the user's quiet hours. When quiet hours are unset, or now is already
// outside them, it returns now unchanged.
//
// The arithmetic is deliberately done on wall-clock components in the user's
// own location rather than as a UTC offset: "no pushes after 22:00" means
// 22:00 where the user is, and that must keep meaning that across a DST
// transition, when the UTC offset itself changes.
func NextDeliverable(now time.Time, p Prefs) time.Time {
	if p.QuietFrom == "" || p.QuietTo == "" {
		return now
	}
	loc := p.Location
	if loc == nil {
		loc = time.UTC
	}
	local := now.In(loc)

	from, okFrom := clockMinutes(p.QuietFrom)
	to, okTo := clockMinutes(p.QuietTo)
	if !okFrom || !okTo || from == to {
		return now
	}

	cur := local.Hour()*60 + local.Minute()
	overnight := from > to

	inQuiet := false
	switch {
	case overnight:
		// e.g. 22:00-07:00 wraps midnight: quiet if at/after 22:00 OR before 07:00.
		inQuiet = cur >= from || cur < to
	default:
		inQuiet = cur >= from && cur < to
	}
	if !inQuiet {
		return now
	}

	// The window ends at `to` on the current local day, except when we are in
	// the pre-midnight leg of an overnight window, where it ends tomorrow.
	day := local
	if overnight && cur >= from {
		day = local.AddDate(0, 0, 1)
	}
	end := time.Date(day.Year(), day.Month(), day.Day(), to/60, to%60, 0, 0, loc)
	if !end.After(now) {
		return now
	}
	return end
}

// clockMinutes converts "HH:MM" to minutes past local midnight.
func clockMinutes(s string) (int, bool) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}
