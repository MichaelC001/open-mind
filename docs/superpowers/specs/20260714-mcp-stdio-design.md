# MCP stdio transport — design

Date: 2026-07-14. Scope: the `openmind mcp` subcommand — the stdio-transport
portion of the TODO "MCP fast-follows" bundle. Companion spec:
`20260714-mcp-tools-v2-design.md` (tools v2 lands first; this transport
serves whatever the shared registry exposes).

## Goal

A co-located self-host (Claude Code/Desktop running on the same machine as
the Openmind binary/DB) can use MCP without going through HTTP or minting a
device key: `claude mcp add openmind -- openmind mcp`.

## Decision (user-confirmed)

**Single-user only.** `openmind mcp` connects directly to Postgres and acts
as the auto-provisioned single-user account. When the instance is in Clerk
multi-user mode (`AUTH_MODE=clerk`), it exits non-zero at startup with:
`stdio transport is single-user only — use the HTTP transport at /mcp with a
device key`. No `omk_` key resolution, no per-user flag.

## Changes

### `cmd/openmind`

- New subcommand `mcp` alongside `serve|work|all|migrate`. It:
  1. loads config exactly like `serve` (same env vars, same DB pool);
  2. refuses to start in Clerk mode (message above);
  3. resolves the single-user account id (the same auto-provision lookup
     `serve` uses for token mode);
  4. builds the same `internal/mcp` server (tools, resources, prompts) over
     a Backend, with `uidFor` returning that fixed user id;
  5. runs the SDK's stdio transport (`mcp.NewStdioTransport` /
     `server.Run(ctx, transport)` per go-sdk v1.6.1 API) until stdin closes
     or SIGINT/SIGTERM.
- Logging goes to stderr only (stdout is the JSON-RPC channel); reuse the
  existing slog setup pointed at stderr.

### Backend construction

The HTTP path implements `mcp.Backend` on `*api.Server`. The stdio command
must not spin up the HTTP server, so the Backend construction is factored
so both paths share it: extract the `mcpBackend` adapter's dependencies
(store, search, capture helper, river client) into a constructor callable
without the HTTP router. Enrichment enqueue still works — `save_item` from
stdio queues River jobs exactly as HTTP does (the `mcp` process embeds a
River client but does not run workers; a separate `openmind work`/`all`
process processes the queue, and the doc says so).

### Docs

`docs/self-hosting.md` MCP section gains a "Local (stdio)" subsection:
`claude mcp add openmind -- openmind mcp` (or the docker-compose exec
variant `docker compose exec -T api openmind mcp`), the single-user-only
caveat, and the note that enrichment of stdio saves requires the worker to
be running.

## Out of scope

Multi-user stdio (device-key resolution), running workers inside the `mcp`
process, Windows service packaging.

## Testing

- Unit: subcommand wiring test that `mcp` in Clerk mode exits with the
  documented error; `uidFor` returns the provisioned user.
- Integration (DB-backed): drive the stdio transport in-process via the
  SDK's in-memory/pipe transport pair — `initialize`, `tools/list` (same
  registry as HTTP), `save_item` creates a pending row and a River job.
- Manual e2e: `claude mcp add` against a local binary, one tool call.
