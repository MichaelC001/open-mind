# OSS-launch prep — design

Date: 2026-07-15. Scope: make the repo ready to flip public. **Prep-only** —
the repo stays private at the end; the maintainer flips visibility manually
after review.

## Decisions (user-confirmed)

- **Licence: AGPL-3.0** (protects against unmodified SaaS clones; matches the
  closest OSS neighbour's licensing).
- **Prep-only**: no visibility change in this work.
- **Same repo, squash-to-clean-root**: no new repo (CI/secrets stay); git
  history is removed from GitHub by force-pushing an orphan root commit of the
  scrubbed tree. Full history is archived privately first.

## Work items (in order)

### 1. Sensitive-content scrub (working tree)

- gitleaks over the tree + targeted greps: IPv4s, the maintainer's real
  domain, ssh/host strings, e-mail addresses, `omk_`/token patterns, Clerk
  instance IDs.
- `TODO.md`: replaced by a short public-safe tracker (Now/Next/Later pointing
  at GitHub Issues); the 47 KB Done history lives only in the private archive.
- `docs/superpowers/specs/*` and `plans/*`: kept, with infra references
  scrubbed (IPs, hostnames, incident specifics). A file that is inherently
  operational rather than design gets dropped from the tree instead.
- `CLAUDE.md`, `docs/PRD.md`, `docs/self-hosting.md`, `.env.example`:
  verified public-safe (no secrets; example values only). mymind is referred
  to only as inspiration/comparison, never using their copy (existing rule).
- Also verify `.gitignore` covers local-only dirs (`.claude/`, `.agents/`,
  `.superpowers/`, `.playwright-mcp/`, `.task/`, `skills-lock.json`) so they
  can't be committed into the public tree.

### 2. Launch files

- `LICENSE` — GNU AGPL-3.0 text verbatim.
- `README.md` (root) — what Openmind is (self-hostable commonplace book:
  save anything, AI enriches, find by fragments), feature tour, quickstart
  (`docker compose up`), repo layout, link to `docs/self-hosting.md`,
  tech stack, licence note, "independent project, not affiliated with
  mymind" note.
- `CONTRIBUTING.md` — dev setup (Taskfile, compose db), the contract-first
  workflow (openapi.yaml → `task generate`), Go/TS conventions summary,
  testing expectations, PR guidance. Distilled from CLAUDE.md, not a copy.
- `SECURITY.md` — private disclosure via the maintainer's e-mail alias (a
  public-safe address, not the work e-mail).
- No per-file licence headers; no issue templates (YAGNI).

### 3. Name sweep (research deliverable)

- Web + registry sweep for "OpenMind"/"Openmind" collisions in software/AI
  (GitHub, PyPI/npm, company names, USPTO/EUIPO quick search).
- Output `docs/20260715-openmind-name-sweep.md`: findings, risk read,
  recommendation (keep / keep-with-qualifier / rename candidates).
- No rename in this work regardless of outcome — maintainer's decision later.

### 4. History squash (same repo)

1. Archive: `git bundle create ~/openmind-history-<date>.bundle --all` (local,
   outside the repo) **and** mirror-push to a fresh private cold-storage repo.
   No branch inside this repo may carry the old history (it would leak at
   flip).
2. `git checkout --orphan public-root` on the scrubbed tree → single initial
   commit → force-push as `main`.
3. Verify: `git log` shows one root; gitleaks over the new history is clean;
   CI still green on the new head.

### 5. Issues graduation

- After the squash, file the open backlog as GitHub Issues via `gh` on the
  still-private repo (they become public with the flip): Later items (AVIF,
  Kindle follow-ups, Omnivore slice B), known minors worth tracking (XFF
  trusted-proxy config), and the user-side M1 tail if still relevant.
- Public `TODO.md` points at Issues for anything beyond the maintainer's
  private planning.

### 6. Flip checklist

- `docs/launch-checklist.md`: flip visibility → verify Actions run on public
  repo → confirm production instance unaffected → confirm no leaked strings
  via a GitHub code search on the fresh repo → announce. Notes what was
  verified during prep and what only the maintainer can do.

## Out of scope

Flipping the repo public, renaming the project, hosted-offering plans,
issue/PR templates, CLA/DCO.

## Verification

- gitleaks clean over the final tree **and** the new single-commit history.
- Grep sweep (IP/hostname/email/token patterns) clean over every tracked file.
- `task test` + `task lint` green on the new root commit; CI green on push.
- README quickstart followed verbatim on the local compose stack.
- Archive restore test: clone the bundle to a temp dir and confirm the old
  history is intact there.
