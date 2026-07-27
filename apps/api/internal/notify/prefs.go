package notify

import (
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// user_settings keys backing Prefs.
const (
	KeyDigest     = "notify.digest"
	KeyFeedRiver  = "notify.feed_river"
	KeyLifecycle  = "notify.lifecycle"
	KeyQuietHours = "notify.quiet_hours"
	KeyTimezone   = "notify.timezone"
	KeyDailyCap   = "notify.daily_cap"
)

// defaultDailyCap is the number of successful deliveries a user receives per
// day before feed-river notifications defer to tomorrow.
const defaultDailyCap = 10

// Channels is the resolved per-category delivery choice.
type Channels struct {
	Push  bool
	Email bool
}

// Prefs is a typed view over the caller's notify.* user_settings rows.
type Prefs struct {
	Digest    Channels
	FeedRiver Channels
	Lifecycle Channels
	QuietFrom string
	QuietTo   string
	Location  *time.Location
	DailyCap  int
}

// For returns the channels enabled for c.
func (p Prefs) For(c Category) Channels {
	switch c {
	case CategoryDigest:
		return p.Digest
	case CategoryFeedRiver:
		return p.FeedRiver
	case CategoryLifecycle:
		return p.Lifecycle
	default:
		return Channels{}
	}
}

// ParsePrefs maps raw user_settings rows onto Prefs. Absent keys take the
// documented default, and unparseable values fall back rather than erroring:
// a bad preference must never block delivery.
func ParsePrefs(rows map[string]string) Prefs {
	p := Prefs{
		Digest:    Channels{Push: true},
		FeedRiver: Channels{},
		Lifecycle: Channels{Push: true},
		Location:  time.UTC,
		DailyCap:  defaultDailyCap,
	}
	if v, ok := rows[KeyDigest]; ok {
		p.Digest = parseChannels(v)
	}
	if v, ok := rows[KeyFeedRiver]; ok {
		p.FeedRiver = parseChannels(v)
	}
	if v, ok := rows[KeyLifecycle]; ok {
		p.Lifecycle = parseChannels(v)
	}
	if v := rows[KeyQuietHours]; v != "" {
		if from, to, ok := parseQuietHours(v); ok {
			p.QuietFrom, p.QuietTo = from, to
		} else {
			slog.Warn("notify: unparseable quiet_hours, ignoring", "value", v)
		}
	}
	if v := rows[KeyTimezone]; v != "" {
		if loc, ok := parseTimezone(v); ok {
			p.Location = loc
		} else {
			slog.Warn("notify: unknown timezone, using UTC", "value", v)
		}
	}
	if v := rows[KeyDailyCap]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			p.DailyCap = n
		} else {
			slog.Warn("notify: unparseable daily_cap, using default", "value", v)
		}
	}
	return p
}

// parseTimezone loads v as an IANA zone. "Local" is explicitly rejected even
// though time.LoadLocation accepts it: it resolves to the server process's
// own system timezone, not the user's, which would silently evaluate quiet
// hours in the wrong zone for a user who believes they've set their own —
// see validTimezone in internal/api/settings.go, which rejects it for the
// same reason at write time. Rejecting it again here is defence in depth for
// a row written before that validation existed, or written directly.
func parseTimezone(v string) (*time.Location, bool) {
	if v == "Local" {
		return nil, false
	}
	loc, err := time.LoadLocation(v)
	if err != nil {
		return nil, false
	}
	return loc, true
}

// parseChannels maps off|push|email|both onto Channels. An unrecognised value
// is treated as off, which fails closed (silence) rather than spamming.
func parseChannels(v string) Channels {
	switch v {
	case "push":
		return Channels{Push: true}
	case "email":
		return Channels{Email: true}
	case "both":
		return Channels{Push: true, Email: true}
	default:
		return Channels{}
	}
}

// parseQuietHours splits "22:00-07:00" and validates both halves are HH:MM.
func parseQuietHours(v string) (string, string, bool) {
	from, to, found := strings.Cut(v, "-")
	if !found || !validHHMM(from) || !validHHMM(to) {
		return "", "", false
	}
	return from, to, true
}

// validHHMM reports whether s is a 24-hour HH:MM clock time.
func validHHMM(s string) bool {
	_, err := time.Parse("15:04", s)
	return err == nil
}
