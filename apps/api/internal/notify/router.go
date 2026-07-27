package notify

import "context"

// Router fans one notification out across the channels a user has enabled.
// It owns no state beyond its senders; target resolution and ledger writes
// belong to the caller (the flush job), which keeps senders and the router
// free of store access.
type Router struct {
	Push  Sender
	Email Sender
}

// NewRouter returns a Router over the given senders. Either may be a noop.
func NewRouter(push, email Sender) *Router {
	return &Router{Push: push, Email: email}
}

// Enabled reports whether any real (non-noop) channel is wired. This is
// load-bearing, not cosmetic: cmd/openmind's buildNotifyDeps calls it to set
// NotifyDeps.Configured, and the flush job uses Configured to decide what a
// zero-result delivery means — noop mode, so stamp the row and let the
// outbox drain, versus a real channel that was supposed to deliver and
// didn't, so leave the row pending. Do not inline or simplify this away.
func (r *Router) Enabled() bool {
	return (r.Push != nil && r.Push.Name() != "noop") || (r.Email != nil && r.Email.Name() != "noop")
}

// Deliver sends n over every channel enabled in ch and returns one Result per
// destination attempted. A sender returning an error is converted into a
// single failed Result for its channel rather than aborting the fan-out: one
// channel being down must never suppress the other.
func (r *Router) Deliver(ctx context.Context, n Notification, ch Channels, t Target) []Result {
	var out []Result
	if ch.Push && r.Push != nil {
		out = append(out, collect(ctx, r.Push, n, t)...)
	}
	if ch.Email && r.Email != nil {
		out = append(out, collect(ctx, r.Email, n, t)...)
	}
	return out
}

// collect runs one sender and normalises its outcome into Results.
func collect(ctx context.Context, s Sender, n Notification, t Target) []Result {
	res, err := s.Send(ctx, n, t)
	if err != nil {
		return []Result{{Channel: s.Name(), OK: false, Err: err}}
	}
	return res
}
