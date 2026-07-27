package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"

	"github.com/rohithgilla12/openmind/api/internal/notify"
	"github.com/rohithgilla12/openmind/api/internal/store"
	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

// notifyQueue keeps notification work off the default queue, so a burst of
// enrichment jobs cannot delay delivery.
const notifyQueue = "notifications"

// notifyScanInterval is how often due users are looked for. One minute keeps
// "immediate" categories feeling immediate while staying a trivially cheap
// indexed query.
const notifyScanInterval = time.Minute

// receiptInterval is how often Expo receipts are reconciled. Expo needs a few
// minutes before receipts are meaningful, so this is deliberately slow.
const receiptInterval = 15 * time.Minute

// pruneInterval is how often old notification rows are deleted.
const pruneInterval = 24 * time.Hour

// notifyMaxAttempts caps River retries for a flush job. It matches the
// attempts < 3 predicate in the due-row queries so a row and its job give up
// together.
const notifyMaxAttempts = 3

// noTargetRetry is how long a notification is deferred when the router is
// configured but none of the channels enabled for this notification have a
// destination — e.g. a new mobile install before its Expo token registers,
// or a user with email-only digests and no e-mail address on file. It is
// deliberately much longer than notifyScanInterval: claiming the row on every
// one-minute scan would exhaust notifyMaxAttempts within three minutes and
// lose exactly the onboarding notifications this path exists to protect. An
// hour is still short enough that the backlog clears promptly once a device
// or address registers.
const noTargetRetry = time.Hour

// ReceiptChecker resolves Expo ticket IDs to their terminal error codes. It is
// an interface rather than *notify.Expo so tests can substitute a stub.
type ReceiptChecker interface {
	Receipts(ctx context.Context, ticketIDs []string) (map[string]string, error)
}

// NotifyDeps carries the delivery router into the workers. Configured is
// false when NOTIFY_CHANNELS is unset, in which case Router wraps two noop
// senders: Deliver returns no results for anything, and deliverOne's
// Configured branch stamps the row anyway — the outbox must still drain on
// an install that never delivers, rather than piling up behind the pending
// partial index forever. Receipts is nil when Expo is not configured, in
// which case the receipt job is a no-op.
type NotifyDeps struct {
	Router     *notify.Router
	Receipts   ReceiptChecker
	Configured bool
}

// ScanNotificationsArgs is the periodic fan-out job. It carries no state.
type ScanNotificationsArgs struct{}

// Kind identifies the job type in River.
func (ScanNotificationsArgs) Kind() string { return "scan_notifications" }

// InsertOpts pins the job to the notifications queue.
func (ScanNotificationsArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: notifyQueue}
}

// ScanNotificationsWorker finds users with due outbox rows and enqueues one
// flush per user. Per-user jobs (rather than one global loop) give per-user
// retry isolation and stop one slow Expo call head-of-line blocking everyone.
type ScanNotificationsWorker struct {
	river.WorkerDefaults[ScanNotificationsArgs]
	Store *store.Store
	River *river.Client[pgx.Tx]
}

// Work enqueues a flush per due user. A single user's enqueue failing is
// logged and skipped rather than failing the whole scan: the next tick retries
// them, and one bad user must not stall everyone else's notifications.
func (w *ScanNotificationsWorker) Work(ctx context.Context, _ *river.Job[ScanNotificationsArgs]) error {
	users, err := w.Store.Queries.ListUsersWithDueNotifications(ctx)
	if err != nil {
		return fmt.Errorf("listing users with due notifications: %w", err)
	}
	for _, uid := range users {
		if _, err := w.River.Insert(ctx, FlushNotificationsArgs{UserID: uid}, &river.InsertOpts{
			Queue:       notifyQueue,
			MaxAttempts: notifyMaxAttempts,
		}); err != nil {
			slog.Error("scan_notifications: enqueueing flush", "user_id", uid, "err", err)
		}
	}
	return nil
}

// FlushNotificationsArgs delivers one user's due notifications.
type FlushNotificationsArgs struct {
	UserID uuid.UUID `json:"user_id"`
}

