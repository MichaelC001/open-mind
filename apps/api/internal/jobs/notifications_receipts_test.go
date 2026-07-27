package jobs

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

// stubReceipts returns a fixed ticket-to-error-code map.
type stubReceipts struct {
	codes map[string]string
	asked []string
}

func (s *stubReceipts) Receipts(_ context.Context, ids []string) (map[string]string, error) {
	s.asked = ids
	return s.codes, nil
}

func TestCheckReceiptsRetiresUnregisteredDevice(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := uuid.New()
	if err := s.Queries.EnsureUser(ctx, uid); err != nil {
		t.Fatalf("ensure user: %v", err)
	}
	for _, tok := range []string{"ExponentPushToken[dead]", "ExponentPushToken[live]"} {
		if err := s.Queries.UpsertPushDevice(ctx, db.UpsertPushDeviceParams{UserID: uid, Token: tok, Platform: "ios"}); err != nil {
			t.Fatalf("upsert %s: %v", tok, err)
		}
	}
	if err := s.Queries.EnqueueNotification(ctx, db.EnqueueNotificationParams{
		UserID: uid, Category: "digest", DedupeKey: "d1", Title: "t", Body: "", Data: []byte(`{}`),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	due, _ := s.Queries.ListDueNotifications(ctx, uid)
	for _, tc := range []struct{ token, ticket string }{
		{"ExponentPushToken[dead]", "ticket-dead"},
		{"ExponentPushToken[live]", "ticket-live"},
	} {
		if err := s.Queries.RecordDelivery(ctx, db.RecordDeliveryParams{
			UserID: uid, NotificationID: due[0].ID, Channel: "expo",
			Token: tc.token, TicketID: tc.ticket, Ok: true,
		}); err != nil {
			t.Fatalf("record delivery: %v", err)
		}
	}

	w := &CheckReceiptsWorker{Store: s, Deps: NotifyDeps{Receipts: &stubReceipts{codes: map[string]string{
		"ticket-dead": "DeviceNotRegistered",
		"ticket-live": "",
	}}}}
	if err := w.Work(ctx, &river.Job[CheckReceiptsArgs]{}); err != nil {
		t.Fatalf("Work: %v", err)
	}

	devices, err := s.Queries.ListPushDevices(ctx, uid)
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if len(devices) != 1 || devices[0].Token != "ExponentPushToken[live]" {
		t.Errorf("devices = %+v, want only the live token", devices)
	}
}

// Re-running must be safe — the worker deliberately has no "checked" marker.
func TestCheckReceiptsIsIdempotent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := uuid.New()
	s.Queries.EnsureUser(ctx, uid)
	s.Queries.UpsertPushDevice(ctx, db.UpsertPushDeviceParams{UserID: uid, Token: "ExponentPushToken[dead]", Platform: "ios"})
	s.Queries.EnqueueNotification(ctx, db.EnqueueNotificationParams{
		UserID: uid, Category: "digest", DedupeKey: "d1", Title: "t", Body: "", Data: []byte(`{}`),
	})
	due, _ := s.Queries.ListDueNotifications(ctx, uid)
	s.Queries.RecordDelivery(ctx, db.RecordDeliveryParams{
		UserID: uid, NotificationID: due[0].ID, Channel: "expo",
		Token: "ExponentPushToken[dead]", TicketID: "ticket-dead", Ok: true,
	})

	w := &CheckReceiptsWorker{Store: s, Deps: NotifyDeps{Receipts: &stubReceipts{
		codes: map[string]string{"ticket-dead": "DeviceNotRegistered"},
	}}}
	job := &river.Job[CheckReceiptsArgs]{}
	if err := w.Work(ctx, job); err != nil {
		t.Fatalf("first Work: %v", err)
	}
	if err := w.Work(ctx, job); err != nil {
		t.Fatalf("second Work: %v", err)
	}
	devices, _ := s.Queries.ListPushDevices(ctx, uid)
	if len(devices) != 0 {
		t.Errorf("devices = %+v, want none", devices)
	}
}

// With Expo unconfigured the job must be a silent no-op, not a failure.
func TestCheckReceiptsNoopWithoutExpo(t *testing.T) {
	s := testStore(t)
	w := &CheckReceiptsWorker{Store: s, Deps: NotifyDeps{}}
	if err := w.Work(context.Background(), &river.Job[CheckReceiptsArgs]{}); err != nil {
		t.Fatalf("Work: %v", err)
	}
}

func TestPruneNotificationsRunsClean(t *testing.T) {
	s := testStore(t)
	w := &PruneNotificationsWorker{Store: s}
	job := &river.Job[PruneNotificationsArgs]{}
	if err := w.Work(context.Background(), job); err != nil {
		t.Fatalf("first Work: %v", err)
	}
	if err := w.Work(context.Background(), job); err != nil {
		t.Fatalf("second Work: %v", err)
	}
}
