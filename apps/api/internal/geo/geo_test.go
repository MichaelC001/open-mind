package geo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNominatimGeocode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			http.NotFound(w, r)
			return
		}
		if ua := r.Header.Get("User-Agent"); ua == "" {
			t.Error("missing User-Agent header")
		}
		switch r.URL.Query().Get("q") {
		case "Fabrica, Lisbon":
			_, _ = w.Write([]byte(`[{"lat":"38.7223","lon":"-9.1393","display_name":"Fabrica Coffee Roasters, Lisbon, Portugal"}]`))
		case "Nowhere Special":
			_, _ = w.Write([]byte(`[]`))
		case "flaky":
			http.Error(w, "slow down", http.StatusTooManyRequests)
		}
	}))
	defer srv.Close()

	n := NewNominatim(srv.URL, "test@example.com", srv.Client())

	res, ok, err := n.Geocode(context.Background(), "Fabrica, Lisbon")
	if err != nil || !ok {
		t.Fatalf("Geocode hit: ok=%v err=%v", ok, err)
	}
	if res.Lat != 38.7223 || res.Lng != -9.1393 || res.Address == "" {
		t.Errorf("Geocode = %+v", res)
	}

	_, ok, err = n.Geocode(context.Background(), "Nowhere Special")
	if err != nil || ok {
		t.Errorf("Geocode miss: ok=%v err=%v, want clean miss", ok, err)
	}

	if _, _, err := n.Geocode(context.Background(), "flaky"); err == nil {
		t.Error("expected error for 429 response")
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("GEOCODER", "")
	if g, err := FromEnv(); g != nil || err != nil {
		t.Errorf("unset GEOCODER: got %v, %v; want nil, nil", g, err)
	}

	t.Setenv("GEOCODER", "nominatim")
	g, err := FromEnv()
	if err != nil || g == nil || g.Name() != "nominatim" {
		t.Errorf("nominatim: got %v, %v", g, err)
	}

	t.Setenv("GEOCODER", "mapzen")
	if _, err := FromEnv(); err == nil {
		t.Error("expected error for unknown geocoder")
	}
}