// Kind identifies the job type in River.
func (FlushNotificationsArgs) Kind() string { return "flush_notifications" }

// InsertOpts pins the job to the notifications queue.
func (FlushNotificationsArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: notifyQueue}
}

// FlushNotificationsWorker applies preferences, coalescing, quiet hours, and
// the daily cap to one user's due rows, then delivers what survives.
type FlushNotificationsWorker struct {
	river.WorkerDefaults[FlushNotificationsArgs]
	Store *store.Store
	Deps  NotifyDeps
}

// Work is the delivery pipeline for one user.
//
// Delivery is at-least-once. The send is an HTTP call and cannot sit inside
// the transaction that stamps sent_at, so rows are claimed (attempts+1), sent,
// then stamped. A crash between send and stamp can re-send once; attempts
// reaching notifyMaxAttempts gives up with last_error set. Repeating a
// notification is a smaller harm than losing one.
func (w *FlushNotificationsWorker) Work(ctx context.Context, job *river.Job[FlushNotificationsArgs]) error {
	uid := job.Args.UserID

	prefs, err := w.loadPrefs(ctx, uid)
	if err != nil {
		return err
	}

	due, err := w.Store.Queries.ListDueNotifications(ctx, uid)
	if err != nil {
		return fmt.Errorf("listing due notifications: %w", err)
	}
	if len(due) == 0 {
		return nil
	}

	// Quiet hours defer rather than drop: bump every due row past the window
	// and let a later scan pick them up.
	now := time.Now()
	if next := notify.NextDeliverable(now, prefs); next.After(now) {
		ids := make([]uuid.UUID, len(due))
		for i, row := range due {
			ids[i] = row.ID
		}
		if err := w.Store.Queries.DeferNotifications(ctx, db.DeferNotificationsParams{
			UserID: uid, DeliverAfter: pgTimestamp(next), Ids: ids,
		}); err != nil {
			return fmt.Errorf("deferring for quiet hours: %w", err)
		}
		return nil
	}

	sentToday, err := w.Store.Queries.CountDeliveriesSince(ctx, db.CountDeliveriesSinceParams{
		UserID: uid, SentAt: pgTimestamp(startOfDay(now, prefs.Location)),
	})
	if err != nil {
		return fmt.Errorf("counting today's deliveries: %w", err)
	}

	target, err := w.resolveTarget(ctx, uid)
	if err != nil {
		return err
	}

	// budget is in units of outbox rows (CountDeliveriesSince counts distinct
	// notification_ids, excluding lifecycle), so it is spent by
	// len(n.SourceIDs) below, not by one unit per coalesced message — a
	// three-row feed_river roll-up must cost 3, the same as three separate
	// digest sends would.
	budget := prefs.DailyCap - int(sentToday)
	for _, cat := range []notify.Category{notify.CategoryLifecycle, notify.CategoryDigest, notify.CategoryFeedRiver} {
		pending := rowsFor(uid, cat, due)
		if len(pending) == 0 {
			continue
		}
		ch := prefs.For(cat)
		if !ch.Push && !ch.Email {
			// The user has this category switched off. Stamp the rows so they
			// do not accumulate forever in the pending index.
			if err := w.stamp(ctx, uid, idsOf(pending)); err != nil {
				return err
			}
			continue
		}

		// The cap is re-checked per coalesced message, not once per category:
		// checking only at category entry let a single row of remaining
		// budget wave through every pending message in the category (e.g.
		// budget=1 with 20 pending digests would have sent all 20). lifecycle
		// bypasses the check entirely — a "we gave up on your save" swallowed
		// because feed river spent the budget is the one failure mode that
		// makes the whole feature untrustworthy.
		for _, n := range notify.Coalesce(cat, pending) {
			if cat != notify.CategoryLifecycle && budget <= 0 {
				if err := w.Store.Queries.DeferNotifications(ctx, db.DeferNotificationsParams{
					UserID: uid, DeliverAfter: pgTimestamp(startOfDay(now, prefs.Location).AddDate(0, 0, 1)), Ids: n.SourceIDs,
				}); err != nil {
					return fmt.Errorf("deferring over-cap notifications: %w", err)
				}
				continue
			}
			attempted, err := w.deliverOne(ctx, uid, n, ch, target)
			if err != nil {
				return err
			}
			if attempted && cat != notify.CategoryLifecycle {
				budget -= len(n.SourceIDs)
			}
		}
	}
	return nil
}

