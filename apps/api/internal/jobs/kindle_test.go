package jobs_test

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/rohithgilla12/openmind/api/internal/ai"
	"github.com/rohithgilla12/openmind/api/internal/jobs"
	"github.com/rohithgilla12/openmind/api/internal/mailer"
	"github.com/rohithgilla12/openmind/api/internal/store"
	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

var kindleTestUser = uuid.MustParse("00000000-0000-0000-0000-0000000000aa")

// newKindleTestStore connects to the test Postgres, migrates, truncates the
// tables this suite touches, and provisions the test user.
func newKindleTestStore(t *testing.T) *store.Store {
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
	if _, err := pool.Exec(ctx, `TRUNCATE items, item_embeddings, lenses, user_settings CASCADE`); err != nil {
		t.Fatalf("truncating: %v", err)
	}
	s := store.New(pool)
	if err := s.Queries.EnsureUser(ctx, kindleTestUser); err != nil {
		t.Fatalf("ensure user: %v", err)
	}
	return s
}

// fakeMailer records every Message it is asked to send, so tests can assert
// on subject, attachment name, and content without touching real SMTP.
type fakeMailer struct {
	sent []mailer.Message
}

func (m *fakeMailer) Send(_ context.Context, msg mailer.Message) error {
	m.sent = append(m.sent, msg)
	return nil
}

func newItem(t *testing.T, s *store.Store, title, body, cardType string) db.Item {
	t.Helper()
	ctx := context.Background()
	item, err := s.Queries.CreateItem(ctx, db.CreateItemParams{UserID: kindleTestUser, Url: "https://example.com/" + title})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	if err := s.Queries.UpdateItemExtraction(ctx, db.UpdateItemExtractionParams{
		UserID: kindleTestUser, ID: item.ID, Title: title, Body: body, CardType: cardType,
	}); err != nil {
		t.Fatalf("update extraction: %v", err)
	}
	item, err = s.Queries.GetItem(ctx, db.GetItemParams{UserID: kindleTestUser, ID: item.ID})
	if err != nil {
		t.Fatalf("reload item: %v", err)
	}
	return item
}

func TestKindleWorkerItemSubjectMatchesTitle(t *testing.T) {
	s := newKindleTestStore(t)
	item := newItem(t, s, "My Article", "the full article body", "article")

	fm := &fakeMailer{}
	w := &jobs.SendKindleWorker{
		Store:    s,
		Provider: ai.NewNoop(),
		Deps:     jobs.KindleDeps{Mailer: fm, To: "reader@kindle.com", Configured: true},
	}
	id := item.ID
	err := w.Work(context.Background(), &river.Job[jobs.SendKindleArgs]{Args: jobs.SendKindleArgs{UserID: kindleTestUser, ItemID: &id}})
	if err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(fm.sent) != 1 {
		t.Fatalf("sent = %d messages, want 1", len(fm.sent))
	}
	msg := fm.sent[0]
	if msg.To != "reader@kindle.com" {
		t.Errorf("to = %q, want reader@kindle.com", msg.To)
	}
	if msg.Subject != "My Article" {
		t.Errorf("subject = %q, want item title", msg.Subject)
	}
	if msg.Attachment == nil || msg.Attachment.ContentType != "application/epub+zip" {
		t.Fatalf("attachment = %+v, want an epub", msg.Attachment)
	}
	if len(msg.Attachment.Data) == 0 {
		t.Error("attachment data is empty")
	}
}

