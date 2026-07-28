package main

import (
	"errors"
	"testing"

	"github.com/rohithgilla12/openmind/api/internal/mailer"
	"github.com/rohithgilla12/openmind/api/internal/notify"
)

func TestStdioRefusesClerkMode(t *testing.T) {
	t.Setenv("AUTH_MODE", "clerk")
	t.Setenv("CLERK_ISSUER", "https://clerk.example.com")
	err := checkStdioAuthMode()
	if err == nil {
		t.Fatal("want error in clerk mode")
	}
	if !errors.Is(err, errStdioSingleUser) {
		t.Fatalf("err = %v, want errStdioSingleUser", err)
	}
	if err.Error() != "stdio transport is single-user only — use the HTTP transport at /mcp with a device key" {
		t.Fatalf("message = %q", err.Error())
	}
}

func TestStdioAllowsTokenMode(t *testing.T) {
	t.Setenv("AUTH_MODE", "")
	if err := checkStdioAuthMode(); err != nil {
		t.Fatalf("token mode should be allowed: %v", err)
	}
}

// TestBuildNotifyDepsRouterLiveTruthTable replaces the old Configured-based
// truth table (Configured was deleted along with NotifyDeps.Configured as
// part of the C1 fix — a global flag can't answer a per-channel question).
// What must still hold for every combination of requested channels and SMTP
// availability is that Router.Live(Channels{Push: true, Email: true}) — the
// per-message question the flush job actually asks — reports exactly which
// channels ended up backed by a real sender.
func TestBuildNotifyDepsRouterLiveTruthTable(t *testing.T) {
	smtpMailer := mailer.New(mailer.SMTPConfig{Host: "smtp.example.com", From: "notify@example.com"})

	cases := []struct {
		name      string
		channels  string
		mailer    mailer.Mailer
		wantPush  bool
		wantEmail bool
	}{
		{"unset, no smtp", "", nil, false, false},
		{"unset, smtp configured", "", smtpMailer, false, false},
		{"expo only", "expo", nil, true, false},
		{"email requested, no smtp", "email", nil, false, false},
		{"email requested, smtp configured", "email", smtpMailer, false, true},
		{"expo and email, no smtp", "expo,email", nil, true, false},
		{"expo and email, smtp configured", "expo,email", smtpMailer, true, true},
		{"unknown channel only", "carrier-pigeon", nil, false, false},
		{"whitespace and empty entries", " expo , , ", nil, true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NOTIFY_CHANNELS", tc.channels)
			deps := buildNotifyDeps(tc.mailer)
			if deps.Router == nil {
				t.Fatal("Router must never be nil, even with nothing configured")
			}
			live := deps.Router.Live(notify.Channels{Push: true, Email: true})
			if live.Push != tc.wantPush || live.Email != tc.wantEmail {
				t.Fatalf("Live = %+v, want {Push:%v Email:%v}", live, tc.wantPush, tc.wantEmail)
			}
		})
	}
}

func TestBuildNotifyDepsWiresExpoAsReceiptChecker(t *testing.T) {
	t.Setenv("NOTIFY_CHANNELS", "expo")
	deps := buildNotifyDeps(nil)
	if deps.Receipts == nil {
		t.Fatal("want Receipts wired when the expo channel is enabled")
	}
}

func TestBuildNotifyDepsNoExpoNoReceiptChecker(t *testing.T) {
	t.Setenv("NOTIFY_CHANNELS", "email")
	deps := buildNotifyDeps(mailer.New(mailer.SMTPConfig{Host: "smtp.example.com", From: "notify@example.com"}))
	if deps.Receipts != nil {
		t.Fatal("want no Receipts checker when the expo channel is disabled")
	}
}
