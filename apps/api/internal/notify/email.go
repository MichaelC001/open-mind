package notify

import (
	"context"

	"github.com/rohithgilla12/openmind/api/internal/mailer"
)

// emailSender delivers notifications as plain-text e-mail over the existing
// SMTP mailer. It is deliberately plain: the rich HTML digest e-mail is sent
// by the digest job on its own path and is untouched by the substrate.
type emailSender struct {
	m mailer.Mailer
}

// NewEmail returns a Sender that delivers over m.
func NewEmail(m mailer.Mailer) Sender { return &emailSender{m: m} }

func (*emailSender) Name() string { return "email" }

// Send delivers to t.Email. A transport failure is reported as a failed
// Result rather than an error return, so one channel failing never aborts the
// fan-out to the other.
func (s *emailSender) Send(ctx context.Context, n Notification, t Target) ([]Result, error) {
	if t.Email == "" {
		return nil, nil
	}
	err := s.m.Send(ctx, mailer.Message{To: t.Email, Subject: n.Title, BodyText: n.Body})
	res := Result{Channel: "email", OK: err == nil, Err: err}
	return []Result{res}, nil
}
