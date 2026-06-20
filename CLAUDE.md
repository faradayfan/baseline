# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Current state

**All milestones M0–M6 are complete** (spec §17). The full v1 service is implemented with unit +
integration + API tests, and the `test/conformance` suite asserts all 17 §14 invariants against a
live server — the build is "on baseline." Packages: `platform` (config/log/OTEL), `store`
(pgx+goose), `namespaces`, `rbac` + `server` (auth/RBAC/handlers/MCP-bridge wiring), `facts`,
`audit`, `promotions`, `autopromote` (+`simple`), `memory` (+`mem0`/`null`), `embed`, `contextsvc`,
`mcpbridge`, `metrics`, `reaper`; the contract is `api/openapi.yaml`; the §14 suite is
`test/conformance`. The one entrypoint `cmd/baseline` runs HTTP by default, the reaper under
`BASELINE_REAP=true`, and the MCP bridge under `BASELINE_MCP_STDIO=true`.

What remains is **deferred-by-plan**, not unfinished: Helm packaging (`deploy/`), CI wiring, an OTEL
exporter (instruments exist; production points `OTEL_*` at the collector), real OIDC/mTLS
authenticators (the `Authenticator` seam exists; `HeaderAuthenticator` is dev-only), and semantic
(embedding-ranked) `q` search (`/facts?q=` is substring for now). The plan lives at
`/Users/john/.claude/plans/shiny-jingling-gizmo.md`.

**[docs/SPEC.md](docs/SPEC.md) is the source of truth.** It is a locked, buildable spec (v0.2, all
v1 decisions decided). Read it before implementing anything; the decisions in §18 are settled — do
not relitigate them. Sections below summarize the parts that aren't discoverable from code.

## What Baseline is

A governance layer in front of an agent memory store. Raw per-actor **memories** (in Mem0) get
*promoted* through a reviewed, audited workflow into curated **facts** that live in shared
**namespaces** (`user` / `team` / `project` / `org`). Mem0 answers "what has this agent seen?";
Baseline answers "what does the org officially know, who vouched for it, is it still true?"

It is a stateless Go service. Source of truth for facts and governance state is **its own**
Postgres DB (`baseline`) with pgvector; memories stay in Mem0 and are read at request time.

## Tech stack & commands

- **Go** (1.26.4, pinned via asdf in `.tool-versions`), feature-first layout under `internal/`.
- **Postgres + pgvector**, separate DB `baseline`. Migrations via **goose** (embedded in
  `internal/store`, run automatically at startup).
- **HTTP**: stdlib `net/http` + **chi**. DB driver **pgx/v5** + `pgxpool`. **OpenAPI spec
  authored first** (`api/openapi.yaml`) before M2 handlers.
- **Embeddings**: Ollama via `EMBEDDER_URL` (default `nomic-embed-text`, **768 dims**).

Commands:

- `go build ./...`, `go vet ./...`
- `go test -short ./...` — unit tests only, no Docker (use while iterating).
- `go test ./...` — full suite incl. integration; needs Docker for the pgvector testcontainer.
- `go test -run TestName ./internal/<pkg>` — a single test.

### Testing is mandatory per feature

See [TESTING.md](TESTING.md). Every feature ships **unit + integration tests**, and integration
includes **API tests** (real chi router + DB behind `httptest`) for anything over HTTP. The shared
harness is `internal/storetest` (one pgvector container per package via `storetest.Main` in
`TestMain`; `Tx`/`FreshDB`/`NewAPI` helpers). A milestone is done only when its tests — including the
relevant §14 conformance invariants — are green.
- Once a Go module exists, expect the standard `go build ./...`, `go test ./...`,
  `go test -run TestName ./pkg` (single test), `go vet ./...` / `golangci-lint run`.

## Intended package layout (spec §15)

