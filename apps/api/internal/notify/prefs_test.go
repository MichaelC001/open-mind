package notify

import "testing"

func TestParsePrefsDefaults(t *testing.T) {
	p := ParsePrefs(nil)
	if got := p.For(CategoryDigest); !got.Push || got.Email {
		t.Errorf("digest default = %+v, want push only", got)
	}
	if got := p.For(CategoryFeedRiver); got.Push || got.Email {
		t.Errorf("feed_river default = %+v, want off", got)
	}
	if got := p.For(CategoryLifecycle); !got.Push || got.Email {
		t.Errorf("lifecycle default = %+v, want push only", got)
	}
	if p.DailyCap != 10 {
		t.Errorf("DailyCap = %d, want 10", p.DailyCap)
	}
	if p.Location.String() != "UTC" {
		t.Errorf("Location = %s, want UTC", p.Location)
	}
}

func TestParsePrefsOverrides(t *testing.T) {
	p := ParsePrefs(map[string]string{
		"notify.digest":      "both",
		"notify.feed_river":  "email",
		"notify.lifecycle":   "off",
		"notify.quiet_hours": "22:00-07:00",
		"notify.timezone":    "Europe/London",
		"notify.daily_cap":   "3",
	})
	if got := p.For(CategoryDigest); !got.Push || !got.Email {
		t.Errorf("digest = %+v, want both", got)
	}
	if got := p.For(CategoryFeedRiver); got.Push || !got.Email {
		t.Errorf("feed_river = %+v, want email only", got)
	}
	if got := p.For(CategoryLifecycle); got.Push || got.Email {
		t.Errorf("lifecycle = %+v, want off", got)
	}
	if p.QuietFrom != "22:00" || p.QuietTo != "07:00" {
		t.Errorf("quiet = %s-%s, want 22:00-07:00", p.QuietFrom, p.QuietTo)
	}
	if p.Location.String() != "Europe/London" {
		t.Errorf("Location = %s, want Europe/London", p.Location)
	}
	if p.DailyCap != 3 {
		t.Errorf("DailyCap = %d, want 3", p.DailyCap)
	}
}

// A bad timezone or cap must never block delivery — fall back to the default.
func TestParsePrefsBadValuesFallBack(t *testing.T) {
	p := ParsePrefs(map[string]string{
		"notify.timezone":    "Mars/Olympus",
		"notify.daily_cap":   "not-a-number",
		"notify.quiet_hours": "garbage",
	})
	if p.Location.String() != "UTC" {
		t.Errorf("Location = %s, want UTC fallback", p.Location)
	}
	if p.DailyCap != 10 {
		t.Errorf("DailyCap = %d, want 10 fallback", p.DailyCap)
	}
	if p.QuietFrom != "" || p.QuietTo != "" {
		t.Errorf("quiet = %s-%s, want empty on garbage", p.QuietFrom, p.QuietTo)
	}
}

// An unrecognised channel value fails closed: it must resolve to off, not fall back
// to the category default. This is correct because writes go through PATCH /settings,
// which validates against the four allowed values (off, push, email, both), so an
// unrecognised value means a corrupt row. Silence is the safer failure mode for a
// notification system than unexpected pushes.
func TestParsePrefsUnknownChannelFailsClosed(t *testing.T) {
	p := ParsePrefs(map[string]string{
		"notify.digest": "sometimes",
	})
	if got := p.For(CategoryDigest); got.Push || got.Email {
		t.Errorf("digest with corrupt value = %+v, want off (silence)", got)
	}
}