// deliverOne claims, delivers, records the ledger, and then either stamps or
// fails one message. It returns whether a delivery was actually attempted
// (claimed and sent), as opposed to deferred without being touched — the
// caller only spends daily-cap budget on an attempt.
//
// Every row leaves this function on exactly one of three paths:
//   - noop mode (Configured false): stamped unconditionally, since nothing is
//     wired up to attempt delivery and the outbox must still drain.
//   - no destination for any enabled channel (Configured true): deferred by
//     noTargetRetry before claiming, so it can't burn an attempt while
//     waiting for a device or address to register; collected by
//     PruneNotifications' third clause after 30 days if that never happens.
//   - configured with a destination: claimed, delivered, and then either
//     stamped (at least one channel succeeded — re-attempting would
//     duplicate the message to whichever destination already received it,
//     and repeating a notification is a smaller harm than losing one) or
//     left pending with last_error set as the final write (every channel
//     failed), so a later scan retries it until attempts reaches
//     notifyMaxAttempts and the abandoned-row prune clause collects it.
//     MarkNotificationsFailed must be the last write on that path: stamping
//     afterwards (MarkNotificationsSent clears last_error) would erase the
//     failure and make the retry ladder unreachable.
func (w *FlushNotificationsWorker) deliverOne(ctx context.Context, uid uuid.UUID, n notify.Notification, ch notify.Channels, target notify.Target) (bool, error) {
	// The guard is evaluated per enabled channel, not on the target as a
	// whole: a user with only the e-mail channel enabled for this category
	// and no e-mail address on file must defer even if they have a live push
	// device, because push isn't enabled here and can't help. Symmetrically
	// for push-only with no devices but an e-mail on file. Checking the whole
	// target (any device OR any email) would let either of those slip past
	// the guard, claim the row, get a silent (nil, nil) back from the one
	// sender that's actually enabled, and fall through to "no destination
	// failed" — stamping it as delivered and losing it for good.
	hasDestination := (ch.Push && len(target.Devices) > 0) || (ch.Email && target.Email != "")
	if w.Deps.Configured && !hasDestination {
		// Defer instead of claiming: claiming here would burn an attempt on
		// every one-minute scan and exhaust notifyMaxAttempts within three
		// minutes — exactly the onboarding notifications this path protects.
		if err := w.Store.Queries.DeferNotifications(ctx, db.DeferNotificationsParams{
			UserID: uid, DeliverAfter: pgTimestamp(time.Now().Add(noTargetRetry)), Ids: n.SourceIDs,
		}); err != nil {
			return false, fmt.Errorf("deferring for no target: %w", err)
		}
		slog.Debug("flush_notifications: no destination for any enabled channel, deferring", "user_id", uid, "notification_id", n.ID)
		return false, nil
	}

	if err := w.Store.Queries.ClaimNotifications(ctx, db.ClaimNotificationsParams{UserID: uid, Ids: n.SourceIDs}); err != nil {
		return false, fmt.Errorf("claiming notifications: %w", err)
	}

	results := w.Deps.Router.Deliver(ctx, n, ch, target)

	anyOK := false
	for _, res := range results {
		errText := ""
		if res.Err != nil {
			errText = res.Err.Error()
		} else if res.OK {
			anyOK = true
		}
		// The ledger is per source row so the cap count and the audit trail
		// both stay accurate for a coalesced message.
		for _, srcID := range n.SourceIDs {
			if err := w.Store.Queries.RecordDelivery(ctx, db.RecordDeliveryParams{
				UserID: uid, NotificationID: srcID, Channel: res.Channel,
				Token: res.Token, TicketID: res.TicketID, Ok: res.OK, Error: errText,
			}); err != nil {
				return true, fmt.Errorf("recording delivery: %w", err)
			}
		}
	}

	if anyOK {
		return true, w.stamp(ctx, uid, n.SourceIDs)
	}
	if len(results) == 0 {
		if !w.Deps.Configured {
			// Noop mode: nothing is wired up to attempt delivery at all (both
			// senders are notify.NewNoop()), so results is always empty here.
			// The row must still be stamped — draining the outbox on an
			// install with nothing configured is the "noop keeps the app
			// fully functional" guarantee, and leaving it pending would block
			// re-enqueue of the same dedupe key behind the pending partial
			// index indefinitely.
			return true, w.stamp(ctx, uid, n.SourceIDs)
		}
		// Configured is true, so the no-target guard above already confirmed
		// at least one enabled channel has a destination — this branch is
		// only reached if the router still produced nothing to record, e.g.
		// a channel enabled with no sender wired into the Router at all.
		// Leave the row pending without asserting an error message that
		// didn't happen.
		slog.Warn("flush_notifications: no delivery attempted despite a resolvable target", "user_id", uid, "notification_id", n.ID)
		return true, nil
	}
	if err := w.Store.Queries.MarkNotificationsFailed(ctx, db.MarkNotificationsFailedParams{
		UserID: uid, LastError: "all channels failed", Ids: n.SourceIDs,
	}); err != nil {
		return true, fmt.Errorf("marking failed: %w", err)
	}
	return true, nil
}

