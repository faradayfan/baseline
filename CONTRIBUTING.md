# Contributing to Baseline

Thanks for your interest in contributing! This guide covers how to get a change
landed.

## Development

Baseline is a Go service (1.26.4, pinned via [asdf](https://asdf-vm.com) in
`.tool-versions`) backed by Postgres + pgvector. See [RUNNING.md](RUNNING.md) for
local setup and [docs/SPEC.md](docs/SPEC.md) for the architecture.

```bash
go build ./...
go test -short ./...      # unit tests, no Docker
go test ./...             # full suite incl. integration (needs Docker for the pgvector testcontainer)
```

## Before you open a PR

CI enforces these, so save yourself a round-trip and run them locally:

- **Format:** `gofmt -l .` must be empty.
- **Lint:** `golangci-lint run` (config in `.golangci.yml`) must be clean.
- **Tests:** `go test ./...` passes — including the `test/conformance` suite that
  asserts the §14 invariants against a live server.
- **Security:** `govulncheck ./...` and `grype dir:.` surface no High/Critical
  findings.

### Test-driven bug fixes

When fixing a bug, **write a failing test that demonstrates it first**, then make
it pass. (This is a governed project rule, not just a suggestion.)

## Commits & PRs

- **Conventional Commits** are required — e.g. `feat(facts): …`, `fix(mcp): …`,
  `docs(readme): …`. Releases and the changelog are generated from commit history
  by [Release Please](https://github.com/googleapis/release-please), so the
  format matters.
- Keep PRs focused. Every feature ships with **unit + integration tests** (see
  [TESTING.md](TESTING.md)).
- `main` is protected: PRs require the CI checks to pass before merging.

## Releases

Releases are automated — do **not** hand-tag versions. Merging the Release Please
PR cuts a GitHub Release and publishes versioned, multi-arch container images +
the Helm chart to GHCR and binaries to the Release.

## Architecture decisions

The decisions in [docs/SPEC.md](docs/SPEC.md) §18 are settled and load-bearing
(canonical identity, separation of duties, append-only audit, the `/context`
precedence model). If a change touches these, explain why in the PR — the
conformance suite exists to catch regressions.
