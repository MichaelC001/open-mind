package jobs

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// 2026-07-14 is a fixed, known Tuesday (UTC), used throughout so weekly
// schedule tests don't depend on when the suite happens to run.
var digestTestTuesday = time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
var digestTestWednesday = time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

func validTS(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func TestDigestDue(t *testing.T) {
	tests := []struct {
		name     string
		schedule string
		last     pgtype.Timestamptz
		now      time.Time
		want     bool
	}{
		{"daily never", "daily", pgtype.Timestamptz{}, digestTestTuesday, true},
		{"daily 21h", "daily", validTS(digestTestTuesday.Add(-21 * time.Hour)), digestTestTuesday, true},
		{"daily 3h", "daily", validTS(digestTestTuesday.Add(-3 * time.Hour)), digestTestTuesday, false},
		{"weekly:2 tuesday never", "weekly:2", pgtype.Timestamptz{}, digestTestTuesday, true},
		{"weekly:2 tuesday last 5d", "weekly:2", validTS(digestTestTuesday.Add(-5 * 24 * time.Hour)), digestTestTuesday, false},
		{"weekly:2 tuesday last 7d", "weekly:2", validTS(digestTestTuesday.Add(-7 * 24 * time.Hour)), digestTestTuesday, true},
		{"weekly:2 wednesday", "weekly:2", pgtype.Timestamptz{}, digestTestWednesday, false},
		{"empty schedule", "", pgtype.Timestamptz{}, digestTestTuesday, false},
		{"junk schedule", "junk", pgtype.Timestamptz{}, digestTestTuesday, false},
		{"weekly:9 invalid day", "weekly:9", pgtype.Timestamptz{}, digestTestTuesday, false},
		{"weekly:-1 invalid day", "weekly:-1", pgtype.Timestamptz{}, digestTestTuesday, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := digestDue(tt.schedule, tt.last, tt.now); got != tt.want {
				t.Errorf("digestDue(%q, %+v, %v) = %v, want %v", tt.schedule, tt.last, tt.now, got, tt.want)
			}
		})
	}
}
