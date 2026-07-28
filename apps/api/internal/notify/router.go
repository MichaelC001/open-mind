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

// Enabled reports whether any real (non-noop) channel is wired at all. It is
// used only for the startup log in cmd/openmind — whether to warn that no
// channel is configured anywhere. It must never stand in for "the channel
// this message is going out on is real": NOTIFY_CHANNELS names channels
// independently (e.g. "expo" alone leaves email wired to noop), so a global
// answer and a per-message answer diverge exactly when only one channel is
// requested. Live answers the per-message question; use that in the flush job.
func (r *Router) Enabled() bool {
	return (r.Push != nil && r.Push.Name() != "noop") || (r.Email != nil && r.Email.Name() != "noop")
}

// Live masks ch down to the channels actually backed by a real (non-noop)
// sender. A channel the user enabled in their preferences but that the
// server wired to noop (because NOTIFY_CHANNELS didn't name it, or SMTP
// wasn't configured) must be treated as "nothing configured" for this
// message — not as a delivery attempt that mysteriously produced no results.
// This is what deliverOne must check instead of a single global "some channel
// somewhere is real" bool, which cannot represent a mixed configuration like
// NOTIFY_CHANNELS=expo plus a user whose digest preference is email.
func (r *Router) Live(ch Channels) Channels {
	return Channels{
		Push:  ch.Push && r.Push != nil && r.Push.Name() != "noop",
		Email: ch.Email && r.Email != nil && r.Email.Name() != "noop",
	}
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
