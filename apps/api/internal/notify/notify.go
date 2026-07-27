// Package notify delivers user-facing notifications over pluggable channels.
// It mirrors internal/ai: a Sender interface per channel, a noop that keeps
// the app fully functional with nothing configured, and a fake for tests.
//
// Senders never touch the store. The router resolves targets and writes the
// delivery ledger, so a Sender is a pure "given this message and these
// addresses, deliver" adapter.
package notify

import (
	"context"

	"github.com/google/uuid"
)

// Category groups notifications for preference and coalescing purposes.
type Category string

const (
	CategoryDigest    Category = "digest"
	CategoryFeedRiver Category = "feed_river"
	CategoryLifecycle Category = "lifecycle"
)

// Notification is one user-facing message. SourceIDs carries the outbox row
// IDs it was built from — one for a pass-through category, many for a
// coalesced feed-river message — so the flush knows which rows to stamp.
type Notification struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Category  Category
	DedupeKey string
	Title     string
	Body      string
	Data      map[string]any
	SourceIDs []uuid.UUID
}

// Device is one registered push target.
type Device struct {
	Token    string
	Platform string
}

// Target holds the resolved destinations for a single user.
type Target struct {
	Devices []Device
	Email   string
}

// Result is the outcome for one destination. Token is empty for e-mail.
// TicketID is set only by Expo, which reports delivery failures later via a
// separate receipts call.
type Result struct {
	Channel  string
	Token    string
	TicketID string
	OK       bool
	Err      error
}

// Sender delivers notifications over exactly one channel.
type Sender interface {
	Name() string
	Send(ctx context.Context, n Notification, t Target) ([]Result, error)
}
