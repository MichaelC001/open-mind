package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"

	"github.com/rohithgilla12/openmind/api/internal/ai"
	"github.com/rohithgilla12/openmind/api/internal/store"
	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

// digestGrace widens the new-items filter below last_digest_at by an hour.
// An item's created_at is its save time, but async enrichment can make it
// enter a Lens's rule (tags, card type, body) only later — a strict
// created_at > last_digest_at cut would drop items that were still being
// enriched when the previous digest went out. The grace catches those
// late-enriched items at the cost of a rare duplicate across consecutive
// digests (an item created in the hour before a stamp may appear twice).
const digestGrace = time.Hour

// ScanDigestsArgs is the River job payload for the periodic digest scan. It
// carries no state: the worker lists every Lens with a non-empty
// digest_schedule itself and decides per-lens whether it is due.
type ScanDigestsArgs struct{}

// Kind identifies the job type in River.
func (ScanDigestsArgs) Kind() string { return "scan_digests" }

// ScanDigestsWorker scans every scheduled Lens and, for those due, enqueues a
// send_kindle job carrying only the items newly matched since the last
// digest. It runs on the periodic schedule registered in NewRiverClient.
type ScanDigestsWorker struct {
	river.WorkerDefaults[ScanDigestsArgs]
	Store    *store.Store
	Provider ai.Provider
	River    *river.Client[pgx.Tx]
}

// Work lists every scheduled Lens and processes the ones that are due. A
// single broken Lens (a bad rule, a transient query error) is logged and
// skipped rather than failing the whole scan — one misconfigured Lens must
// never block digests for every other user.
func (w *ScanDigestsWorker) Work(ctx context.Context, _ *river.Job[ScanDigestsArgs]) error {
	lenses, err := w.Store.Queries.ListDueDigestLenses(ctx)
	if err != nil {
		return fmt.Errorf("scan_digests: listing scheduled lenses: %w", err)
	}

	now := time.Now()
	for _, lens := range lenses {
		if !digestDue(lens.DigestSchedule, lens.LastDigestAt, now) {
			continue
		}
		if err := w.processLens(ctx, lens); err != nil {
			slog.Error("scan_digests: processing lens", "lens_id", lens.ID, "err", err)
			continue
		}
	}
	return nil
}

// processLens materialises lens's matching items, filters to only those
// created since the last digest (when there was one), and — if any remain —
// enqueues a send_kindle job carrying just their IDs and stamps last_digest_at.
// An empty result set is not an error: it just means nothing new to send, and
// the stamp is deliberately left untouched so a lens that stays quiet doesn't
// drift its schedule.
func (w *ScanDigestsWorker) processLens(ctx context.Context, lens db.Lense) error {
	items, err := lensItems(ctx, w.Store, w.Provider, lens.UserID, lens)
	if err != nil {
		return fmt.Errorf("running lens rule: %w", err)
	}

	ids := make([]uuid.UUID, 0, kindleDigestCap)
	for _, item := range items {
		if item.Body == "" {
			continue
		}
		if lens.LastDigestAt.Valid && !item.CreatedAt.Time.After(lens.LastDigestAt.Time.Add(-digestGrace)) {
			continue
		}
		ids = append(ids, item.ID)
		if len(ids) >= kindleDigestCap {
			break
		}
	}
	if len(ids) == 0 {
		return nil
	}

	// Enqueue and stamp in one transaction: a crash between a committed
	// enqueue and the stamp would re-send the same digest on the next scan,
	// so both land together or not at all (a rollback means the next scan
	// retries cleanly).
	tx, err := w.Store.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning tx: %w", err)
	}
	defer tx.Rollback(ctx)

	lensID := lens.ID
	if _, err := w.River.InsertTx(ctx, tx, SendKindleArgs{UserID: lens.UserID, LensID: &lensID, ItemIDs: ids}, nil); err != nil {
		return fmt.Errorf("enqueueing send_kindle: %w", err)
	}
	if _, err := w.Store.Queries.WithTx(tx).StampLensDigest(ctx, db.StampLensDigestParams{UserID: lens.UserID, ID: lens.ID}); err != nil {
		return fmt.Errorf("stamping last_digest_at: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing digest tx: %w", err)
	}
	return nil
}

// digestDue reports whether a Lens with the given digest_schedule and
// last_digest_at is due for a digest at now. An empty or unrecognised
// schedule is never due. "daily" is due if there has never been a digest, or
// the last one was at least 20h ago (a little under 24h so an hourly scan
// tick doesn't slip a day). "weekly:<0-6>" (Go time.Weekday numbering, UTC)
// is due only on the matching day of week, and then only if there has never
// been a digest or the last one was at least 6 days ago (so a single UTC day
// doesn't fire the job twice across weeks, while still tolerating the scan
// running more than once on the due day).
func digestDue(schedule string, last pgtype.Timestamptz, now time.Time) bool {
	switch {
	case schedule == "daily":
		return !last.Valid || now.Sub(last.Time) >= 20*time.Hour
	case strings.HasPrefix(schedule, "weekly:"):
		dayStr := strings.TrimPrefix(schedule, "weekly:")
		day, err := strconv.Atoi(dayStr)
		if err != nil || day < 0 || day > 6 {
			return false
		}
		if int(now.UTC().Weekday()) != day {
			return false
		}
		return !last.Valid || now.Sub(last.Time) >= 6*24*time.Hour
	default:
		return false
	}
}
