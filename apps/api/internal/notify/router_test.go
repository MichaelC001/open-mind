package notify

import (
	"context"
	"errors"
	"testing"
)

func TestRouterFansOutToBothChannels(t *testing.T) {
	push, email := NewFake(), NewFake()
	push.ChannelName, email.ChannelName = "expo", "email"
	r := NewRouter(push, email)

	results := r.Deliver(context.Background(), Notification{Title: "hi"},
		Channels{Push: true, Email: true},
		Target{Devices: []Device{{Token: "t"}}, Email: "a@b.c"})

	if len(push.Sent) != 1 {
		t.Errorf("push calls = %d, want 1", len(push.Sent))
	}
	if len(email.Sent) != 1 {
		t.Errorf("email calls = %d, want 1", len(email.Sent))
	}
	if len(results) != 2 {
		t.Errorf("results = %d, want 2", len(results))
	}
}

func TestRouterRespectsDisabledChannel(t *testing.T) {
	push, email := NewFake(), NewFake()
	r := NewRouter(push, email)

	r.Deliver(context.Background(), Notification{Title: "hi"},
		Channels{Push: true}, Target{Devices: []Device{{Token: "t"}}, Email: "a@b.c"})

	if len(email.Sent) != 0 {
		t.Errorf("email was called %d times despite being disabled", len(email.Sent))
	}
	if len(push.Sent) != 1 {
		t.Errorf("push calls = %d, want 1", len(push.Sent))
	}
}

// One channel erroring must not stop the other, and must surface as a failed
// Result so the ledger records why.
func TestRouterPartialFailure(t *testing.T) {
	push, email := NewFake(), NewFake()
	push.ChannelName, email.ChannelName = "expo", "email"
	push.Err = errors.New("expo down")
	r := NewRouter(push, email)

	results := r.Deliver(context.Background(), Notification{Title: "hi"},
		Channels{Push: true, Email: true},
		Target{Devices: []Device{{Token: "t"}}, Email: "a@b.c"})

	if len(email.Sent) != 1 {
		t.Fatalf("email calls = %d, want 1 despite push failing", len(email.Sent))
	}
	var failed, ok int
	for _, res := range results {
		if res.OK {
			ok++
		} else {
			failed++
		}
	}
	if failed != 1 || ok != 1 {
		t.Errorf("results = %+v, want one ok and one failed", results)
	}
}

func TestRouterNoChannelsEnabled(t *testing.T) {
	push, email := NewFake(), NewFake()
	r := NewRouter(push, email)
	results := r.Deliver(context.Background(), Notification{Title: "hi"}, Channels{}, Target{Email: "a@b.c"})
	if len(results) != 0 {
		t.Errorf("results = %+v, want none", results)
	}
	if len(push.Sent)+len(email.Sent) != 0 {
		t.Error("a sender was called with no channels enabled")
	}
}
