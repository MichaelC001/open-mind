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
		if _, err := s.Queries.UpsertPushDevice(ctx, db.UpsertPushDeviceParams{UserID: uid, Token: tok, Platform: "ios"}); err != nil {
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

// TestListRecentTicketsDeduplicatesTicketIDs is the regression test for the
// whole-branch review's I3 finding: deliverOne writes one ledger row per
// (result x source row), so a single coalesced feed-river message spanning
// several source rows delivered to one device writes several ledger rows
// carrying the same ticket_id. Before the DISTINCT fix, ListRecentTickets
// returned one row per ledger row rather than per ticket, inflating the
// count posted to Expo's getReceipts endpoint far beyond the number of
// actual outstanding tickets — the mechanism that let a busy instance wedge
// check_receipts well below any realistic real push volume.
func TestListRecentTicketsDeduplicatesTicketIDs(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := uuid.New()
	if err := s.Queries.EnsureUser(ctx, uid); err != nil {
		t.Fatalf("ensure user: %v", err)
	}
	for _, key := range []string{"d1", "d2"} {
		if err := s.Queries.EnqueueNotification(ctx, db.EnqueueNotificationParams{
			UserID: uid, Category: "feed_river", DedupeKey: key, Title: "t", Body: "", Data: []byte(`{}`),
		}); err != nil {
			t.Fatalf("enqueue %s: %v", key, err)
		}
	}
	due, err := s.Queries.ListDueNotifications(ctx, uid)
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	if len(due) != 2 {
		t.Fatalf("due = %d, want 2", len(due))
	}
	// One ticket_id/token pair recorded once per source row, exactly as
	// deliverOne does for a coalesced message's ledger writes.
	for _, row := range due {
		if err := s.Queries.RecordDelivery(ctx, db.RecordDeliveryParams{
			UserID: uid, NotificationID: row.ID, Channel: "expo",
			Token: "ExponentPushToken[x]", TicketID: "ticket-shared", Ok: true,
		}); err != nil {
			t.Fatalf("record delivery: %v", err)
		}
	}

	rows, err := s.Queries.ListRecentTickets(ctx)
	if err != nil {
		t.Fatalf("list recent tickets: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("rows = %d, want 1 (deduplicated despite 2 ledger rows sharing one ticket_id)", len(rows))
	}
}

// Re-running must be safe — the worker deliberately has no "checked" marker.
func TestCheckReceiptsIsIdempotent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := uuid.New()
	s.Queries.EnsureUser(ctx, uid)
	if _, err := s.Queries.UpsertPushDevice(ctx, db.UpsertPushDeviceParams{UserID: uid, Token: "ExponentPushToken[dead]", Platform: "ios"}); err != nil {
		t.Fatalf("upsert device: %v", err)
	}
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

// pruneFixture is one notifications row inserted with explicit timestamps —
// EnqueueNotification always defaults to now(), so aged-out rows have to be
// written directly.
type pruneFixture struct {
	key          string
	sentAtExpr   string // SQL expression, or "NULL"
	attempts     int
	createdAtAgo string // SQL interval literal, e.g. "31 days"
	wantSurvives bool
}

func TestPruneNotificationsDeletesAgedOutRows(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := uuid.New()
	if err := s.Queries.EnsureUser(ctx, uid); err != nil {
		t.Fatalf("ensure user: %v", err)
	}

	fixtures := []pruneFixture{
		// Delivered rows age out after 30 days; a recent one survives.
		{"old-sent", "now() - interval '31 days'", 0, "31 days", false},
		{"recent-sent", "now() - interval '1 day'", 0, "1 day", true},
		// Abandoned rows (retries exhausted, never sent) age out after 7
		// days; still within the grace period survives.
		{"old-abandoned", "NULL", 3, "8 days", false},
		{"recent-abandoned", "NULL", 3, "1 day", true},
		// Never-claimed rows (e.g. permanently deferred for lack of a
		// target) age out after 30 days regardless of attempts.
		{"old-unclaimed", "NULL", 0, "31 days", false},
		{"recent-unclaimed", "NULL", 0, "1 day", true},
	}
	for _, f := range fixtures {
		sql := `INSERT INTO notifications (user_id, category, dedupe_key, title, body, sent_at, attempts, created_at, deliver_after)
			VALUES ($1, 'digest', $2, 't', '', ` + f.sentAtExpr + `, $3, now() - interval '` + f.createdAtAgo + `', now())`
		if _, err := s.Pool.Exec(ctx, sql, uid, f.key, f.attempts); err != nil {
			t.Fatalf("insert fixture %s: %v", f.key, err)
		}
	}

	wantDeleted := 0
	for _, f := range fixtures {
		if !f.wantSurvives {
			wantDeleted++
		}
	}

	count, err := s.Queries.PruneNotifications(ctx)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if count != int64(wantDeleted) {
		t.Errorf("deleted = %d, want %d", count, wantDeleted)
	}
	for _, f := range fixtures {
		var n int
		if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE dedupe_key = $1`, f.key).Scan(&n); err != nil {
			t.Fatalf("counting %s: %v", f.key, err)
		}
		survived := n == 1
		if survived != f.wantSurvives {
			t.Errorf("fixture %s survived = %v, want %v", f.key, survived, f.wantSurvives)
		}
	}
}

// Re-running against an already-pruned table must be safe and change nothing.
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
