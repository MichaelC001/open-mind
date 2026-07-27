package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// expoPushURL and expoReceiptURL are Expo's hosted push endpoints.
const (
	expoPushURL    = "https://exp.host/--/api/v2/push/send"
	expoReceiptURL = "https://exp.host/--/api/v2/push/getReceipts"
)

// expoBatchSize is the maximum number of messages Expo accepts per request.
const expoBatchSize = 100

// expoReceiptBatchSize is the maximum number of ticket IDs Expo's
// getReceipts endpoint accepts per request — a separate, larger cap from
// expoBatchSize's push-send limit. Posting more than this in one call
// returns a non-200 response, which previously made the whole receipts job
// fail identically on every retry once ledger rows (inflated by one row per
// result x source row for a coalesced message) passed this count within the
// job's one-hour lookback.
const expoReceiptBatchSize = 1000

// expoTimeout bounds a single push or receipts call.
const expoTimeout = 20 * time.Second

// Expo delivers over the Expo Push service. BaseURL is overridable so tests
// can point it at an httptest server; when empty the hosted endpoints are used.
//
// Delivery is two-phase: a send returns *tickets*, and terminal failures such
// as DeviceNotRegistered only appear later in the *receipts*. Callers must
// poll Receipts to learn which tokens are dead.
type Expo struct {
	BaseURL     string
	AccessToken string
	HTTP        *http.Client
}

// NewExpo returns an Expo sender. accessToken may be empty; Expo accepts
// unauthenticated pushes, and the token only adds send-security.
func NewExpo(accessToken string) *Expo {
	return &Expo{AccessToken: accessToken, HTTP: &http.Client{Timeout: expoTimeout}}
}

func (*Expo) Name() string { return "expo" }

// expoMessage is one push in a batch.
type expoMessage struct {
	To    string         `json:"to"`
	Title string         `json:"title"`
	Body  string         `json:"body,omitempty"`
	Data  map[string]any `json:"data,omitempty"`
}

// expoTicket is one entry in the send response.
type expoTicket struct {
	Status  string `json:"status"`
	ID      string `json:"id"`
	Message string `json:"message"`
	Details struct {
		Error string `json:"error"`
	} `json:"details"`
}

// Send pushes n to every device in t, in batches of expoBatchSize. It returns
// one Result per device, in the same order as t.Devices. A whole-batch
// transport failure marks that batch's devices failed but does not abort the
// remaining batches — partial delivery beats none.
func (e *Expo) Send(ctx context.Context, n Notification, t Target) ([]Result, error) {
	if len(t.Devices) == 0 {
		return nil, nil
	}
	results := make([]Result, 0, len(t.Devices))
	for start := 0; start < len(t.Devices); start += expoBatchSize {
		end := min(start+expoBatchSize, len(t.Devices))
		batch := t.Devices[start:end]

		msgs := make([]expoMessage, len(batch))
		for i, d := range batch {
			msgs[i] = expoMessage{To: d.Token, Title: n.Title, Body: n.Body, Data: n.Data}
		}

		tickets, err := e.postBatch(ctx, msgs)
		if err != nil {
			for _, d := range batch {
				results = append(results, Result{Channel: "expo", Token: d.Token, OK: false, Err: err})
			}
			continue
		}
		for i, d := range batch {
			if i >= len(tickets) {
				results = append(results, Result{Channel: "expo", Token: d.Token, OK: false, Err: errors.New("expo: missing ticket for token")})
				continue
			}
			tk := tickets[i]
			if tk.Status != "ok" {
				msg := tk.Message
				if tk.Details.Error != "" {
					msg = tk.Details.Error
				}
				results = append(results, Result{Channel: "expo", Token: d.Token, OK: false, Err: errors.New("expo: " + msg)})
				continue
			}
			results = append(results, Result{Channel: "expo", Token: d.Token, TicketID: tk.ID, OK: true})
		}
	}
	return results, nil
}

// postBatch sends one batch and returns its tickets.
func (e *Expo) postBatch(ctx context.Context, msgs []expoMessage) ([]expoTicket, error) {
	body, err := json.Marshal(msgs)
	if err != nil {
		return nil, fmt.Errorf("marshalling push batch: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url(expoPushURL), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building push request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+e.AccessToken)
	}
	resp, err := e.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("posting push batch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("expo push: unexpected status %d", resp.StatusCode)
	}
	var out struct {
		Data []expoTicket `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding push response: %w", err)
	}
	return out.Data, nil
}

// Receipts maps each ticket ID to its terminal error code, or to the empty
// string when the push was delivered. Unknown ticket IDs are simply absent
// from the result. Requests are chunked at expoReceiptBatchSize, mirroring
// how Send chunks at expoBatchSize: a whole-chunk transport failure is
// reported as an error, but earlier successfully-fetched chunks are still
// merged into the returned map rather than discarded, since a caller
// reconciling receipts benefits from a partial answer over none at all.
func (e *Expo) Receipts(ctx context.Context, ticketIDs []string) (map[string]string, error) {
	codes := map[string]string{}
	for start := 0; start < len(ticketIDs); start += expoReceiptBatchSize {
		end := min(start+expoReceiptBatchSize, len(ticketIDs))
		batch, err := e.postReceiptsBatch(ctx, ticketIDs[start:end])
		if err != nil {
			return codes, fmt.Errorf("fetching receipts batch %d-%d: %w", start, end, err)
		}
		for id, code := range batch {
			codes[id] = code
		}
	}
	return codes, nil
}

// postReceiptsBatch fetches receipts for one chunk of ticket IDs, already
// within Expo's per-request cap.
func (e *Expo) postReceiptsBatch(ctx context.Context, ticketIDs []string) (map[string]string, error) {
	if len(ticketIDs) == 0 {
		return map[string]string{}, nil
	}
	body, err := json.Marshal(map[string]any{"ids": ticketIDs})
	if err != nil {
		return nil, fmt.Errorf("marshalling receipts request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url(expoReceiptURL), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building receipts request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+e.AccessToken)
	}
	resp, err := e.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching receipts: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("expo receipts: unexpected status %d", resp.StatusCode)
	}
	var out struct {
		Data map[string]struct {
			Status  string `json:"status"`
			Details struct {
				Error string `json:"error"`
			} `json:"details"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding receipts: %w", err)
	}
	codes := make(map[string]string, len(out.Data))
	for id, r := range out.Data {
		if r.Status == "ok" {
			codes[id] = ""
			continue
		}
		codes[id] = r.Details.Error
	}
	return codes, nil
}

// url returns the override BaseURL when set, otherwise the hosted endpoint.
// Tests point BaseURL at an httptest server, which serves both paths.
func (e *Expo) url(hosted string) string {
	if e.BaseURL != "" {
		return e.BaseURL
	}
	return hosted
}

func (e *Expo) client() *http.Client {
	if e.HTTP != nil {
		return e.HTTP
	}
	return &http.Client{Timeout: expoTimeout}
}
