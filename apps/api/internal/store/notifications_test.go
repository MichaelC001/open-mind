package store_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

// TestEnqueueNotificationDedupes proves the partial unique index actually
// collapses a duplicate producer insert while the first row is still pending,
// and that the same key is insertable again once the first row is sent.
func TestEnqueueNotificationDedupes(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := uuid.New()
	if err := s.Queries.EnsureUser(ctx, uid); err != nil {
		t.Fatalf("ensure user: %v", err)
	}

	arg := db.EnqueueNotificationParams{
		UserID:    uid,
		Category:  "feed_river",
		DedupeKey: "feed_river:abc:2026-07-27T09",
		Title:     "1 new item",
		Body:      "",
		Data:      []byte(`{}`),
	}
	for range 3 {
		if err := s.Queries.EnqueueNotification(ctx, arg); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}

	due, err := s.Queries.ListDueNotifications(ctx, uid)
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("pending rows = %d, want 1", len(due))
	}

	if err := s.Queries.MarkNotificationsSent(ctx, db.MarkNotificationsSentParams{UserID: uid, Ids: []uuid.UUID{due[0].ID}}); err != nil {
		t.Fatalf("mark sent: %v", err)
	}
	if err := s.Queries.EnqueueNotification(ctx, arg); err != nil {
		t.Fatalf("re-enqueue after send: %v", err)
	}
	due2, err := s.Queries.ListDueNotifications(ctx, uid)
	if err != nil {
		t.Fatalf("list due 2: %v", err)
	}
	if len(due2) != 1 {
		t.Fatalf("pending rows after re-enqueue = %d, want 1", len(due2))
	}
}

// TestListPushDevicesSkipsFailed proves a device marked DeviceNotRegistered
// drops out of the target query.
func TestListPushDevicesSkipsFailed(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := uuid.New()
	if err := s.Queries.EnsureUser(ctx, uid); err != nil {
		t.Fatalf("ensure user: %v", err)
	}
	for _, tok := range []string{"ExponentPushToken[a]", "ExponentPushToken[b]"} {
		if _, err := s.Queries.UpsertPushDevice(ctx, db.UpsertPushDeviceParams{UserID: uid, Token: tok, Platform: "ios"}); err != nil {
			t.Fatalf("upsert %s: %v", tok, err)
		}
	}
	if err := s.Queries.MarkPushDeviceFailed(ctx, "ExponentPushToken[a]"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	devices, err := s.Queries.ListPushDevices(ctx, uid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(devices) != 1 || devices[0].Token != "ExponentPushToken[b]" {
		t.Fatalf("devices = %+v, want only token b", devices)
	}
}
