package mcp_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestItemResourceRead(t *testing.T) {
	sess := connect(t, &fakeBackend{})

	tmpls, err := sess.ListResourceTemplates(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tmpl := range tmpls.ResourceTemplates {
		if tmpl.URITemplate == "openmind://item/{id}" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected openmind://item/{id} template, got %+v", tmpls.ResourceTemplates)
	}

	id := uuid.New()
	res, err := sess.ReadResource(context.Background(), &sdk.ReadResourceParams{URI: "openmind://item/" + id.String()})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(res.Contents) != 1 {
		t.Fatalf("expected one content, got %+v", res.Contents)
	}
	c := res.Contents[0]
	if c.Text != "hello body" {
		t.Fatalf("unexpected body: %q", c.Text)
	}
	if c.MIMEType != "text/plain" {
		t.Fatalf("unexpected mime type: %q", c.MIMEType)
	}
}

func TestItemResourceNotFound(t *testing.T) {
	sess := connect(t, &fakeBackend{notFound: true})
	id := uuid.New()
	_, err := sess.ReadResource(context.Background(), &sdk.ReadResourceParams{URI: "openmind://item/" + id.String()})
	if err == nil {
		t.Fatal("expected error for not-found item")
	}
}

func TestFindAndSummarisePrompt(t *testing.T) {
	sess := connect(t, &fakeBackend{})

	prompts, err := sess.ListPrompts(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range prompts.Prompts {
		if p.Name == "find_and_summarise" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected find_and_summarise prompt, got %+v", prompts.Prompts)
	}

	res, err := sess.GetPrompt(context.Background(), &sdk.GetPromptParams{
		Name:      "find_and_summarise",
		Arguments: map[string]string{"query": "go performance"},
	})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	if len(res.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	tc, ok := res.Messages[0].Content.(*sdk.TextContent)
	if !ok {
		t.Fatalf("expected text content, got %T", res.Messages[0].Content)
	}
	if !strings.Contains(tc.Text, "go performance") {
		t.Fatalf("expected text to contain query, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "search_items") || !strings.Contains(tc.Text, "get_item") {
		t.Fatalf("expected text to mention search_items and get_item, got %q", tc.Text)
	}
}
