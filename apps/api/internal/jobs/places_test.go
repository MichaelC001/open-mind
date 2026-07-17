package jobs_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/rohithgilla12/openmind/api/internal/ai"
	"github.com/rohithgilla12/openmind/api/internal/geo"
	"github.com/rohithgilla12/openmind/api/internal/jobs"
	"github.com/rohithgilla12/openmind/api/internal/store"
	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

var placesTestUser = uuid.MustParse("00000000-0000-0000-0000-0000000000cc")

func newPlacesTestStore(t *testing.T) *store.Store {
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
	if _, err := pool.Exec(ctx, `TRUNCATE items, item_places, river_job CASCADE`); err != nil {
		t.Fatalf("truncating: %v", err)
	}
	s := store.New(pool)
	if err := s.Queries.EnsureUser(ctx, placesTestUser); err != nil {
		t.Fatalf("ensure user: %v", err)
	}
	return s
}

func newPlacesTestItem(t *testing.T, s *store.Store, userID uuid.UUID, title, body, leadImageURL string) db.Item {
	t.Helper()
	ctx := context.Background()
	item, err := s.Queries.CreateItem(ctx, db.CreateItemParams{UserID: userID, Url: "https://www.instagram.com/reel/" + uuid.NewString()})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	if err := s.Queries.UpdateItemExtraction(ctx, db.UpdateItemExtractionParams{
		UserID: userID, ID: item.ID, Title: title, Body: body, LeadImageUrl: leadImageURL, CardType: "video",
	}); err != nil {
		t.Fatalf("update extraction: %v", err)
	}
	return item
}

func runPlacesWorker(t *testing.T, w *jobs.ExtractPlacesWorker, userID, itemID uuid.UUID) error {
	t.Helper()
	return w.Work(context.Background(), &river.Job[jobs.ExtractPlacesArgs]{
		JobRow: nil,
		Args:   jobs.ExtractPlacesArgs{UserID: userID, ItemID: itemID},
	})
}

// stubGeocoder resolves every query to a fixed coordinate, counting calls.
type stubGeocoder struct{ calls int }

func (*stubGeocoder) Name() string { return "stub" }

func (g *stubGeocoder) Geocode(context.Context, string) (geo.Result, bool, error) {
	g.calls++
	return geo.Result{Lat: 38.7, Lng: -9.1, Address: "Somewhere, Lisbon"}, true, nil
}

