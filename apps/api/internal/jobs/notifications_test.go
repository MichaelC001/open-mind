package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/rohithgilla12/openmind/api/internal/notify"
	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

// flushFixture builds a worker over a real store with one user, a registered
// device, and recording senders.
func flushFixture(t *testing.T) (*FlushNotificationsWorker, uuid.UUID, *notify.Fake, *notify.Fake) {
	t.Helper()
	s := testStore(t)
	ctx := context.Background()
	uid := uuid.New()
	if err := s.Queries.EnsureUser(ctx, uid); err != nil {
		t.Fatalf("ensure user: %v", err)
	}
	if err := s.Queries.UpsertPushDevice(ctx, db.UpsertPushDeviceParams{
		UserID: uid, Token: "ExponentPushToken[x]", Platform: "ios",
	}); err != nil {
		t.Fatalf("upsert device: %v", err)
	}
	push, email := notify.NewFake(), notify.NewFake()
	push.ChannelName, email.ChannelName = "expo", "email"
	w := &FlushNotificationsWorker{
		Store: s,
		Deps:  NotifyDeps{Router: notify.NewRouter(push, email), Configured: true},
	}
	return w, uid, push, email
}

func enqueue(t *testing.T, w *FlushNotificationsWorker, uid uuid.UUID, cat, key, title string, data string) {
	t.Helper()
	if err := w.Store.Queries.EnqueueNotification(context.Background(), db.EnqueueNotificationParams{
		UserID: uid, Category: cat, DedupeKey: key, Title: title, Body: "", Data: []byte(data),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
}

func TestFlushDeliversAndStamps(t *testing.T) {
	w, uid, push, _ := flushFixture(t)
	ctx := context.Background()
	enqueue(t, w, uid, "digest", "digest:lens-a:2026-07-27", "Design digest — 7 new saves", `{}`)

	if err := w.Work(ctx, &river.Job[FlushNotificationsArgs]{Args: FlushNotificationsArgs{UserID: uid}}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(push.Sent) != 1 {
		t.Fatalf("push sends = %d, want 1", len(push.Sent))
	}
	due, err := w.Store.Queries.ListDueNotifications(ctx, uid)
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("pending after flush = %d, want 0", len(due))
	}
}

// Running the flush twice must not deliver twice — CLAUDE.md's idempotency rule.
func TestFlushIsIdempotent(t *testing.T) {
	w, uid, push, _ := flushFixture(t)
	ctx := context.Background()
	enqueue(t, w, uid, "digest", "digest:lens-a:2026-07-27", "Design digest", `{}`)

	job := &river.Job[FlushNotificationsArgs]{Args: FlushNotificationsArgs{UserID: uid}}
	if err := w.Work(ctx, job); err != nil {
		t.Fatalf("first Work: %v", err)
	}
	if err := w.Work(ctx, job); err != nil {
		t.Fatalf("second Work: %v", err)
	}
	if len(push.Sent) != 1 {
		t.Errorf("push sends = %d after two flushes, want 1", len(push.Sent))
	}
}

// feed_river defaults to off: rows must be stamped, not delivered, so they do
// not accumulate in the pending index forever.
func TestFlushStampsDisabledCategoryWithoutDelivering(t *testing.T) {
	w, uid, push, email := flushFixture(t)
	ctx := context.Background()
	enqueue(t, w, uid, "feed_river", "feed_river:f1:2026-07-27T09", "5 new items", `{"feed_id":"f1","count":5}`)

	if err := w.Work(ctx, &river.Job[FlushNotificationsArgs]{Args: FlushNotificationsArgs{UserID: uid}}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(push.Sent)+len(email.Sent) != 0 {
		t.Errorf("delivered %d messages for a disabled category", len(push.Sent)+len(email.Sent))
	}
	due, _ := w.Store.Queries.ListDueNotifications(ctx, uid)
	if len(due) != 0 {
		t.Errorf("pending = %d, want 0 (disabled rows must be stamped)", len(due))
	}
}

func TestFlushCoalescesFeedRiver(t *testing.T) {
	w, uid, push, _ := flushFixture(t)
	ctx := context.Background()
	if err := w.Store.Queries.UpsertUserSetting(ctx, db.UpsertUserSettingParams{
		UserID: uid, Key: notify.KeyFeedRiver, Value: "push",
	}); err != nil {
		t.Fatalf("set pref: %v", err)
	}
	enqueue(t, w, uid, "feed_river", "feed_river:f1:2026-07-27T09", "5 new items", `{"feed_id":"f1","count":5}`)
	enqueue(t, w, uid, "feed_river", "feed_river:f2:2026-07-27T09", "4 new items", `{"feed_id":"f2","count":4}`)
	enqueue(t, w, uid, "feed_river", "feed_river:f3:2026-07-27T09", "3 new items", `{"feed_id":"f3","count":3}`)

	if err := w.Work(ctx, &river.Job[FlushNotificationsArgs]{Args: FlushNotificationsArgs{UserID: uid}}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(push.Sent) != 1 {
		t.Fatalf("push sends = %d, want 1 coalesced message", len(push.Sent))
	}
	if push.Sent[0].Title != "12 new items across 3 feeds" {
		t.Errorf("Title = %q", push.Sent[0].Title)
	}
}

// Quiet hours defer rather than drop.
func TestFlushDefersDuringQuietHours(t *testing.T) {
	w, uid, push, _ := flushFixture(t)
	ctx := context.Background()
	// A window covering the whole day guarantees "now" is inside it whenever
	// this test runs, without the test having to control the clock.
	if err := w.Store.Queries.UpsertUserSetting(ctx, db.UpsertUserSettingParams{
		UserID: uid, Key: notify.KeyQuietHours, Value: "00:00-23:59",
	}); err != nil {
		t.Fatalf("set quiet hours: %v", err)
	}
	enqueue(t, w, uid, "digest", "digest:lens-a:2026-07-27", "Design digest", `{}`)

	if err := w.Work(ctx, &river.Job[FlushNotificationsArgs]{Args: FlushNotificationsArgs{UserID: uid}}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(push.Sent) != 0 {
		t.Errorf("delivered %d messages during quiet hours", len(push.Sent))
	}
	due, _ := w.Store.Queries.ListDueNotifications(ctx, uid)
	if len(due) != 0 {
		t.Errorf("rows still due = %d; deferral should have pushed deliver_after out", len(due))
	}
}

// lifecycle must ignore an exhausted daily cap.
func TestFlushLifecycleBypassesCap(t *testing.T) {
	w, uid, push, _ := flushFixture(t)
	ctx := context.Background()
	if err := w.Store.Queries.UpsertUserSetting(ctx, db.UpsertUserSettingParams{
		UserID: uid, Key: notify.KeyDailyCap, Value: "0",
	}); err != nil {
		t.Fatalf("set cap: %v", err)
	}
	enqueue(t, w, uid, "lifecycle", "lifecycle:item-1", "We couldn't process a save", `{"item_id":"item-1"}`)

	if err := w.Work(ctx, &river.Job[FlushNotificationsArgs]{Args: FlushNotificationsArgs{UserID: uid}}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(push.Sent) != 1 {
		t.Errorf("push sends = %d, want 1 (lifecycle bypasses the cap)", len(push.Sent))
	}
	_ = time.Now
}
