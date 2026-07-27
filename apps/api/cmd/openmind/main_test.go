package main

import (
	"errors"
	"testing"

	"github.com/rohithgilla12/openmind/api/internal/mailer"
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

// TestBuildNotifyDepsConfiguredTruthTable is the load-bearing test in this
// file: NotifyDeps.Configured tells the flush job whether a zero-result
// delivery means "nothing is configured, stamp the row anyway" or "a real
// channel should have delivered and didn't, leave it pending". Getting this
// wrong in either direction either silently drops notifications or wedges
// the outbox, so every combination of requested channels and SMTP
// availability is asserted here rather than trusted to a smaller sample.
func TestBuildNotifyDepsConfiguredTruthTable(t *testing.T) {
	smtpMailer := mailer.New(mailer.SMTPConfig{Host: "smtp.example.com", From: "notify@example.com"})

	cases := []struct {
		name     string
		channels string
		mailer   mailer.Mailer
		want     bool
	}{
		{"unset, no smtp", "", nil, false},
		{"unset, smtp configured", "", smtpMailer, false},
		{"expo only", "expo", nil, true},
		{"email requested, no smtp", "email", nil, false},
		{"email requested, smtp configured", "email", smtpMailer, true},
		{"expo and email, no smtp", "expo,email", nil, true},
		{"expo and email, smtp configured", "expo,email", smtpMailer, true},
		{"unknown channel only", "carrier-pigeon", nil, false},
		{"whitespace and empty entries", " expo , , ", nil, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NOTIFY_CHANNELS", tc.channels)
			deps := buildNotifyDeps(tc.mailer)
			if deps.Configured != tc.want {
				t.Fatalf("Configured = %v, want %v", deps.Configured, tc.want)
			}
			if deps.Router == nil {
				t.Fatal("Router must never be nil, even with nothing configured")
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
