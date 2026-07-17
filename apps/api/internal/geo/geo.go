// Package geo provides the pluggable, optional geocoder used by place
// extraction. No geocoder is required: when none is configured, extracted
// places are stored by name with no coordinates and the app stays fully
// functional.
package geo

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"golang.org/x/time/rate"
)

// Result is a successful geocode: coordinates plus the display address the
// geocoder resolved the query to.
type Result struct {
	Lat     float64
	Lng     float64
	Address string
}

// Geocoder resolves a free-text place query ("Fabrica, Lisbon") to
// coordinates. ok is false when the geocoder found no match — that is not an
// error, and callers store the place without coordinates.
type Geocoder interface {
	Name() string
	Geocode(ctx context.Context, query string) (res Result, ok bool, err error)
}

// FromEnv builds the configured geocoder, or nil when GEOCODER is unset —
// geocoding is optional and nil is the supported "off" state.
//
// GEOCODER=nominatim enables OSM Nominatim. NOMINATIM_URL points at a
// self-hosted instance (default: the public endpoint, whose usage policy
// requires an identifying contact — set NOMINATIM_EMAIL).
//
// GEOCODER=google enables Google Places Text Search (better POI coverage,
// requires GOOGLE_PLACES_API_KEY with the Places API (New) enabled).
func FromEnv() (Geocoder, error) {
	switch os.Getenv("GEOCODER") {
	case "", "none":
		return nil, nil
	case "nominatim":
		base := os.Getenv("NOMINATIM_URL")
		if base == "" {
			base = "https://nominatim.openstreetmap.org"
		}
		return NewNominatim(base, os.Getenv("NOMINATIM_EMAIL"), nil), nil
	case "google":
		key := os.Getenv("GOOGLE_PLACES_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("geo: GEOCODER=google requires GOOGLE_PLACES_API_KEY")
		}
		return NewGooglePlaces(key, nil), nil
	default:
		return nil, fmt.Errorf("geo: unknown GEOCODER %q (want nominatim or google)", os.Getenv("GEOCODER"))
	}
}

// Nominatim geocodes via an OSM Nominatim /search endpoint. A hard 1 rps
// client-side limiter keeps every deployment inside the public endpoint's
// usage policy; self-hosted instances just tolerate the same gentle pace.
type Nominatim struct {
	baseURL string
	email   string
	client  *http.Client
	limiter *rate.Limiter
}

// NewNominatim builds a Nominatim geocoder against the given base URL. email
// (may be empty for self-hosted instances) identifies the deployment to the
// public endpoint per its usage policy. client defaults to a 10s-timeout
// client; tests inject an httptest client.
func NewNominatim(baseURL, email string, client *http.Client) *Nominatim {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Nominatim{
		baseURL: baseURL,
		email:   email,
		client:  client,
		limiter: rate.NewLimiter(rate.Limit(1), 1),
	}
}

// Name returns the geocoder name.
func (*Nominatim) Name() string { return "nominatim" }

// Geocode resolves query via GET /search?format=jsonv2&limit=1. A 429/5xx is
// returned as an error (the job retries later); an empty result set is a
// clean miss (ok=false, no error).
func (n *Nominatim) Geocode(ctx context.Context, query string) (Result, bool, error) {
	if err := n.limiter.Wait(ctx); err != nil {
		return Result{}, false, fmt.Errorf("nominatim: waiting for rate limiter: %w", err)
	}

	q := url.Values{"format": {"jsonv2"}, "limit": {"1"}, "q": {query}}
	if n.email != "" {
		q.Set("email", n.email)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.baseURL+"/search?"+q.Encode(), nil)
	if err != nil {
		return Result{}, false, fmt.Errorf("nominatim: building request: %w", err)
	}
	req.Header.Set("User-Agent", "openmind (self-hosted; +https://github.com/rohithgilla12/open-mind)")

	resp, err := n.client.Do(req)
	if err != nil {
		return Result{}, false, fmt.Errorf("nominatim: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Result{}, false, fmt.Errorf("nominatim: unexpected status %d", resp.StatusCode)
	}

	var rows []struct {
		Lat         string `json:"lat"`
		Lon         string `json:"lon"`
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return Result{}, false, fmt.Errorf("nominatim: decoding response: %w", err)
	}
	if len(rows) == 0 {
		return Result{}, false, nil
	}
	lat, err := strconv.ParseFloat(rows[0].Lat, 64)
	if err != nil {
		return Result{}, false, fmt.Errorf("nominatim: parsing lat %q: %w", rows[0].Lat, err)
	}
	lng, err := strconv.ParseFloat(rows[0].Lon, 64)
	if err != nil {
		return Result{}, false, fmt.Errorf("nominatim: parsing lon %q: %w", rows[0].Lon, err)
	}
	slog.Debug("nominatim geocoded", "query", query, "lat", lat, "lng", lng)
	return Result{Lat: lat, Lng: lng, Address: rows[0].DisplayName}, true, nil
}
