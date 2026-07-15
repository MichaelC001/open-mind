# Openmind

Your self-hosted commonplace book: save anything — links, notes, images,
quotes, PDFs — and find it again by fragments: a colour, a keyword, a vibe.
AI enriches every save in the background; the app works fully without any
AI configured.

## Features

- **Capture from anywhere**: web app, browser extension, iOS/Android
  share sheet (share-sheet-first mobile app), macOS menu-bar dock (quick
  save + quick find; macOS-only for now), and plain HTTP API. Saves
  return instantly — enrichment is always async.
- **Find by fragments**: hybrid full-text + semantic search, colour
  search, and natural-language queries.
- **Lenses**: saved searches that stay live. **Desk**: a pinboard.
  **Drift**: a calm daily resurfacing ritual for forgotten saves.
- **Bring your library**: import Netscape bookmark exports (browsers,
  Pocket, Raindrop, Pinboard, Instapaper), CSV, a plain list of URLs, or an
  Omnivore zip; subscribe to RSS/Atom feeds; save PDFs with full-text
  extraction.
- **Send to Kindle**, distraction-free reading, JSON export.
- **MCP server**: your AI agents (Claude Desktop, Claude Code, or any MCP
  client) can search, save, tag, and pin — over HTTP or local stdio.
- **Self-hosting is the product**: Postgres + one Go binary. `docker
  compose up` is the whole deployment. AI is pluggable (Gemini, any
  OpenAI-compatible endpoint including local models, or none) and never
  required.

## Quickstart

```bash
git clone <repo> && cd open-mind
docker compose up -d
open http://localhost:3000
```

See [docs/self-hosting.md](docs/self-hosting.md) for configuration, auth,
AI providers, and everything else.

## Repo layout

| Path | Stack | Purpose |
|---|---|---|
| `apps/api` | Go | API + River workers, one binary (`cmd/openmind`: `serve`\|`work`\|`all`\|`migrate`) |
| `apps/web` | Next.js | Web app |
| `apps/extension` | WXT (React) | Browser extension |
| `apps/mobile` | Expo | Share-sheet-first mobile capture |
| `apps/dock` | Tauri | Floating desktop dock (P2) |
| `packages/api-client` | TS, generated | API client generated from `openapi.yaml` — never edit by hand |
| `packages/ui` | TS | Shared React components + design tokens |
| `openapi.yaml` | — | The contract — single source of truth for the API |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). The one rule to know:
`openapi.yaml` is the source of truth — change it, run `task generate`,
then implement.

## Licence

AGPL-3.0 — see [LICENSE](LICENSE).

Openmind is an independent project, inspired by (but not affiliated with)
mymind.
