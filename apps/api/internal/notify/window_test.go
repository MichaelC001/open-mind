package notify

import (
	"testing"
	"time"
)

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("loading %s: %v", name, err)
	}
	return loc
}

func TestNextDeliverable(t *testing.T) {
	london := mustLoad(t, "Europe/London")

	tests := []struct {
		name  string
		now   time.Time
		prefs Prefs
		want  time.Time
	}{
		{
			name:  "no quiet hours passes through",
			now:   time.Date(2026, 7, 27, 3, 0, 0, 0, time.UTC),
			prefs: Prefs{Location: time.UTC},
			want:  time.Date(2026, 7, 27, 3, 0, 0, 0, time.UTC),
		},
		{
			name:  "outside quiet window passes through",
			now:   time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC),
			prefs: Prefs{Location: time.UTC, QuietFrom: "22:00", QuietTo: "07:00"},
			want:  time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC),
		},
		{
			name:  "inside overnight window before midnight defers to morning",
			now:   time.Date(2026, 7, 27, 23, 30, 0, 0, time.UTC),
			prefs: Prefs{Location: time.UTC, QuietFrom: "22:00", QuietTo: "07:00"},
			want:  time.Date(2026, 7, 28, 7, 0, 0, 0, time.UTC),
		},
		{
			name:  "inside overnight window after midnight defers to same morning",
			now:   time.Date(2026, 7, 27, 2, 0, 0, 0, time.UTC),
			prefs: Prefs{Location: time.UTC, QuietFrom: "22:00", QuietTo: "07:00"},
			want:  time.Date(2026, 7, 27, 7, 0, 0, 0, time.UTC),
		},
		{
			name:  "same-day window defers to its end",
			now:   time.Date(2026, 7, 27, 13, 0, 0, 0, time.UTC),
			prefs: Prefs{Location: time.UTC, QuietFrom: "09:00", QuietTo: "17:00"},
			want:  time.Date(2026, 7, 27, 17, 0, 0, 0, time.UTC),
		},
		{
			// Quiet hours are wall-clock in the user's zone. 23:30 UTC in July
			// is 00:30 BST, which is inside 22:00-07:00 London, and the window
			// ends at 07:00 BST = 06:00 UTC the same London day.
			name:  "wall-clock arithmetic happens in the user's zone",
			now:   time.Date(2026, 7, 27, 23, 30, 0, 0, time.UTC),
			prefs: Prefs{Location: london, QuietFrom: "22:00", QuietTo: "07:00"},
			want:  time.Date(2026, 7, 28, 6, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextDeliverable(tt.now, tt.prefs)
			if !got.Equal(tt.want) {
				t.Errorf("NextDeliverable = %s, want %s", got.UTC(), tt.want.UTC())
			}
		})
	}
}
