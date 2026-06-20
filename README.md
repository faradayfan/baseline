# Baseline

**A governance layer in front of an agent memory store.** Raw, per-actor
**memories** (in [Mem0](https://github.com/mem0ai/mem0)) get *promoted* — through a
reviewed, audited workflow — into curated **facts** that live in shared
**namespaces** (`user` / `team` / `project` / `org`). Agents read those facts
through a single context endpoint.

> Mem0 answers *"what has this agent seen?"*
> Baseline answers *"what does the org officially know, who vouched for it, and is it still true?"*

The name is after the Post-Traumatic Baseline Test in *Blade Runner 2049* — the
apparatus that checks whether a subject has deviated from an approved baseline.

## Why

An agent's memory is personal, unreviewed, and easy to pollute. That's fine for
"remembers what you told it," but it's the wrong substrate for *organizational*
knowledge — the deploy policy, the security baseline, the coding standard that
every agent should follow and that someone is accountable for. Baseline adds the
missing layer: a reviewed promotion path, namespace scoping, separation of
duties, an append-only audit trail, and a precedence-resolved read path — so a
fact an agent receives is one the org actually stands behind.

## How it works

```text
   agent harness ──writes──▶  Mem0  (raw per-actor memories)
                               │ read-only
                               ▼
  propose ─review─approve ─▶ Baseline (facts DB + governance) ─▶ /context ─▶ agent
   (separation of duties,         pgvector · Postgres            (facts ⊕ memories,
    audited, namespace-scoped)                                    precedence-resolved)
```

- **Facts** have a structured, server-derived **canonical identity** (`subject` →
  `canonical_key`), so conflict detection and supersession are deterministic, not
  NLP-guesswork. One active fact per `(namespace, canonical_key)`.
- **Promotion is gated.** A proposer can never approve their own fact; approval
  needs *N* distinct reviewers. Every transition writes one immutable audit event.
- **`/context` is the agent read path** and leaks nothing: only `active` facts in
  the caller's entitled namespaces, precedence `user ▸ project ▸ team ▸ org`,
  except `authoritative:true` facts always win. Personal memories merge in at
  lowest precedence and never override a fact.
- **Tag filtering** (`?tags=a,b`) narrows the read path so an agent subscribes to
  only the topics it needs (`authoritative:true` always passes).
- **Pluggable auto-promotion** (`AutoPromoteEngine`, versioned like `simple/v1`)
  that **fails closed** — any uncertainty falls back to human review.
- **The memory backend is behind a narrow, read-only port.** `none` gives a
  first-class **standards-only mode** with no memory backend at all.

See [docs/SPEC.md](docs/SPEC.md) for the full, locked specification.

## What's here

| Path | What |
| ---- | ---- |
| `internal/` | The Go service — `facts`, `promotions`, `rbac`, `contextsvc`, `namespaces`, `audit`, `autopromote`, `memory` (+ `mem0`/`null` adapters), `server`, `store`, … |
| `api/openapi.yaml` | The REST contract (authored first) |
| `cmd/baseline/` | One binary: HTTP server, the reaper (`BASELINE_REAP`), or the MCP bridge (`BASELINE_MCP_STDIO`) |
| `frontend/` | A read-only dashboard (React + Vite) — facts, promotion inbox, audit trails, a "what an agent sees" context preview |
| `plugin/` | A [Claude Code plugin](plugin/README.md) packaging the integration (context-injection hook, `[remember: …]` capture, MCP tools) for one-command install |
| `deploy/` | Helm chart + environment overlays (local Docker Desktop, remote k3s) |
| `test/conformance/` | Asserts the spec's §14 invariants against a live server |

## Quick start

Standards-only mode (no Mem0) — Postgres + pgvector, enough to exercise the whole
governance flow — in one command:

```bash
./scripts/dev-setup.sh
```

Then connect Claude Code over MCP, or run the full stack (Baseline + Mem0 + the
fact/memory merge) on Docker Desktop Kubernetes. Full instructions, the dashboard,
remote deployment, and the plugin install are in **[RUNNING.md](RUNNING.md)**.

```bash
go build ./...          # build
go test -short ./...    # unit tests (no Docker)
go test ./...           # full suite (Docker for the pgvector testcontainer)
```

Testing approach: [TESTING.md](TESTING.md). Working notes for contributors (and
Claude Code): [CLAUDE.md](CLAUDE.md).

## Status

The full v1 service (spec milestones M0–M6) is implemented and **conformance-green**
against all of the spec's §14 invariants. A remote deployment (M7-POC) runs on a
home k3s cluster with Mem0 wired for the fact/memory merge.

This is a **proof-of-concept / portfolio project**, and a few things are
**deferred by design, not unfinished**: real OIDC/mTLS auth (a dev-only header
authenticator stands in — it is spoofable), an OTEL exporter (the instruments
exist), CI wiring, and semantic (embedding-ranked) fact search (`?q=` is
substring for now). These are called out where they appear; nothing here claims
production auth.

## Tech

Go 1.26 · stdlib `net/http` + chi · pgx/v5 · Postgres + pgvector · goose
migrations · the official MCP Go SDK · Helm · React/Vite for the dashboard.

## License

[MIT](LICENSE).