// stamp marks rows delivered.
func (w *FlushNotificationsWorker) stamp(ctx context.Context, uid uuid.UUID, ids []uuid.UUID) error {
	if err := w.Store.Queries.MarkNotificationsSent(ctx, db.MarkNotificationsSentParams{UserID: uid, Ids: ids}); err != nil {
		return fmt.Errorf("marking notifications sent: %w", err)
	}
	return nil
}

// loadPrefs reads the caller's notify.* settings.
func (w *FlushNotificationsWorker) loadPrefs(ctx context.Context, uid uuid.UUID) (notify.Prefs, error) {
	rows, err := w.Store.Queries.ListUserSettings(ctx, uid)
	if err != nil {
		return notify.Prefs{}, fmt.Errorf("listing user settings: %w", err)
	}
	kv := make(map[string]string, len(rows))
	for _, row := range rows {
		kv[row.Key] = row.Value
	}
	return notify.ParsePrefs(kv), nil
}

// resolveTarget collects the user's live devices and e-mail address.
func (w *FlushNotificationsWorker) resolveTarget(ctx context.Context, uid uuid.UUID) (notify.Target, error) {
	devices, err := w.Store.Queries.ListPushDevices(ctx, uid)
	if err != nil {
		return notify.Target{}, fmt.Errorf("listing push devices: %w", err)
	}
	t := notify.Target{Devices: make([]notify.Device, len(devices))}
	for i, d := range devices {
		t.Devices[i] = notify.Device{Token: d.Token, Platform: d.Platform}
	}
	// A missing or empty account e-mail is not an error: the e-mail channel
	// simply has no target, and push still goes.
	if email, err := w.Store.Queries.GetUserEmail(ctx, uid); err == nil {
		t.Email = email
	}
	return t, nil
}

// rowsFor selects the due rows belonging to one category and maps them onto
// notify.Notification values.
func rowsFor(uid uuid.UUID, cat notify.Category, due []db.ListDueNotificationsRow) []notify.Notification {
	var out []notify.Notification
	for _, row := range due {
		if notify.Category(row.Category) != cat {
			continue
		}
		data := map[string]any{}
		if len(row.Data) > 0 {
			if err := json.Unmarshal(row.Data, &data); err != nil {
				slog.Warn("flush_notifications: unreadable data payload", "notification_id", row.ID, "err", err)
				data = map[string]any{}
			}
		}
		out = append(out, notify.Notification{
			ID: row.ID, UserID: uid, Category: cat, DedupeKey: row.DedupeKey,
			Title: row.Title, Body: row.Body, Data: data, SourceIDs: []uuid.UUID{row.ID},
		})
	}
	return out
}

