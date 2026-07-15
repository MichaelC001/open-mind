package main

import (
	"errors"
	"testing"
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
