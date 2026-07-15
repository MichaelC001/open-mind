# Contributing to Openmind

## Dev setup

- Prereqs: Go 1.25+, Node 20+ with pnpm, Docker, [Task](https://taskfile.dev).
- `docker compose up -d db` then `task dev` runs Postgres, the Go API with
  live reload, and the web app.
- `task --list` shows everything else. `task test` and `task lint` must be
  green before a PR.

## The contract workflow (the most important convention)

`openapi.yaml` is the spine. To change or add an endpoint:

1. Edit `openapi.yaml`.
2. Run `task generate` (regenerates the Go server, the TS client, and
   sqlc).
3. Implement the Go handler; update consumers against the regenerated
   client.

Never hand-edit generated code (`packages/api-client`, sqlc output,
oapi-codegen output). Never hand-write API types in TypeScript, and never
add a Go route that isn't in the spec.

## Ground rules

- Capture is sacred: save paths return instantly; enrichment is always
  async.
- Postgres is the only required infrastructure — never add a required
  service (no Redis, no Python sidecars); optional integrations go behind
  config.
- Every table has `user_id`; every store query is scoped by it.
- All AI goes through the provider adapter chain; the `noop` provider must
  always keep the app fully functional. Only budget model tiers belong in
  the enrichment pipeline — never a flagship model.
- Go: standard library first, errors wrapped with `%w`, queries via sqlc
  only (no inline SQL in handlers or jobs).
- Tests: table-driven Go tests; store and pipeline tests run against real
  Postgres, not mocks; every enrichment job needs an idempotency test (run
  twice, same result).

## Sign-off (DCO)

Contributions are accepted under the
[Developer Certificate of Origin](https://developercertificate.org). Every
commit must carry a `Signed-off-by` line matching the commit author — add it
with:

```bash
git commit -s
```

By signing off you certify that you wrote the change (or otherwise have the
right to submit it) under the project's licence, AGPL-3.0. There is no CLA
and nothing to sign up for; the sign-off line is the whole ceremony. PRs
with unsigned commits will be asked to rebase with `git rebase --signoff`.

## PRs

Small, focused PRs with tests. Describe what and why, and link the issue.
