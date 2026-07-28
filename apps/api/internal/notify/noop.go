package notify

import (
	"context"
	"log/slog"
)

// noopSender logs and succeeds. It is what runs when NOTIFY_CHANNELS is unset,
// and it is what keeps the app fully functional with no delivery configured:
// producers keep enqueueing, the flush keeps stamping, nothing is sent, and
// nothing errors.
type noopSender struct{}

// NewNoop returns a Sender that delivers nothing and always succeeds.
func NewNoop() Sender { return noopSender{} }

func (noopSender) Name() string { return "noop" }

func (noopSender) Send(_ context.Context, n Notification, _ Target) ([]Result, error) {
	slog.Debug("notify(noop): dropping notification", "category", n.Category, "title", n.Title)
	return nil, nil
}
