package feeds

import (
	"testing"
	"time"
)

func TestNextPollInterval(t *testing.T) {
	tests := []struct {
		name        string
		current     time.Duration
		hadNew      bool
		cacheMaxAge time.Duration
		want        time.Duration
	}{
		{"new items reset to floor", 4 * time.Hour, true, 0, pollFloor},
		{"unchanged doubles", 30 * time.Minute, false, 0, time.Hour},
		{"doubling caps at 24h", 20 * time.Hour, false, 0, pollCap},
		{"cache max-age raises a shorter interval", 30 * time.Minute, false, 0, time.Hour},
		{"cache max-age floors even on new items", 0, true, 2 * time.Hour, 2 * time.Hour},
		{"cache max-age below computed is ignored", 30 * time.Minute, false, 10 * time.Minute, time.Hour},
		{"zero current backs off from floor", 0, false, 0, pollFloor},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextPollInterval(tt.current, tt.hadNew, tt.cacheMaxAge); got != tt.want {
				t.Errorf("nextPollInterval(%v,%v,%v) = %v, want %v", tt.current, tt.hadNew, tt.cacheMaxAge, got, tt.want)
			}
		})
	}
}

func TestParseCacheControlMaxAge(t *testing.T) {
	tests := []struct {
		name, cc, age string
		want          time.Duration
	}{
		{"valid", "max-age=3600", "", time.Hour},
		{"with other directives", "public, max-age=600, must-revalidate", "", 10 * time.Minute},
		{"subtracts age", "max-age=3600", "600", 50 * time.Minute},
		{"age exceeds max-age", "max-age=100", "200", 0},
		{"absent", "public", "", 0},
		{"empty", "", "", 0},
		{"malformed", "max-age=abc", "", 0},
		{"no-store ignored for pacing", "no-store", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseCacheControlMaxAge(tt.cc, tt.age); got != tt.want {
				t.Errorf("parseCacheControlMaxAge(%q,%q) = %v, want %v", tt.cc, tt.age, got, tt.want)
			}
		})
	}
}
