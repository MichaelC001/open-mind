package jobs

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/rohithgilla12/openmind/api/internal/notify"
)

// A producer running twice must leave exactly one pending row — the outbox
// dedupe is what makes every producer safe to retry.
func TestEnqueueNotificationIsIdempotent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := uuid.New()
	if err := s.Queries.EnsureUser(ctx, uid); err != nil {
		t.Fatalf("ensure user: %v", err)
	}

	for range 2 {
		if err := EnqueueNotification(ctx, s, uid, notify.CategoryDigest,
			"digest:lens-a:2026-07-27", "Design digest — 7 new saves", "",
			map[string]any{"lens_id": "lens-a"}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}

	due, err := s.Queries.ListDueNotifications(ctx, uid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("pending = %d, want 1", len(due))
	}
	if due[0].Title != "Design digest — 7 new saves" {
		t.Errorf("Title = %q", due[0].Title)
	}
}

func TestPlural(t *testing.T) {
	if got := plural(1, "new save", "new saves"); got != "1 new save" {
		t.Errorf("plural(1) = %q", got)
	}
	if got := plural(7, "new save", "new saves"); got != "7 new saves" {
		t.Errorf("plural(7) = %q", got)
	}
}
