package mcp

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerResources adds the single item resource template (the archived body
// as plain text, so clients can attach an item without a tool call) and the
// find_and_summarise prompt.
func registerResources(s *mcp.Server, b Backend, uidFor func(context.Context) uuid.UUID) {
	s.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "item",
		Title:       "Saved item body",
		URITemplate: "openmind://item/{id}",
		MIMEType:    "text/plain",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		raw := strings.TrimPrefix(req.Params.URI, "openmind://item/")
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		it, err := b.GetItem(ctx, uidFor(ctx), id)
		if err != nil {
			if isNotFound(err) {
				return nil, mcp.ResourceNotFoundError(req.Params.URI)
			}
			return nil, err
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI:      req.Params.URI,
			MIMEType: "text/plain",
			Text:     it.Body,
		}}}, nil
	})

	s.AddPrompt(&mcp.Prompt{
		Name:        "find_and_summarise",
		Description: "Search the Openmind library for a topic, read the best match, and summarise it.",
		Arguments:   []*mcp.PromptArgument{{Name: "query", Description: "what to look for", Required: true}},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		q := req.Params.Arguments["query"]
		return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{{
			Role: "user",
			Content: &mcp.TextContent{Text: "Search my Openmind library for: " + q +
				". Use the search_items tool, pick the most relevant result, fetch it with get_item, and give me a short summary with the original link."},
		}}}, nil
	})
}
