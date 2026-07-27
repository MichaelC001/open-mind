package notify

import (
	"testing"

	"github.com/google/uuid"
)

func TestCoalescePassesThroughNonRiverCategories(t *testing.T) {
	in := []Notification{
		{ID: uuid.New(), Category: CategoryDigest, Title: "Design digest"},
		{ID: uuid.New(), Category: CategoryDigest, Title: "Reading digest"},
	}
	got := Coalesce(CategoryDigest, in)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (digest must not collapse)", len(got))
	}
}

func TestCoalesceFeedRiverAcrossFeeds(t *testing.T) {
	in := []Notification{
		{ID: uuid.New(), UserID: uuid.Nil, Category: CategoryFeedRiver, Title: "5 new items", Data: map[string]any{"feed_id": "a", "count": float64(5)}},
		{ID: uuid.New(), Category: CategoryFeedRiver, Title: "4 new items", Data: map[string]any{"feed_id": "b", "count": float64(4)}},
		{ID: uuid.New(), Category: CategoryFeedRiver, Title: "3 new items", Data: map[string]any{"feed_id": "c", "count": float64(3)}},
	}
	got := Coalesce(CategoryFeedRiver, in)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Title != "12 new items across 3 feeds" {
		t.Errorf("Title = %q, want %q", got[0].Title, "12 new items across 3 feeds")
	}
	if len(got[0].SourceIDs) != 3 {
		t.Errorf("SourceIDs = %d, want 3", len(got[0].SourceIDs))
	}
	// Mixed feeds must not deep-link to an arbitrary one.
	if _, ok := got[0].Data["feed_id"]; ok {
		t.Errorf("Data carries feed_id %v for a mixed-feed roll-up; want none", got[0].Data["feed_id"])
	}
}

func TestCoalesceFeedRiverSingleFeedKeepsDeepLink(t *testing.T) {
	in := []Notification{
		{ID: uuid.New(), Category: CategoryFeedRiver, Data: map[string]any{"feed_id": "a", "count": float64(2)}},
		{ID: uuid.New(), Category: CategoryFeedRiver, Data: map[string]any{"feed_id": "a", "count": float64(1)}},
	}
	got := Coalesce(CategoryFeedRiver, in)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Title != "3 new items in a feed you follow" {
		t.Errorf("Title = %q", got[0].Title)
	}
	if got[0].Data["feed_id"] != "a" {
		t.Errorf("feed_id = %v, want a", got[0].Data["feed_id"])
	}
}

func TestCoalesceEmpty(t *testing.T) {
	if got := Coalesce(CategoryFeedRiver, nil); len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}
