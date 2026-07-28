package jobs_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/rohithgilla12/openmind/api/internal/ai"
	"github.com/rohithgilla12/openmind/api/internal/enrich"
	"github.com/rohithgilla12/openmind/api/internal/jobs"
	"github.com/rohithgilla12/openmind/api/internal/store"
	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

var enrichLifecycleTestUser = uuid.MustParse("00000000-0000-0000-0000-0000000000dd")

// newEnrichLifecycleTestStore connects to the test Postgres, migrates,
// truncates the tables this suite touches, and provisions the test user.
func newEnrichLifecycleTestStore(t *testing.T) *store.Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = "postgres://openmind:openmind@localhost:5433/openmind_test"
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE items, item_embeddings, notifications CASCADE`); err != nil {
		t.Fatalf("truncating: %v", err)
	}
	s := store.New(pool)
	if err := s.Queries.EnsureUser(ctx, enrichLifecycleTestUser); err != nil {
		t.Fatalf("ensure user: %v", err)
	}
	return s
}

// runEnrichWorker builds an EnrichWorker over pipeline and runs it as if job
// were at the given attempt/maxAttempts — River exposes these on the embedded
// JobRow, which is what EnrichWorker.Work reads to decide terminality.
func runEnrichWorker(t *testing.T, pipeline *enrich.Pipeline, userID, itemID uuid.UUID, attempt, maxAttempts int) error {
	t.Helper()
	return (&jobs.EnrichWorker{Pipeline: pipeline}).Work(context.Background(), &river.Job[jobs.EnrichArgs]{
		JobRow: &rivertype.JobRow{Attempt: attempt, MaxAttempts: maxAttempts},
		Args:   jobs.EnrichArgs{UserID: userID, ItemID: itemID},
	})
}

// TestEnrichWorkerNotifiesOnTerminalFailure asserts that when River has
// exhausted retries (Attempt >= MaxAttempts) on a failing enrichment, exactly
// one lifecycle notification lands in the outbox, keyed by the item id.
func TestEnrichWorkerNotifiesOnTerminalFailure(t *testing.T) {
	s := newEnrichLifecycleTestStore(t)
	ctx := context.Background()
	pipeline := &enrich.Pipeline{Store: s, AI: ai.NewNoop()}

	// An item id with no backing row makes Pipeline.Run fail deterministically
	// at the GetItem load, without needing to fake out the extractor.
	missingItemID := uuid.New()

	err := runEnrichWorker(t, pipeline, enrichLifecycleTestUser, missingItemID, 3, 3)
	if err == nil {
		t.Fatal("Work err = nil, want the underlying pipeline error")
	}

	due, err := s.Queries.ListDueNotifications(ctx, enrichLifecycleTestUser)
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("pending notifications = %d, want 1", len(due))
	}
	if due[0].Category != "lifecycle" {
		t.Errorf("category = %q, want lifecycle", due[0].Category)
	}
	wantKey := fmt.Sprintf("lifecycle:enrich-failed:%s", missingItemID)
	if due[0].DedupeKey != wantKey {
		t.Errorf("dedupe_key = %q, want %q", due[0].DedupeKey, wantKey)
	}
}

// TestEnrichWorkerSilentOnIntermediateFailure asserts that a failing
// enrichment on an attempt River will still retry (Attempt < MaxAttempts)
// notifies no one — intermediate retries are expected and must stay invisible.
func TestEnrichWorkerSilentOnIntermediateFailure(t *testing.T) {
	s := newEnrichLifecycleTestStore(t)
	ctx := context.Background()
	pipeline := &enrich.Pipeline{Store: s, AI: ai.NewNoop()}

	missingItemID := uuid.New()

	err := runEnrichWorker(t, pipeline, enrichLifecycleTestUser, missingItemID, 1, 3)
	if err == nil {
		t.Fatal("Work err = nil, want the underlying pipeline error")
	}

	due, err := s.Queries.ListDueNotifications(ctx, enrichLifecycleTestUser)
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("pending notifications = %d, want 0 (not the final attempt)", len(due))
	}
}

// TestEnrichWorkerSilentOnSuccess asserts that a successful enrichment never
// enqueues anything — the item appearing in the Library is the only signal a
// user gets, by design.
func TestEnrichWorkerSilentOnSuccess(t *testing.T) {
	s := newEnrichLifecycleTestStore(t)
	ctx := context.Background()
	pipeline := &enrich.Pipeline{Store: s, AI: ai.NewNoop()}

	item, err := s.Queries.CreateItem(ctx, db.CreateItemParams{UserID: enrichLifecycleTestUser, Body: "hello world, a note"})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	if err := runEnrichWorker(t, pipeline, enrichLifecycleTestUser, item.ID, 1, 3); err != nil {
		t.Fatalf("Work: %v", err)
	}

	due, err := s.Queries.ListDueNotifications(ctx, enrichLifecycleTestUser)
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("pending notifications = %d, want 0 (successful enrichment stays silent)", len(due))
	}
}
