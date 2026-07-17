package geo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// GooglePlaces geocodes via the Google Places API (New) Text Search endpoint.
// It requests only Pro-SKU fields (formattedAddress, location) via the field
// mask, keeping every call inside the Pro tier's free monthly threshold at
// this project's volumes. POI coverage is far better than OSM's, which is the
// whole reason to configure it over nominatim.
//
// Note on Google's terms: Places data is subject to Google's caching and
// storage restrictions; Openmind persists resolved coordinates indefinitely.
// Operators enable this geocoder knowingly via GEOCODER=google (see
// docs/self-hosting.md).
type GooglePlaces struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewGooglePlaces builds a Google Places geocoder. client defaults to a
// 10s-timeout client; tests inject an httptest client and override baseURL.
func NewGooglePlaces(apiKey string, client *http.Client) *GooglePlaces {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &GooglePlaces{
		apiKey:  apiKey,
		baseURL: "https://places.googleapis.com",
		client:  client,
	}
}

// Name returns the geocoder name.
func (*GooglePlaces) Name() string { return "google" }

// Geocode resolves query via POST /v1/places:searchText with pageSize 1. A
// non-200 is returned as an error (the caller stores the place without
// coordinates); an empty result set is a clean miss (ok=false, no error).
func (g *GooglePlaces) Geocode(ctx context.Context, query string) (Result, bool, error) {
	payload, err := json.Marshal(map[string]any{"textQuery": query, "pageSize": 1})
	if err != nil {
		return Result{}, false, fmt.Errorf("google places: encoding request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+"/v1/places:searchText", bytes.NewReader(payload))
	if err != nil {
		return Result{}, false, fmt.Errorf("google places: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-Api-Key", g.apiKey)
	req.Header.Set("X-Goog-FieldMask", "places.formattedAddress,places.location")

	resp, err := g.client.Do(req)
	if err != nil {
		return Result{}, false, fmt.Errorf("google places: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Result{}, false, fmt.Errorf("google places: unexpected status %d", resp.StatusCode)
	}

	var body struct {
		Places []struct {
			FormattedAddress string `json:"formattedAddress"`
			Location         struct {
				Latitude  float64 `json:"latitude"`
				Longitude float64 `json:"longitude"`
			} `json:"location"`
		} `json:"places"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Result{}, false, fmt.Errorf("google places: decoding response: %w", err)
	}
	if len(body.Places) == 0 {
		return Result{}, false, nil
	}
	p := body.Places[0]
	slog.Debug("google places geocoded", "query", query, "lat", p.Location.Latitude, "lng", p.Location.Longitude)
	return Result{Lat: p.Location.Latitude, Lng: p.Location.Longitude, Address: p.FormattedAddress}, true, nil
}
