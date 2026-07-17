package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/rohithgilla12/openmind/api/internal/api"
	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

func TestGetItemPlaces(t *testing.T) {
	s, rc, pool := testDeps(t)
	ctx := context.Background()
	srv := httptest.NewServer(newSrv(t, s, rc, ""))
	defer srv.Close()

	item, err := s.Queries.CreateItem(ctx, db.CreateItemParams{UserID: api.DevUserID, Url: "https://www.instagram.com/reel/abc/"})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	getPlaces := func(t *testing.T, id string) (int, []map[string]any) {
		t.Helper()
		resp, err := http.Get(srv.URL + "/items/" + id + "/places")
		if err != nil {
			t.Fatalf("get places: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return resp.StatusCode, nil
		}
		var out []map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode places: %v", err)
		}
		return resp.StatusCode, out
	}

	t.Run("empty before extraction", func(t *testing.T) {
		status, out := getPlaces(t, item.ID.String())
		if status != http.StatusOK || len(out) != 0 {
			t.Errorf("status=%d places=%v, want 200 []", status, out)
		}
	})

	t.Run("returns stored places, geocoded and not", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `INSERT INTO item_places (user_id, item_id, name, hint, address, lat, lng, source)
			VALUES ($1, $2, 'Fabrica', 'Lisbon', 'Fabrica, Lisbon, Portugal', 38.7223, -9.1393, 'caption'),
			       ($1, $2, 'Copenhagen Coffee Lab', 'Lisbon', '', NULL, NULL, 'caption')`,
			api.DevUserID, item.ID); err != nil {
			t.Fatalf("seeding places: %v", err)
		}
		status, out := getPlaces(t, item.ID.String())
		if status != http.StatusOK || len(out) != 2 {
			t.Fatalf("status=%d len=%d, want 200 with 2 places", status, len(out))
		}
		byName := map[string]map[string]any{}
		for _, p := range out {
			byName[p["name"].(string)] = p
		}
		if p := byName["Fabrica"]; p == nil || p["lat"] != 38.7223 || p["address"] != "Fabrica, Lisbon, Portugal" {
			t.Errorf("geocoded place = %v", p)
		}
		if p := byName["Copenhagen Coffee Lab"]; p == nil {
			t.Error("missing ungeocoded place")
		} else if _, hasLat := p["lat"]; hasLat {
			t.Errorf("ungeocoded place must omit lat: %v", p)
		}
	})

	t.Run("unknown item is 404", func(t *testing.T) {
		if status, _ := getPlaces(t, uuid.NewString()); status != http.StatusNotFound {
			t.Errorf("status=%d, want 404", status)
		}
	})
}

func TestListPlaces(t *testing.T) {
	s, rc, pool := testDeps(t)
	ctx := context.Background()
	srv := httptest.NewServer(newSrv(t, s, rc, ""))
	defer srv.Close()

	listPlaces := func(t *testing.T) (int, []map[string]any) {
		t.Helper()
		resp, err := http.Get(srv.URL + "/places")
		if err != nil {
			t.Fatalf("get places: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return resp.StatusCode, nil
		}
		var out []map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode places: %v", err)
		}
		return resp.StatusCode, out
	}

	t.Run("empty before any places exist", func(t *testing.T) {
		status, out := listPlaces(t)
		if status != http.StatusOK || len(out) != 0 {
			t.Errorf("status=%d places=%v, want 200 []", status, out)
		}
	})

	item, err := s.Queries.CreateItem(ctx, db.CreateItemParams{UserID: api.DevUserID, Url: "https://www.instagram.com/reel/xyz/"})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	if err := s.Queries.UpdateItemExtraction(ctx, db.UpdateItemExtractionParams{
		UserID: api.DevUserID, ID: item.ID, Title: "Lisbon reel", Body: "places", CardType: "video",
	}); err != nil {
		t.Fatalf("update extraction: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO item_places (user_id, item_id, name, hint, address, lat, lng, source)
		VALUES ($1, $2, 'Fabrica', 'Lisbon', 'Fabrica, Lisbon, Portugal', 38.7223, -9.1393, 'caption'),
		       ($1, $2, 'Copenhagen Coffee Lab', 'Lisbon', '', NULL, NULL, 'caption')`,
		api.DevUserID, item.ID); err != nil {
		t.Fatalf("seeding places: %v", err)
	}

	// Another user's item + place must never leak into user A's listing.
	otherUser := uuid.MustParse("00000000-0000-0000-0000-0000000000ff")
	if err := s.Queries.EnsureUser(ctx, otherUser); err != nil {
		t.Fatalf("ensure other user: %v", err)
	}
	otherItem, err := s.Queries.CreateItem(ctx, db.CreateItemParams{UserID: otherUser, Url: "https://www.instagram.com/reel/other/"})
	if err != nil {
		t.Fatalf("create other item: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO item_places (user_id, item_id, name, hint, address, lat, lng, source)
		VALUES ($1, $2, 'Secret Spot', 'Nowhere', '', NULL, NULL, 'caption')`,
		otherUser, otherItem.ID); err != nil {
		t.Fatalf("seeding other user's place: %v", err)
	}

	t.Run("returns only the caller's places with item context", func(t *testing.T) {
		status, out := listPlaces(t)
		if status != http.StatusOK || len(out) != 2 {
			t.Fatalf("status=%d len=%d, want 200 with 2 places", status, len(out))
		}
		byName := map[string]map[string]any{}
		for _, p := range out {
			byName[p["name"].(string)] = p
		}
		if p := byName["Secret Spot"]; p != nil {
			t.Errorf("cross-tenant place leaked into listing: %v", p)
		}
		fabrica := byName["Fabrica"]
		if fabrica == nil {
			t.Fatal("missing geocoded place")
		}
		if fabrica["lat"] != 38.7223 || fabrica["address"] != "Fabrica, Lisbon, Portugal" {
			t.Errorf("geocoded place = %v", fabrica)
		}
		if fabrica["itemId"] != item.ID.String() {
			t.Errorf("itemId = %v, want %v", fabrica["itemId"], item.ID)
		}
		if fabrica["itemTitle"] != "Lisbon reel" {
			t.Errorf("itemTitle = %v, want Lisbon reel", fabrica["itemTitle"])
		}
		if fabrica["itemCardType"] != "video" {
			t.Errorf("itemCardType = %v, want video", fabrica["itemCardType"])
		}

		coffee := byName["Copenhagen Coffee Lab"]
		if coffee == nil {
			t.Fatal("missing ungeocoded place")
		}
		if _, hasLat := coffee["lat"]; hasLat {
			t.Errorf("ungeocoded place must omit lat: %v", coffee)
		}
		if _, hasLng := coffee["lng"]; hasLng {
			t.Errorf("ungeocoded place must omit lng: %v", coffee)
		}
		if coffee["itemId"] != item.ID.String() || coffee["itemTitle"] != "Lisbon reel" || coffee["itemCardType"] != "video" {
			t.Errorf("ungeocoded place item context = %v", coffee)
		}
	})
}