func TestKindleWorkerItemSkipsEmptyBody(t *testing.T) {
	s := newKindleTestStore(t)
	ctx := context.Background()
	item, err := s.Queries.CreateItem(ctx, db.CreateItemParams{UserID: kindleTestUser, Url: "https://example.com/empty"})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	fm := &fakeMailer{}
	w := &jobs.SendKindleWorker{Store: s, Provider: ai.NewNoop(), Deps: jobs.KindleDeps{Mailer: fm, To: "reader@kindle.com", Configured: true}}
	id := item.ID
	if err := w.Work(ctx, &river.Job[jobs.SendKindleArgs]{Args: jobs.SendKindleArgs{UserID: kindleTestUser, ItemID: &id}}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(fm.sent) != 0 {
		t.Errorf("sent = %d messages, want 0 for a body-less item (nothing to retry)", len(fm.sent))
	}
}

func TestKindleWorkerUnconfiguredReturnsError(t *testing.T) {
	s := newKindleTestStore(t)
	item := newItem(t, s, "Title", "body", "article")

	fm := &fakeMailer{}
	w := &jobs.SendKindleWorker{Store: s, Provider: ai.NewNoop(), Deps: jobs.KindleDeps{Mailer: fm, Configured: false}}
	id := item.ID
	err := w.Work(context.Background(), &river.Job[jobs.SendKindleArgs]{Args: jobs.SendKindleArgs{UserID: kindleTestUser, ItemID: &id}})
	if err == nil {
		t.Fatal("Work = nil error, want an error so River retries rather than silently dropping the send")
	}
	if len(fm.sent) != 0 {
		t.Errorf("sent = %d messages, want 0", len(fm.sent))
	}
}

// TestKindleWorkerLensDigestCapsAndSkipsBodyless interleaves bodyless and
// bodied items (so the cap is reached only after skipping several bodyless
// ones along the way) and asserts the resulting EPUB has exactly 25 chapters
// — one per bodied item, capped, with bodyless items excluded entirely.
func TestKindleWorkerLensDigestCapsAndSkipsBodyless(t *testing.T) {
	s := newKindleTestStore(t)
	ctx := context.Background()

	const total = 60 // 30 with a body, interleaved with 30 without
	for i := 0; i < total; i++ {
		body := ""
		if i%2 == 0 {
			body = "content"
		}
		newItem(t, s, "note "+uuid.NewString(), body, "note")
	}

	lensRule := []byte(`{"types":["note"]}`)
	lens, err := s.Queries.CreateLens(ctx, db.CreateLensParams{UserID: kindleTestUser, Name: "Notes digest", Rule: lensRule})
	if err != nil {
		t.Fatalf("create lens: %v", err)
	}

	fm := &fakeMailer{}
	w := &jobs.SendKindleWorker{Store: s, Provider: ai.NewNoop(), Deps: jobs.KindleDeps{Mailer: fm, To: "reader@kindle.com", Configured: true}}
	lensID := lens.ID
	if err := w.Work(ctx, &river.Job[jobs.SendKindleArgs]{Args: jobs.SendKindleArgs{UserID: kindleTestUser, LensID: &lensID}}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(fm.sent) != 1 {
		t.Fatalf("sent = %d messages, want 1", len(fm.sent))
	}
	msg := fm.sent[0]
	if !strings.HasPrefix(msg.Subject, "Openmind digest — Notes digest — ") {
		t.Errorf("subject = %q, want digest title", msg.Subject)
	}
	if msg.Attachment == nil || len(msg.Attachment.Data) == 0 {
		t.Fatal("expected an epub attachment")
	}

	zr, err := zip.NewReader(bytes.NewReader(msg.Attachment.Data), int64(len(msg.Attachment.Data)))
	if err != nil {
		t.Fatalf("opening epub as zip: %v", err)
	}
	chapters := 0
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "OEBPS/chapter-") {
			chapters++
		}
	}
	if chapters != 25 {
		t.Errorf("chapters = %d, want 25 (capped, bodyless items excluded)", chapters)
	}
}

