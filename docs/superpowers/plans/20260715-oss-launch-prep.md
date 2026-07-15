# OSS-Launch Prep Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The repo is ready to flip public: scrubbed tree, AGPL-3.0 + README/CONTRIBUTING/SECURITY, name-collision report, single-commit public history (old history archived privately), backlog filed as Issues, and a manual flip checklist.

**Architecture:** Six sequential work items on a branch until Task 4, which rewrites `main` itself (orphan root force-push) after archiving full history outside the repo. Prep-only: repo visibility is never changed.

**Tech Stack:** gitleaks, grep, git bundle / orphan branch, `gh` CLI, web research (name sweep).

**Spec:** `docs/superpowers/specs/20260715-oss-launch-prep-design.md`

## Global Constraints

- **Prep-only: never change repo visibility.**
- Licence is **AGPL-3.0**, text verbatim from gnu.org.
- No history may survive inside this repo after Task 4 (any old-history branch would leak at flip); archives live in a local bundle + a separate private cold-storage repo.
- The force-push in Task 4 happens only after the archive is verified restorable and the user confirms.
- Sensitive patterns to be absent from the final tree AND final history: IPv4 addresses (except 127.0.0.1/0.0.0.0/example ranges), the maintainer's real domain/host strings, ssh host strings, personal/work e-mails, `omk_` tokens, Clerk instance IDs/issuer URLs, the maintainer's org name. Exact strings live in the (gitignored) task brief, not in this tracked plan.
- Don't use mymind's taglines/copy; referring to it as inspiration is fine.
- No banner-style comment blocks.

---

### Task 1: Sensitive-content scrub of the working tree

