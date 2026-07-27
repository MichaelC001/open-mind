package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestExpoBatchesTokens proves the sender splits >100 tokens across requests:
// the Expo Push API caps a single call at 100 messages, so a naive one-request
// send would silently drop targets.
func TestExpoBatchesTokens(t *testing.T) {
	var batchSizes []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msgs []map[string]any
		if err := json.NewDecoder(r.Body).Decode(&msgs); err != nil {
			t.Errorf("decoding body: %v", err)
		}
		batchSizes = append(batchSizes, len(msgs))
		data := make([]map[string]any, len(msgs))
		for i := range msgs {
			data[i] = map[string]any{"status": "ok", "id": fmt.Sprintf("ticket-%d", i)}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer srv.Close()

	e := NewExpo("")
	e.BaseURL = srv.URL

	devices := make([]Device, 250)
	for i := range devices {
		devices[i] = Device{Token: fmt.Sprintf("ExponentPushToken[%d]", i), Platform: "ios"}
	}

	results, err := e.Send(context.Background(), Notification{Title: "hi"}, Target{Devices: devices})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(results) != 250 {
		t.Errorf("results = %d, want 250", len(results))
	}
	want := []int{100, 100, 50}
	if len(batchSizes) != len(want) {
		t.Fatalf("batches = %v, want %v", batchSizes, want)
	}
	for i, n := range want {
		if batchSizes[i] != n {
			t.Errorf("batch %d = %d, want %d", i, batchSizes[i], n)
		}
	}
}

// A per-message error in the ticket response marks only that token failed.
// When details.error is set, it takes precedence over the generic message.
func TestExpoPerTicketErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"status": "ok", "id": "ticket-a"},
			{"status": "error", "message": "bad token", "details": map[string]any{"error": "DeviceNotRegistered"}},
		}})
	}))
	defer srv.Close()

	e := NewExpo("")
	e.BaseURL = srv.URL
	results, err := e.Send(context.Background(), Notification{Title: "hi"}, Target{Devices: []Device{
		{Token: "ExponentPushToken[a]"}, {Token: "ExponentPushToken[b]"},
	}})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if !results[0].OK || results[0].TicketID != "ticket-a" {
		t.Errorf("results[0] = %+v", results[0])
	}
	if results[1].OK || results[1].Err == nil {
		t.Errorf("results[1] = %+v, want failure", results[1])
	}
	if !strings.Contains(results[1].Err.Error(), "DeviceNotRegistered") {
		t.Errorf("results[1].Err = %q, want to contain 'DeviceNotRegistered'", results[1].Err.Error())
	}
}

// When a ticket has only a generic message and no details.error, that message
// is used as the error text.
func TestExpoPerTicketErrorsFallbackToMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"status": "ok", "id": "ticket-a"},
			{"status": "error", "message": "rate limited"},
		}})
	}))
	defer srv.Close()

	e := NewExpo("")
	e.BaseURL = srv.URL
	results, err := e.Send(context.Background(), Notification{Title: "hi"}, Target{Devices: []Device{
		{Token: "ExponentPushToken[a]"}, {Token: "ExponentPushToken[b]"},
	}})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if !results[0].OK || results[0].TicketID != "ticket-a" {
		t.Errorf("results[0] = %+v", results[0])
	}
	if results[1].OK || results[1].Err == nil {
		t.Errorf("results[1] = %+v, want failure", results[1])
	}
	if !strings.Contains(results[1].Err.Error(), "rate limited") {
		t.Errorf("results[1].Err = %q, want to contain 'rate limited'", results[1].Err.Error())
	}
}

// Receipts translate a ticket ID to its terminal error code, which is the only
// place DeviceNotRegistered actually surfaces.
func TestExpoReceipts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"ticket-a": map[string]any{"status": "ok"},
			"ticket-b": map[string]any{"status": "error", "details": map[string]any{"error": "DeviceNotRegistered"}},
		}})
	}))
	defer srv.Close()

	e := NewExpo("")
	e.BaseURL = srv.URL
	got, err := e.Receipts(context.Background(), []string{"ticket-a", "ticket-b"})
	if err != nil {
		t.Fatalf("Receipts: %v", err)
	}
	if got["ticket-a"] != "" {
		t.Errorf("ticket-a = %q, want empty", got["ticket-a"])
	}
	if got["ticket-b"] != "DeviceNotRegistered" {
		t.Errorf("ticket-b = %q", got["ticket-b"])
	}
}
