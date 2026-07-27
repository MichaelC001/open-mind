package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"

	"github.com/rohithgilla12/openmind/api/internal/notify"
	"github.com/rohithgilla12/openmind/api/internal/reelmedia"
	"github.com/rohithgilla12/openmind/api/internal/store"
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
		Deps:  NotifyDeps{Router: notify.NewRouter(push, email)},
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

// readNotification reads a row's delivery state directly, for assertions
// ListDueNotifications can't make: that query filters sent_at IS NULL, so a
// row leaves it whether it was stamped (delivered, or intentionally
// disabled-and-stamped) or merely deferred — the two outcomes this substrate
// must never confuse. attempts distinguishes "deferred before claiming" (0)
// from "claimed and then left pending" (>0).
func readNotification(t *testing.T, w *FlushNotificationsWorker, uid uuid.UUID, dedupeKey string) (sentAt, deliverAfter pgtype.Timestamptz, lastError string, attempts int32) {
	t.Helper()
	err := w.Store.Pool.QueryRow(context.Background(),
		`SELECT sent_at, deliver_after, last_error, attempts FROM notifications WHERE user_id = $1 AND dedupe_key = $2`,
		uid, dedupeKey,
	).Scan(&sentAt, &deliverAfter, &lastError, &attempts)
	if err != nil {
		t.Fatalf("read notification row (dedupe_key=%s): %v", dedupeKey, err)
	}
	return
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
// not accumulate in the pending index forever. Asserted directly against
// sent_at, not ListDueNotifications, since that query can't distinguish
// "stamped" from "deferred".
func TestFlushStampsDisabledCategoryWithoutDelivering(t *testing.T) {
	w, uid, push, email := flushFixture(t)
	ctx := context.Background()
	const key = "feed_river:f1:2026-07-27T09"
	enqueue(t, w, uid, "feed_river", key, "5 new items", `{"feed_id":"f1","count":5}`)

	if err := w.Work(ctx, &river.Job[FlushNotificationsArgs]{Args: FlushNotificationsArgs{UserID: uid}}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(push.Sent)+len(email.Sent) != 0 {
		t.Errorf("delivered %d messages for a disabled category", len(push.Sent)+len(email.Sent))
	}
	sentAt, _, _, _ := readNotification(t, w, uid, key)
	if !sentAt.Valid {
		t.Errorf("sent_at not set; disabled-category rows must be stamped so they don't accumulate")
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

// Quiet hours defer rather than drop. Asserted directly against sent_at and
// deliver_after, since ListDueNotifications leaving the row is also
// consistent with the row having been dropped (stamped some other way) —
// this test guards a non-negotiable rule and must not pass on that bug.
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
	const key = "digest:lens-a:2026-07-27"
	enqueue(t, w, uid, "digest", key, "Design digest", `{}`)

	if err := w.Work(ctx, &river.Job[FlushNotificationsArgs]{Args: FlushNotificationsArgs{UserID: uid}}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(push.Sent) != 0 {
		t.Errorf("delivered %d messages during quiet hours", len(push.Sent))
	}
	sentAt, deliverAfter, _, _ := readNotification(t, w, uid, key)
	if sentAt.Valid {
		t.Errorf("sent_at set; quiet hours must defer, never drop or deliver")
	}
	if !deliverAfter.Time.After(time.Now()) {
		t.Errorf("deliver_after = %v, want pushed into the future by the quiet-hours window", deliverAfter.Time)
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
}

// One channel failing must not suppress delivery on the other, and a partial
// success still stamps the row: re-attempting later would duplicate the
// message to whichever channel already succeeded.
func TestFlushPartialChannelFailureStillStamps(t *testing.T) {
	w, uid, push, email := flushFixture(t)
	ctx := context.Background()
	push.Err = errors.New("expo: transport down")
	if err := w.Store.Queries.UpsertUserSetting(ctx, db.UpsertUserSettingParams{
		UserID: uid, Key: notify.KeyDigest, Value: "both",
	}); err != nil {
		t.Fatalf("set pref: %v", err)
	}
	if _, err := w.Store.Pool.Exec(ctx, `UPDATE users SET email = $1 WHERE id = $2`, "user@example.com", uid); err != nil {
		t.Fatalf("set email: %v", err)
	}
	const key = "digest:lens-a:2026-07-27"
	enqueue(t, w, uid, "digest", key, "Design digest", `{}`)

	if err := w.Work(ctx, &river.Job[FlushNotificationsArgs]{Args: FlushNotificationsArgs{UserID: uid}}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(email.Sent) != 1 {
		t.Errorf("email sends = %d, want 1 (email must still go out when push fails)", len(email.Sent))
	}
	sentAt, _, _, _ := readNotification(t, w, uid, key)
	if !sentAt.Valid {
		t.Errorf("sent_at not set; a partial success must still stamp the row")
	}
}

// The regression guard for the critical bug where a total failure was
// recorded as a successful send: MarkNotificationsSent unconditionally wiped
// the last_error MarkNotificationsFailed had just written, and the row was
// stamped anyway.
func TestFlushAllChannelsFailingLeavesRowPending(t *testing.T) {
	w, uid, push, _ := flushFixture(t)
	ctx := context.Background()
	push.Err = errors.New("expo: transport down")
	const key = "digest:lens-a:2026-07-27"
	enqueue(t, w, uid, "digest", key, "Design digest", `{}`)

	if err := w.Work(ctx, &river.Job[FlushNotificationsArgs]{Args: FlushNotificationsArgs{UserID: uid}}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	sentAt, _, lastErr, _ := readNotification(t, w, uid, key)
	if sentAt.Valid {
		t.Errorf("sent_at set; a row where every channel failed must not be stamped")
	}
	if lastErr == "" {
		t.Errorf("last_error empty; want the failure recorded as the final write")
	}
	due, err := w.Store.Queries.ListDueNotifications(ctx, uid)
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	if len(due) != 1 {
		t.Errorf("pending = %d, want 1 (still retryable on the next scan)", len(due))
	}
}

// Noop mode (both senders are notify.NewNoop(), so Router.Live never reports
// anything live) must still drain the outbox — that is the "noop keeps the
// app fully functional" guarantee, and the human ruling this substrate
// follows verbatim. main.go currently wires exactly this configuration as
// the Task 10 stopgap, so every deployment takes this path until real
// channels are configured; a regression here would silently stop the whole
// outbox from ever draining.
func TestFlushStampsInNoopMode(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := uuid.New()
	if err := s.Queries.EnsureUser(ctx, uid); err != nil {
		t.Fatalf("ensure user: %v", err)
	}
	w := &FlushNotificationsWorker{
		Store: s,
		Deps:  NotifyDeps{Router: notify.NewRouter(notify.NewNoop(), notify.NewNoop())},
	}
	const key = "digest:lens-a:2026-07-27"
	enqueue(t, w, uid, "digest", key, "Design digest", `{}`)

	if err := w.Work(ctx, &river.Job[FlushNotificationsArgs]{Args: FlushNotificationsArgs{UserID: uid}}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	sentAt, _, _, _ := readNotification(t, w, uid, key)
	if !sentAt.Valid {
		t.Errorf("sent_at not set; noop mode must still drain the outbox")
	}
}

// The no-target guard must consider only the channels enabled for this
// category. A user with email-only digest notifications and no e-mail
// address must be deferred even though they still have a live push device —
// the device is irrelevant because push isn't enabled for this category, so
// checking "any device OR any email" (rather than per-channel) would let
// this case slip past the guard, claim the row every scan, and silently
// abandon it after notifyMaxAttempts with last_error never set.
func TestFlushDefersWhenOnlyEnabledChannelHasNoDestination(t *testing.T) {
	w, uid, push, email := flushFixture(t)
	ctx := context.Background()
	if err := w.Store.Queries.UpsertUserSetting(ctx, db.UpsertUserSettingParams{
		UserID: uid, Key: notify.KeyDigest, Value: "email",
	}); err != nil {
		t.Fatalf("set pref: %v", err)
	}
	const key = "digest:lens-a:2026-07-27"
	enqueue(t, w, uid, "digest", key, "Design digest", `{}`)

	if err := w.Work(ctx, &river.Job[FlushNotificationsArgs]{Args: FlushNotificationsArgs{UserID: uid}}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(push.Sent)+len(email.Sent) != 0 {
		t.Errorf("delivered %d messages with no destination for the only enabled channel", len(push.Sent)+len(email.Sent))
	}
	sentAt, deliverAfter, _, attempts := readNotification(t, w, uid, key)
	if sentAt.Valid {
		t.Errorf("sent_at set; row must not be stamped when its only enabled channel has no destination")
	}
	if attempts != 0 {
		t.Errorf("attempts = %d, want 0 (must defer before claiming, not burn an attempt)", attempts)
	}
	if !deliverAfter.Time.After(time.Now()) {
		t.Errorf("deliver_after = %v, want pushed into the future", deliverAfter.Time)
	}
}

// TestFlushChannelEnabledButServerWiredToNoopIsStamped is the regression test
// for the whole-branch review's C1 finding: NOTIFY_CHANNELS naming only one
// channel (e.g. "expo") leaves the user's *other* enabled channel backed by
// notify.NewNoop(), not by a real sender. Before the fix, NotifyDeps.Configured
// was a single global bool meaning "some real channel is wired anywhere", so a
// user with notify.digest=email and an address on file passed the no-target
// guard (their email is non-empty), got routed to the noop email sender,
// received a silent (nil, nil) back, and was left pending forever — never
// stamped, never given a last_error, retried identically until
// notifyMaxAttempts and then pruned. This asserts the row is stamped instead,
// with noop semantics applied per channel rather than globally.
func TestFlushChannelEnabledButServerWiredToNoopIsStamped(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := uuid.New()
	if err := s.Queries.EnsureUser(ctx, uid); err != nil {
		t.Fatalf("ensure user: %v", err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE users SET email = $1 WHERE id = $2`, "user@example.com", uid); err != nil {
		t.Fatalf("set email: %v", err)
	}
	if err := s.Queries.UpsertUserSetting(ctx, db.UpsertUserSettingParams{
		UserID: uid, Key: notify.KeyDigest, Value: "email",
	}); err != nil {
		t.Fatalf("set pref: %v", err)
	}

	// Mirrors NOTIFY_CHANNELS=expo in production: push is a real sender, email
	// stays the real notify.NewNoop() — not a test double standing in for
	// "disabled" — because that is exactly the case Router.Enabled() collapses
	// away into a single global bool.
	push := notify.NewFake()
	push.ChannelName = "expo"
	w := &FlushNotificationsWorker{
		Store: s,
		Deps:  NotifyDeps{Router: notify.NewRouter(push, notify.NewNoop())},
	}
	const key = "digest:lens-a:2026-07-27"
	enqueue(t, w, uid, "digest", key, "Design digest", `{}`)

	if err := w.Work(ctx, &river.Job[FlushNotificationsArgs]{Args: FlushNotificationsArgs{UserID: uid}}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	sentAt, _, _, attempts := readNotification(t, w, uid, key)
	if !sentAt.Valid {
		t.Errorf("sent_at not set; a channel the user enabled but the server wired to noop must be stamped, not left pending forever")
	}
	if attempts == 0 {
		t.Errorf("attempts = 0; row was deferred as if it had no destination at all, rather than claimed and stamped under noop semantics")
	}
}

// countFlushJobs counts enqueued flush_notifications River jobs, for
// asserting the scan's fan-out without a worker actually running them.
func countFlushJobs(t *testing.T, s *store.Store) int {
	t.Helper()
	var n int
	if err := s.Pool.QueryRow(context.Background(), `SELECT count(*) FROM river_job WHERE kind = $1`, FlushNotificationsArgs{}.Kind()).Scan(&n); err != nil {
		t.Fatalf("counting flush jobs: %v", err)
	}
	return n
}

// The scan is the fan-out that turns "users with due rows" into one
// flush_notifications job per user — untested until now despite CLAUDE.md's
// per-job idempotency rule.
func TestScanNotificationsEnqueuesOneFlushPerDueUser(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.Pool.Exec(ctx, `TRUNCATE river_job CASCADE`); err != nil {
		t.Fatalf("truncate river_job: %v", err)
	}
	rc, err := NewRiverClient(s.Pool, nil, nil, KindleDeps{}, NotifyDeps{}, nil, reelmedia.ModeThumbnail, nil, false)
	if err != nil {
		t.Fatalf("river client: %v", err)
	}

	uidA, uidB := uuid.New(), uuid.New()
	for _, uid := range []uuid.UUID{uidA, uidB} {
		if err := s.Queries.EnsureUser(ctx, uid); err != nil {
			t.Fatalf("ensure user: %v", err)
		}
		if err := s.Queries.EnqueueNotification(ctx, db.EnqueueNotificationParams{
			UserID: uid, Category: "digest", DedupeKey: "d1", Title: "t", Body: "", Data: []byte(`{}`),
		}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}

	w := &ScanNotificationsWorker{Store: s, River: rc}
	if err := w.Work(ctx, &river.Job[ScanNotificationsArgs]{}); err != nil {
		t.Fatalf("first Work: %v", err)
	}
	if got := countFlushJobs(t, s); got != 2 {
		t.Fatalf("flush jobs enqueued = %d, want 2 (one per due user)", got)
	}

	// The scan itself has no "already enqueued" guard — that idempotency
	// lives downstream in the flush, which stamps rows so a re-enqueued flush
	// finds nothing due and no-ops. Running the scan again must still behave
	// sanely: no error, and it keeps enqueueing for whichever users are still
	// due (both are, since nothing has been flushed yet).
	if err := w.Work(ctx, &river.Job[ScanNotificationsArgs]{}); err != nil {
		t.Fatalf("second Work: %v", err)
	}
	if got := countFlushJobs(t, s); got != 4 {
		t.Fatalf("flush jobs enqueued after second scan = %d, want 4", got)
	}
}