**Files:**
- Modify: `TODO.md` (full rewrite), any `docs/superpowers/specs/*.md` / `docs/superpowers/plans/*.md` with infra references, possibly `docs/*.md`
- No source-code changes expected (verify, don't assume)

**Interfaces:** Produces a clean tree that Task 2 builds on and Task 4 snapshots.

- [ ] **Step 1: Run the scanners and build the offender list**

From the repo root:

```bash
gitleaks detect --source . --no-git -v 2>&1 | tail -20
git ls-files | xargs grep -lE "([0-9]{1,3}\.){3}[0-9]{1,3}" | grep -v -E "lock|\.sum" || true
git ls-files | xargs grep -lE "<maintainer-domain>|<maintainer-ip-prefix>|<maintainer-org>|omk_[A-Za-z0-9]|clerk\.[a-z]+\.[a-z]+|@gmail|@<maintainer-org>|ubuntu@" || true  # see task brief for the literal patterns
```

Triage every hit: localhost/example IPs and lockfile noise are fine; real infra/e-mail/token strings are offenders. Record the list in the task report.

- [ ] **Step 2: Rewrite TODO.md**

Replace the whole file with a short public-safe tracker:

```markdown
# TODO

> Lightweight maintainer notes. The real backlog lives in
> [GitHub Issues](../../issues) — file bugs and feature requests there.

## Now
- (empty)

## Next
- (see Issues)

## Later
- Lossless AVIF metadata stripping / re-allow AVIF uploads
- Send to Kindle follow-ups: per-user Kindle address, EPUB covers, in-article images, scheduled Lens digests
- Omnivore import slice B: ingest archived content from export zips so dead links survive
```

- [ ] **Step 3: Scrub docs**

For each offender file from Step 1 under `docs/`: replace concrete hostnames/IPs/accounts with neutral placeholders (`<your-server>`, `<your-domain>`, `you@example.com`) where the sentence is design-relevant; delete the sentence/file where it is purely operational (incident notes, deploy runbooks tied to the maintainer's box). Keep design content intact. `docs/self-hosting.md` and `.env.example` must end up using only example values.

- [ ] **Step 4: Verify .gitignore covers local-only dirs**

`.gitignore` must include (add any missing): `.claude/`, `.agents/`, `.superpowers/`, `.playwright-mcp/`, `.task/`, `skills-lock.json`, `apps/mobile/.claude/`, `apps/mobile/.agents/`, `apps/mobile/skills-lock.json`. Confirm `git status` shows none of them as tracked (`git ls-files | grep -E "^\.claude|^\.agents|skills-lock"` → empty).

- [ ] **Step 5: Re-run the scanners — must be clean**

Re-run Step 1's three commands. Expected: no findings beyond triaged-as-fine noise. Record output.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "chore: scrub private operational detail for public release"
```

---

### Task 2: Launch files (LICENSE, README, CONTRIBUTING, SECURITY)

**Files:**
- Create: `LICENSE`, `README.md`, `CONTRIBUTING.md`, `SECURITY.md`

**Interfaces:** Consumes the scrubbed tree; README links `docs/self-hosting.md` and `docs/PRD.md`.

- [ ] **Step 1: LICENSE**

Download the canonical text: `curl -fsSL https://www.gnu.org/licenses/agpl-3.0.txt -o LICENSE` (verify it starts with "GNU AFFERO GENERAL PUBLIC LICENSE" and is ~34k chars; if the network path is untrustworthy, verify against a second source).

- [ ] **Step 2: README.md**

Write the root README with these sections and this substance (tune wording, keep claims accurate to the shipped feature set — check docs/self-hosting.md for the authoritative list):

```markdown
# Openmind

Your self-hosted commonplace book: save anything — links, notes, images,
quotes, PDFs — and find it again by fragments: a colour, a keyword, a vibe.
AI enriches every save in the background; the app works fully without any
AI configured.

## Features
- **Capture from anywhere**: web app, browser extension, iOS/Android share
  sheet, macOS menu-bar dock, plain HTTP API. Saves return instantly —
  enrichment is always async.
- **Find by fragments**: hybrid full-text + semantic search, colour search,
  natural-language queries ("cozy green cabins").
- **Lenses**: saved searches that stay live. **Desk**: a pinboard.
  **Drift**: a calm daily resurfacing ritual for forgotten saves.
- **Bring your library**: import browser bookmarks, Pocket/Raindrop CSV,
  Omnivore zips; subscribe to RSS/Atom feeds; save PDFs with full-text
  extraction.
- **Send to Kindle**, distraction-free reader, JSON export.
- **MCP server**: your AI agents (Claude etc.) can search, save, tag, pin —
  over HTTP or local stdio.
- **Self-hosting is the product**: Postgres + one Go binary. `docker compose
  up` is the whole deployment. AI is pluggable (Gemini, OpenAI-compatible,
  none) and never required.

## Quickstart
    git clone <repo> && cd open-mind
    docker compose up -d
    open http://localhost:3000
See [docs/self-hosting.md](docs/self-hosting.md) for configuration, auth,
AI providers, and everything else.

## Repo layout
(condensed table: apps/api Go, apps/web Next.js, apps/extension WXT,
apps/mobile Expo, apps/dock Tauri, packages/api-client generated,
openapi.yaml is the contract)

## Contributing
See [CONTRIBUTING.md](CONTRIBUTING.md). The one rule to know:
`openapi.yaml` is the source of truth — change it, run `task generate`,
then implement.

## Licence
AGPL-3.0 — see [LICENSE](LICENSE).

Openmind is an independent project, inspired by (but not affiliated with)
mymind.
```

- [ ] **Step 3: CONTRIBUTING.md**

```markdown
# Contributing to Openmind

## Dev setup
- Prereqs: Go 1.25+, Node 20+ with pnpm, Docker, [Task](https://taskfile.dev).
- `docker compose up -d db` then `task dev` runs Postgres, the Go API with
  live reload, and the web app.
- `task --list` shows everything else. `task test` and `task lint` must be
  green before a PR.

## The contract workflow (the most important convention)
`openapi.yaml` is the spine. To change or add an endpoint:
1. Edit `openapi.yaml`
2. `task generate` (regenerates the Go server + TS client + sqlc)
3. Implement the Go handler; update consumers against the regenerated client
Never hand-edit generated code (`packages/api-client`, sqlc output,
oapi-codegen output). Never hand-write API types in TS.

## Ground rules
- Capture is sacred: save paths return instantly; enrichment is async.
- Postgres is the only required infrastructure — no new required services.
- Every table has `user_id`; every store query is scoped by it.
- All AI goes through the provider adapter; the `noop` provider must keep
  the app fully functional.
- Go: stdlib first, errors wrapped with %w, queries via sqlc only.
- Tests: table-driven Go tests; store/pipeline tests run against real
  Postgres; enrichment jobs need an idempotency test.

## PRs
Small, focused PRs with tests. Describe what and why; link the issue.
```

- [ ] **Step 4: SECURITY.md**

```markdown
# Security policy

Please report vulnerabilities privately via GitHub's
["Report a vulnerability"](../../security/advisories/new) form — do not open
a public issue. You'll get an acknowledgement within a few days. Openmind is
a self-hosted product: reports about default-configuration weaknesses are
especially welcome.
```

- [ ] **Step 5: Sanity-check quickstart + commit**

Confirm `docker compose up -d` + `http://localhost:3000` matches reality (the stack is likely already running — `docker ps`). Fix the README if not.

```bash
git add LICENSE README.md CONTRIBUTING.md SECURITY.md
git commit -m "docs: OSS launch files — AGPL-3.0, README, CONTRIBUTING, SECURITY"
```

---

### Task 3: Name-collision sweep (research)

**Files:**
- Create: `docs/20260715-openmind-name-sweep.md`

**Interfaces:** Standalone research deliverable; Task 6's checklist references its conclusion.

- [ ] **Step 1: Research**

Web searches (use a research-capable subagent): `"OpenMind" AI company`, `"OpenMind" software`, `openmind site:github.com`, `openmind PyPI`, `openmind npm`, `"open mind" bookmark OR "read later" app`, USPTO TESS quick search for OPENMIND in software classes (9/42), EUIPO eSearch equivalent. Capture: who, what space, how active, likely confusion with a self-hosted commonplace book.

- [ ] **Step 2: Write the report**

`docs/20260715-openmind-name-sweep.md` with: methodology, findings table (name, org, space, activity, collision risk high/med/low), overall risk read, and a recommendation among: keep as-is / keep with qualifier ("Openmind — the self-hosted commonplace book") / rename (with 2-3 candidate names checked for availability). Explicitly note this is research, not legal advice, and no rename decision is made here.

- [ ] **Step 3: Commit**

```bash
git add docs/20260715-openmind-name-sweep.md
git commit -m "docs: Openmind name-collision sweep"
```

---

### Task 4: History squash (CONTROLLER-RUN — destructive, gated)

**Files:** none in-tree; operates on git itself. Runs on `main` after Tasks 1-3 are merged.

- [ ] **Step 0: Working tree must be clean**

`git status --porcelain` must be empty before the orphan snapshot — the repo currently carries the user's uncommitted `apps/dock` changes and `go.work.sum`; they must be committed (or explicitly stashed by the user) first, otherwise `git add -A` in Step 3 silently bakes them into the public root. Ask the user which.

- [ ] **Step 1: Archive full history (both copies, verify before proceeding)**

```bash
git bundle create ~/openmind-history-20260715.bundle --all
git clone ~/openmind-history-20260715.bundle /tmp/openmind-restore-test && git -C /tmp/openmind-restore-test log --oneline | wc -l   # must match current main depth
gh repo create Rohithgilla12/open-mind-history-archive --private -y
git push --mirror git@github.com:Rohithgilla12/open-mind-history-archive.git
```

All three must succeed (restore-test count sane, mirror push accepted) before Step 2.

- [ ] **Step 2: USER GATE**

Show the user: bundle path + restore-test result + archive repo URL, and the exact force-push about to happen. Proceed only on explicit confirmation.

- [ ] **Step 3: Orphan root + force-push**

```bash
git checkout --orphan public-root
git add -A
git commit -m "Openmind — initial public release"
git branch -M public-root main
git push --force origin main
```

- [ ] **Step 4: Verify**

```bash
git log --oneline | wc -l        # = 1
gitleaks detect --source . -v 2>&1 | tail -5   # over the new (1-commit) history: clean
gh run list --limit 1            # CI triggered on the new head; confirm it goes green
```

Also delete any other remote branches left on origin (`git push origin --delete <branch>` for each in `git branch -r`) — none may carry old history.

---

### Task 5: Issues graduation (CONTROLLER-RUN)

- [ ] **Step 1: File the backlog via gh**

On the still-private repo, one issue per item (`gh issue create --title ... --body ...`):

1. "Lossless AVIF metadata stripping (re-allow AVIF uploads)" — body: currently 415s at upload pending a metadata-strip implementation; label `enhancement`.
2. "Kindle follow-ups: per-user address, EPUB covers, in-article images, scheduled digests" — label `enhancement`.
3. "Omnivore import slice B: ingest archived content/*.html so dead links survive" — body: needs an import-with-body capture path; label `enhancement`.
4. "Trusted-proxy configuration for rate limiting (X-Forwarded-For trust)" — body: clientIP trusts XFF unconditionally; fine behind a proxy, spoofable when directly exposed; label `security`, `enhancement`.
5. "Feed polling: per-feed intervals / Cache-Control awareness" — label `enhancement`, `good first issue`.

Create the labels first if missing (`gh label create security` etc. — `enhancement` exists by default).

- [ ] **Step 2: Confirm TODO.md's Issues link resolves** (it's relative — `../../issues` works on GitHub's UI from the repo root file view).

---

### Task 6: Flip checklist

**Files:**
- Create: `docs/launch-checklist.md`

- [ ] **Step 1: Write the checklist**

```markdown
# Launch checklist (manual, maintainer-only)

Prep (done by the OSS-launch-prep work):
- [x] Tree scrubbed + gitleaks clean (tree and single-commit history)
- [x] AGPL-3.0 LICENSE, README, CONTRIBUTING, SECURITY
- [x] Name sweep: see docs/20260715-openmind-name-sweep.md — decide before announcing
- [x] History squashed to one root; full history in the private archive repo + local bundle
- [x] Backlog filed as Issues

Flip day:
- [ ] Settings → General → Change visibility → Public
- [ ] Enable "Private vulnerability reporting" (Settings → Code security) so SECURITY.md's link works
- [ ] Verify Actions run green on the public repo (billing/permissions can differ)
- [ ] GitHub code-search the fresh repo for the maintainer's real domain, IP prefix, org name, and `omk_` — expect zero hits
- [ ] Confirm the production instance is unaffected (it deploys via rsync, not from GitHub)
- [ ] Decide the name question (sweep report) before any announcement
- [ ] Announce (HN/Reddit/X) — screenshots of The Mind / Desk / Drift render well
```

- [ ] **Step 2: Commit + wrap up**

```bash
git add docs/launch-checklist.md
git commit -m "docs: manual flip checklist for OSS launch"
```

Update the (new, public-safe) TODO.md only if needed; push `main`.
