package notify

import "context"

// Fake is a recording Sender for tests, mirroring ai.Fake. It performs no I/O
// and captures what it was asked to deliver so callers can assert fan-out.
type Fake struct {
	ChannelName string
	Sent        []Notification
	Targets     []Target
	Err         error
}

// NewFake returns a recording Sender named "fake".
func NewFake() *Fake { return &Fake{ChannelName: "fake"} }

func (f *Fake) Name() string { return f.ChannelName }

// Send records the call. When Err is set it is returned unchanged, which lets
// tests exercise the router's partial-failure path.
func (f *Fake) Send(_ context.Context, n Notification, t Target) ([]Result, error) {
	f.Sent = append(f.Sent, n)
	f.Targets = append(f.Targets, t)
	if f.Err != nil {
		return nil, f.Err
	}
	return []Result{{Channel: f.ChannelName, OK: true}}, nil
}
