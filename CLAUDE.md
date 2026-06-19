# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Current state

This is a **greenfield repository**. There is no code yet — only [docs/SPEC.md](docs/SPEC.md)
(the full design) and a placeholder README. There is no Go module, no build, and no tests.
When you implement, you are bootstrapping the structure described below, not modifying existing code.

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

## Planned tech stack & commands

Per spec §15 (none of this exists yet — these are the intended choices):

- **Go**, feature-first layout under `internal/`.
- **Postgres + pgvector**, separate DB `baseline`. Migrations via **goose or golang-migrate**.
- **HTTP**: stdlib `net/http` + chi. **OpenAPI spec authored first** (`api/openapi.yaml`).
- **Embeddings**: Ollama via `EMBEDDER_URL` (default `nomic-embed-text`, **768 dims**).
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