```
cmd/baseline/        main, wiring
api/openapi.yaml     contract — author first
internal/
  facts/             fact domain + state machine
  promotions/        promotion workflow
  autopromote/       AutoPromoteEngine port + registry; simple/ holds simple/v1
  namespaces/        registry + policy
  rbac/              entitlements, separation-of-duties
  contextsvc/        /context resolver + precedence
  audit/             append-only writer
  memory/            MemorySource port (neutral types); mem0/ and null/ adapters
  embed/             embedder client
  store/             postgres + pgvector
  server/            http handlers, mcp bridge, authn/authz middleware
  platform/          config, otel, logging
test/conformance/    the §14 acceptance suite
migrations/          goose/golang-migrate
deploy/              helm values/templates
```

## Architecture decisions that must be honored

These are the load-bearing invariants. Getting them wrong silently breaks the governance model;
the conformance suite (§14) exists to catch violations.

- **Canonical identity is structured, deterministic, and server-derived.** A fact's identity is
  its `subject` (`{type, scope?, qualifiers?}`), supplied at propose time — *never* parsed from the
  free-text `statement`. `canonical_key = normalize(subject)` is a pure function computed on the
  single write path; **a client may never set `canonical_key` directly**. This is what makes
  conflict detection and supersession deterministic instead of NLP-dependent.

- **One active fact per (namespace, canonical_key).** Enforced both by a Postgres partial unique
  index (`WHERE status = 'active'`) *and* asserted in conformance. A reworded update *supersedes*
  the prior fact (lineage set both ways, atomically on approval) rather than duplicating it.

- **Separation of duties is a hard gate, not policy.** The proposer can never be an approver,
  under any namespace policy. Approval needs `≥ required_approvals` *distinct* reviewers (proposer
  excluded), any order.

- **Append-only audit.** Every fact/promotion state transition writes exactly one immutable
  AuditEvent. Audit rows are never updated or deleted.

- **`/context` is the agent read path and must leak nothing.** It returns only `active` facts
  within the caller's entitled namespaces, drops expired/revoked/superseded and anything past
  `valid_to`, and resolves precedence per `canonical_key`: `user ▸ project ▸ team ▸ org`, except
  facts tagged `authoritative:true` always win. Personal memories merge in at lowest precedence
  and never override a fact.

- **Auto-promotion is a pluggable, versioned engine** (`AutoPromoteEngine`, selected per namespace
  by a `family/vN` ID like `simple/v1`). It must **fail closed** (any error/timeout/invalid rules ⇒
  fall back to human review, never auto-approve on uncertainty), be **deterministic**, write an
  AuditEvent with `principal = "engine:<ID>"` + tag the fact `auto:true`, and be **version-isolated**
  (registering `simple/v2` must not change decisions for namespaces pinned to `simple/v1` — no silent
  migration). Policies referencing an unknown engine ID, or rules failing the engine's `Validate`,
  are rejected at write time.

- **The memory backend is behind a narrow port.** Baseline depends on the `MemorySource` interface
  (3 read-only methods), not on Mem0. Mem0 is the default adapter; `none` (null source) gives a
  first-class **standards-only mode** with no memory backend at all. Baseline never writes to the
  backend, and only neutral text+metadata crosses the boundary — never embedding vectors. Baseline
  owns its own fact embeddings (decision §18.1). Materializing facts back into Mem0 was considered
  and **rejected** (§18.3) — merge-at-read `/context` stays the single source of truth.

## Implementation order

Follow the milestones in spec §17: M0 schema/store/namespaces → M1 RBAC → M2 fact state machine +
promotions + audit + canonical-key derivation → M2a auto-promote engine → M3 `/context` resolver →
M4 MCP bridge → M5 reaper + OTEL → M6 full conformance green + Helm + CI. Each milestone lists the
specific §14 conformance items it must satisfy.

When implementing a feature, treat the relevant §14 acceptance criteria as the definition of done —
they are written as a contract test suite that runs against a live instance.