func TestExtractPlacesWorker(t *testing.T) {
	s := newPlacesTestStore(t)
	ctx := context.Background()

	t.Run("extracts, geocodes, and is idempotent", func(t *testing.T) {
		item := newPlacesTestItem(t, s, placesTestUser, "reel", "cafes in lisbon", "")
		gc := &stubGeocoder{}
		w := &jobs.ExtractPlacesWorker{Store: s, Provider: ai.NewFake(), Geocoder: gc}

		for run := 0; run < 2; run++ { // second run must reproduce the same rows
			if err := runPlacesWorker(t, w, placesTestUser, item.ID); err != nil {
				t.Fatalf("run %d: %v", run, err)
			}
		}

		rows, err := s.Queries.ListItemPlaces(ctx, db.ListItemPlacesParams{UserID: placesTestUser, ItemID: item.ID})
		if err != nil {
			t.Fatalf("listing places: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("got %d places, want 2 (fake provider fixture)", len(rows))
		}
		if rows[0].Name != "Fake Cafe" || rows[0].Hint != "Faketown" {
			t.Errorf("first place = %q/%q", rows[0].Name, rows[0].Hint)
		}
		if rows[0].Source != "caption" {
			t.Errorf("source = %q, want caption", rows[0].Source)
		}
		if !rows[0].Lat.Valid || rows[0].Lat.Float64 != 38.7 || rows[0].Address != "Somewhere, Lisbon" {
			t.Errorf("first place not geocoded: %+v", rows[0])
		}
	})

	t.Run("no geocoder stores places without coordinates", func(t *testing.T) {
		item := newPlacesTestItem(t, s, placesTestUser, "reel", "cafes in lisbon", "")
		w := &jobs.ExtractPlacesWorker{Store: s, Provider: ai.NewFake()}
		if err := runPlacesWorker(t, w, placesTestUser, item.ID); err != nil {
			t.Fatal(err)
		}
		rows, err := s.Queries.ListItemPlaces(ctx, db.ListItemPlacesParams{UserID: placesTestUser, ItemID: item.ID})
		if err != nil {
			t.Fatalf("listing places: %v", err)
		}
		if len(rows) != 2 || rows[0].Lat.Valid || rows[0].Lng.Valid || rows[0].Address != "" {
			t.Errorf("want 2 coordinate-less places, got %+v", rows)
		}
	})

	t.Run("noop provider is a clean no-op", func(t *testing.T) {
		item := newPlacesTestItem(t, s, placesTestUser, "reel", "cafes in lisbon", "")
		w := &jobs.ExtractPlacesWorker{Store: s, Provider: ai.NewNoop()}
		if err := runPlacesWorker(t, w, placesTestUser, item.ID); err != nil {
			t.Fatalf("noop provider must not error: %v", err)
		}
		rows, _ := s.Queries.ListItemPlaces(ctx, db.ListItemPlacesParams{UserID: placesTestUser, ItemID: item.ID})
		if len(rows) != 0 {
			t.Errorf("noop provider stored %d places", len(rows))
		}
	})

	t.Run("empty caption is a clean no-op", func(t *testing.T) {
		item := newPlacesTestItem(t, s, placesTestUser, "", "", "")
		w := &jobs.ExtractPlacesWorker{Store: s, Provider: ai.NewFake()}
		if err := runPlacesWorker(t, w, placesTestUser, item.ID); err != nil {
			t.Fatal(err)
		}
		rows, _ := s.Queries.ListItemPlaces(ctx, db.ListItemPlacesParams{UserID: placesTestUser, ItemID: item.ID})
		if len(rows) != 0 {
			t.Errorf("empty caption stored %d places", len(rows))
		}
	})

	t.Run("merges caption and vision by confidence", func(t *testing.T) {
		// 1x1 PNG — fetchLeadImage accepts image/png.
		const png1x1 = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00\x90wS\xde\x00\x00\x00\x0cIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N\x00\x00\x00\x00IEND\xaeB`\x82"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte(png1x1))
		}))
		t.Cleanup(srv.Close)

		item := newPlacesTestItem(t, s, placesTestUser, "reel", "cafes in lisbon", srv.URL+"/thumb.png")
		w := &jobs.ExtractPlacesWorker{Store: s, Provider: ai.NewFake(), HTTPClient: srv.Client()}
		if err := runPlacesWorker(t, w, placesTestUser, item.ID); err != nil {
			t.Fatal(err)
		}
		rows, err := s.Queries.ListItemPlaces(ctx, db.ListItemPlacesParams{UserID: placesTestUser, ItemID: item.ID})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 3 {
			t.Fatalf("got %d places, want 3 (2 caption + 1 vision-only, cafe merged)", len(rows))
		}
		byName := map[string]db.ItemPlace{}
		for _, r := range rows {
			byName[r.Name] = r
		}
		if byName["Fake Cafe"].Source != "vision" {
			t.Errorf("Fake Cafe source = %q, want vision (higher confidence)", byName["Fake Cafe"].Source)
		}
		if byName["Fake Museum"].Source != "caption" {
			t.Errorf("Fake Museum source = %q, want caption", byName["Fake Museum"].Source)
		}
		if byName["Vision Landmark"].Source != "vision" {
			t.Errorf("Vision Landmark source = %q, want vision", byName["Vision Landmark"].Source)
		}
	})

	t.Run("vision-only when caption empty", func(t *testing.T) {
		const png1x1 = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00\x90wS\xde\x00\x00\x00\x0cIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N\x00\x00\x00\x00IEND\xaeB`\x82"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(png1x1))
		}))
		t.Cleanup(srv.Close)

		item := newPlacesTestItem(t, s, placesTestUser, "", "", srv.URL+"/thumb.png")
		w := &jobs.ExtractPlacesWorker{Store: s, Provider: ai.NewFake(), HTTPClient: srv.Client()}
		if err := runPlacesWorker(t, w, placesTestUser, item.ID); err != nil {
			t.Fatal(err)
		}
		rows, _ := s.Queries.ListItemPlaces(ctx, db.ListItemPlacesParams{UserID: placesTestUser, ItemID: item.ID})
		if len(rows) != 2 {
			t.Fatalf("vision-only got %d places, want 2", len(rows))
		}
		for _, r := range rows {
			if r.Source != "vision" {
				t.Errorf("%s source = %q, want vision", r.Name, r.Source)
			}
		}
	})

	t.Run("empty extraction clears prior places", func(t *testing.T) {
		item := newPlacesTestItem(t, s, placesTestUser, "reel", "cafes in lisbon", "")
		w := &jobs.ExtractPlacesWorker{Store: s, Provider: ai.NewFake()}
		if err := runPlacesWorker(t, w, placesTestUser, item.ID); err != nil {
			t.Fatal(err)
		}
		// Wipe caption so Fake returns nil; a successful empty extract must
		// clear the rows written above (idempotent replace, not leave-stale).
		if err := s.Queries.UpdateItemExtraction(ctx, db.UpdateItemExtractionParams{
			UserID: placesTestUser, ID: item.ID, Title: "reel", Body: "", CardType: "video",
		}); err != nil {
			t.Fatal(err)
		}
		if err := runPlacesWorker(t, w, placesTestUser, item.ID); err != nil {
			t.Fatal(err)
		}
		rows, _ := s.Queries.ListItemPlaces(ctx, db.ListItemPlacesParams{UserID: placesTestUser, ItemID: item.ID})
		if len(rows) != 0 {
			t.Errorf("empty extraction left %d stale places", len(rows))
		}
	})

	t.Run("cross-tenant item is not visible", func(t *testing.T) {
		otherUser := uuid.New()
		if err := s.Queries.EnsureUser(ctx, otherUser); err != nil {
			t.Fatal(err)
		}
		item := newPlacesTestItem(t, s, otherUser, "reel", "cafes in lisbon", "")
		w := &jobs.ExtractPlacesWorker{Store: s, Provider: ai.NewFake()}
		if err := runPlacesWorker(t, w, placesTestUser, item.ID); err == nil {
			t.Error("expected error loading another user's item")
		}
	})
}