// idsOf flattens the source row IDs of a set of notifications.
func idsOf(ns []notify.Notification) []uuid.UUID {
	var out []uuid.UUID
	for _, n := range ns {
		out = append(out, n.SourceIDs...)
	}
	return out
}

// startOfDay returns local midnight for t in loc — the boundary the daily cap
// counts from, so the cap resets when the user's day does, not at UTC midnight.
func startOfDay(t time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
}

// pgTimestamp wraps a time for pgx's timestamptz parameters.
func pgTimestamp(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// CheckReceiptsArgs is the periodic Expo receipt reconciliation job.
type CheckReceiptsArgs struct{}

// Kind identifies the job type in River.
func (CheckReceiptsArgs) Kind() string { return "check_receipts" }

// InsertOpts pins the job to the notifications queue.
func (CheckReceiptsArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: notifyQueue}
}

// CheckReceiptsWorker reconciles Expo delivery receipts. Expo reports terminal
// failures — most importantly DeviceNotRegistered — only in the receipts,
// never in the send response, so this is the only place a dead token is
// discovered.
//
// Overlapping runs re-check the same tickets, which is harmless: marking a
// device failed twice is idempotent. That is why no "checked" column exists —
// the bounded one-hour lookback is sufficient.
type CheckReceiptsWorker struct {
	river.WorkerDefaults[CheckReceiptsArgs]
	Store *store.Store
	Deps  NotifyDeps
}

// Work fetches receipts for recently sent tickets and retires dead devices.
func (w *CheckReceiptsWorker) Work(ctx context.Context, _ *river.Job[CheckReceiptsArgs]) error {
	if w.Deps.Receipts == nil {
		return nil
	}
	rows, err := w.Store.Queries.ListRecentTickets(ctx)
	if err != nil {
		return fmt.Errorf("listing recent tickets: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	ids := make([]string, 0, len(rows))
	tokenFor := make(map[string]string, len(rows))
	for _, row := range rows {
		ids = append(ids, row.TicketID)
		tokenFor[row.TicketID] = row.Token
	}

	codes, err := w.Deps.Receipts.Receipts(ctx, ids)
	if err != nil {
		return fmt.Errorf("fetching receipts: %w", err)
	}
	for id, code := range codes {
		if code != "DeviceNotRegistered" {
			continue
		}
		token := tokenFor[id]
		if token == "" {
			continue
		}
		if err := w.Store.Queries.MarkPushDeviceFailed(ctx, token); err != nil {
			return fmt.Errorf("marking device failed: %w", err)
		}
		slog.Info("check_receipts: retired unregistered device", "ticket_id", id)
	}
	return nil
}

// PruneNotificationsArgs is the periodic retention job.
type PruneNotificationsArgs struct{}

// Kind identifies the job type in River.
func (PruneNotificationsArgs) Kind() string { return "prune_notifications" }

// InsertOpts pins the job to the notifications queue.
func (PruneNotificationsArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: notifyQueue}
}

// PruneNotificationsWorker enforces retention. Without it the outbox and its
// ledger are the fastest-growing tables in the application, and abandoned rows
// would occupy the pending partial index indefinitely.
type PruneNotificationsWorker struct {
	river.WorkerDefaults[PruneNotificationsArgs]
	Store *store.Store
}

// Work deletes aged-out and abandoned notification rows; deliveries cascade.
func (w *PruneNotificationsWorker) Work(ctx context.Context, _ *river.Job[PruneNotificationsArgs]) error {
	n, err := w.Store.Queries.PruneNotifications(ctx)
	if err != nil {
		return fmt.Errorf("pruning notifications: %w", err)
	}
	if n > 0 {
		slog.Info("prune_notifications: deleted rows", "count", n)
	}
	return nil
}