// TestKindleWorkerItemIDsModeBuildsFromExactlyThoseItems asserts that when
// ItemIDs is set, the digest is built from exactly that set — not from
// re-running the lens's rule — and that a body-less item among them is
// skipped just like the rule-derived path.
func TestKindleWorkerItemIDsModeBuildsFromExactlyThoseItems(t *testing.T) {
	s := newKindleTestStore(t)
	ctx := context.Background()

	included := newItem(t, s, "included", "included body", "note")
	bodyless := newItem(t, s, "bodyless", "", "note")
	// excluded matches the lens's rule but is not in ItemIDs, so it must not
	// appear in the digest.
	newItem(t, s, "excluded", "excluded body", "note")

	lens, err := s.Queries.CreateLens(ctx, db.CreateLensParams{UserID: kindleTestUser, Name: "Some notes", Rule: []byte(`{"types":["note"]}`)})
	if err != nil {
		t.Fatalf("create lens: %v", err)
	}

	fm := &fakeMailer{}
	w := &jobs.SendKindleWorker{Store: s, Provider: ai.NewNoop(), Deps: jobs.KindleDeps{Mailer: fm, To: "reader@kindle.com", Configured: true}}
	lensID := lens.ID
	args := jobs.SendKindleArgs{UserID: kindleTestUser, LensID: &lensID, ItemIDs: []uuid.UUID{included.ID, bodyless.ID}}
	if err := w.Work(ctx, &river.Job[jobs.SendKindleArgs]{Args: args}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(fm.sent) != 1 {
		t.Fatalf("sent = %d messages, want 1", len(fm.sent))
	}

	zr, err := zip.NewReader(bytes.NewReader(fm.sent[0].Attachment.Data), int64(len(fm.sent[0].Attachment.Data)))
	if err != nil {
		t.Fatalf("opening epub as zip: %v", err)
	}
	chapters := 0
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "OEBPS/chapter-") {
			chapters++
		}
	}
	if chapters != 1 {
		t.Errorf("chapters = %d, want 1 (only the included, bodied item)", chapters)
	}
}

// TestKindleWorkerRecipientFallsBackToDepsTo asserts recipient resolution:
// the user's kindle_email setting wins when present, and Deps.To is used
// only as a fallback when it is not.
func TestKindleWorkerRecipientFallsBackToDepsTo(t *testing.T) {
	s := newKindleTestStore(t)
	item := newItem(t, s, "Title", "body", "article")

	fm := &fakeMailer{}
	w := &jobs.SendKindleWorker{Store: s, Provider: ai.NewNoop(), Deps: jobs.KindleDeps{Mailer: fm, To: "fallback@kindle.com", Configured: true}}
	id := item.ID
	if err := w.Work(context.Background(), &river.Job[jobs.SendKindleArgs]{Args: jobs.SendKindleArgs{UserID: kindleTestUser, ItemID: &id}}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(fm.sent) != 1 || fm.sent[0].To != "fallback@kindle.com" {
		t.Fatalf("sent = %+v, want one message to the Deps.To fallback", fm.sent)
	}

	if err := s.Queries.UpsertUserSetting(context.Background(), db.UpsertUserSettingParams{
		UserID: kindleTestUser, Key: "kindle_email", Value: "personal@kindle.com",
	}); err != nil {
		t.Fatalf("upsert setting: %v", err)
	}
	if err := w.Work(context.Background(), &river.Job[jobs.SendKindleArgs]{Args: jobs.SendKindleArgs{UserID: kindleTestUser, ItemID: &id}}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(fm.sent) != 2 || fm.sent[1].To != "personal@kindle.com" {
		t.Fatalf("sent[1] = %+v, want a message to the user's kindle_email setting", fm.sent)
	}
}

// TestKindleWorkerRecipientErrorsWithNeitherSettingNorFallback asserts that
// when neither the user's kindle_email setting nor Deps.To is configured,
// Work returns an error so River retries rather than silently dropping the
// send.
func TestKindleWorkerRecipientErrorsWithNeitherSettingNorFallback(t *testing.T) {
	s := newKindleTestStore(t)
	item := newItem(t, s, "Title", "body", "article")

	fm := &fakeMailer{}
	w := &jobs.SendKindleWorker{Store: s, Provider: ai.NewNoop(), Deps: jobs.KindleDeps{Mailer: fm, To: "", Configured: true}}
	id := item.ID
	err := w.Work(context.Background(), &river.Job[jobs.SendKindleArgs]{Args: jobs.SendKindleArgs{UserID: kindleTestUser, ItemID: &id}})
	if err == nil {
		t.Fatal("Work = nil error, want an error when neither kindle_email nor Deps.To is set")
	}
	if len(fm.sent) != 0 {
		t.Errorf("sent = %d messages, want 0", len(fm.sent))
	}
}
