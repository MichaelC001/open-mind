package notify

import (
	"context"
	"errors"
	"testing"

	"github.com/rohithgilla12/openmind/api/internal/mailer"
)

type stubMailer struct {
	got mailer.Message
	err error
}

func (s *stubMailer) Send(_ context.Context, msg mailer.Message) error {
	s.got = msg
	return s.err
}

func TestEmailSenderComposesMessage(t *testing.T) {
	m := &stubMailer{}
	s := NewEmail(m)
	n := Notification{Title: "12 new items across 3 feeds", Body: "Take a look when you have a minute."}

	results, err := s.Send(context.Background(), n, Target{Email: "reader@example.com"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if m.got.To != "reader@example.com" {
		t.Errorf("To = %q", m.got.To)
	}
	if m.got.Subject != n.Title {
		t.Errorf("Subject = %q, want %q", m.got.Subject, n.Title)
	}
	if m.got.BodyText != n.Body {
		t.Errorf("BodyText = %q", m.got.BodyText)
	}
	if len(results) != 1 || !results[0].OK || results[0].Channel != "email" {
		t.Errorf("results = %+v", results)
	}
}

// No address is not an error: the user simply has no e-mail destination, and
// the notification is still considered handled for this channel.
func TestEmailSenderNoAddressIsNoop(t *testing.T) {
	m := &stubMailer{}
	results, err := NewEmail(m).Send(context.Background(), Notification{Title: "x"}, Target{})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("results = %+v, want none", results)
	}
	if m.got.To != "" {
		t.Errorf("mailer was called with To = %q", m.got.To)
	}
}

func TestEmailSenderRecordsFailure(t *testing.T) {
	m := &stubMailer{err: errors.New("smtp down")}
	results, err := NewEmail(m).Send(context.Background(), Notification{Title: "x"}, Target{Email: "a@b.c"})
	if err != nil {
		t.Fatalf("Send returned err %v; want the failure reported in Result", err)
	}
	if len(results) != 1 || results[0].OK {
		t.Fatalf("results = %+v, want one failed result", results)
	}
	if results[0].Err == nil {
		t.Error("Result.Err is nil, want the smtp error")
	}
}
