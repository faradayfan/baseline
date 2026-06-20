# Running Baseline locally

This guide gets Baseline running on your machine and connects it to Claude Code
(VS Code extension) over MCP. It runs in **standards-only mode** (no Mem0) — just
Postgres + pgvector — which is enough to exercise the whole governance flow.

## Prerequisites

- **Go** 1.26.4 (pinned via asdf in `.tool-versions`)
- **Docker** (for the Postgres + pgvector container)
- **Claude Code** — the VS Code extension or the `claude` CLI

## Quick start

```bash
./scripts/dev-setup.sh
```

That script is idempotent and does everything below in one shot:

1. starts a `pgvector/pgvector:pg16` container (`baseline-pg` on port **5433**),
2. builds `./bin/baseline`,
3. applies migrations (the binary auto-migrates on startup),
4. seeds an `org` namespace, grants `local-dev` the reader/contributor/reviewer
   roles, and authors one sample active fact so the tools return data.

Then jump to **[Connect to Claude Code](#connect-to-claude-code)**.

To customize: `PG_PORT=5444 PRINCIPAL=me ./scripts/dev-setup.sh`.

## What's running

```
Claude Code (VS Code) ──.mcp.json──▶ bin/baseline (MCP stdio, principal "local-dev")
                                          │  in-process call through the real REST
                                          │  handler — auth/RBAC/audit all reused
                                          ▼
                                   Postgres + pgvector (baseline-pg :5433)
                                   MEMORY_SOURCE=none (standards-only)
```

The same `bin/baseline` binary has three modes, chosen by env var:

| Mode | Trigger | Use |
| ---- | ------- | --- |
| HTTP server (default) | _none_ | the REST API on `BASELINE_ADDR` (default `:8080`) |
| MCP over stdio | `BASELINE_MCP_STDIO=true` | what Claude launches |
| Reaper (one pass, exits) | `BASELINE_REAP=true` | expire stale facts; deployed as a CronJob |

## Connect to Claude Code

Create **`.mcp.json`** at the repo root (the VS Code extension auto-discovers it).
**Use an absolute path** for `command` — the extension does not resolve relative paths:

```json
{
  "mcpServers": {
    "baseline": {
      "command": "/ABSOLUTE/PATH/TO/baseline/bin/baseline",
      "args": [],
      "env": {
        "DATABASE_URL": "postgres://baseline:baseline@localhost:5433/baseline?sslmode=disable",
        "MEMORY_SOURCE": "none",
        "BASELINE_MCP_STDIO": "true",
        "BASELINE_MCP_PRINCIPAL": "local-dev"
      }
    }
  }
}
```

Replace `/ABSOLUTE/PATH/TO/baseline` with your checkout path (e.g. the output of
`pwd`). Then **reload the Claude extension** (or restart VS Code) and approve the
project MCP server when prompted.

You should now see five tools: `get_context`, `search_facts`, `propose_fact`,
`list_my_promotions`, `review_promotion`. Try: _"use search_facts to list the
org's facts."_ It should return the seeded deploy-policy fact.

> **Productized install — the Baseline plugin.** The `.mcp.json` + hooks above are
> the hand-wired setup. For onboarding real users, [`plugin/`](plugin/README.md)
> packages all of it (context-injection hook, `[remember: …]` capture hook, and the
> HTTP MCP server) as one Claude Code plugin, parameterized per-user at install time
> by **Baseline URL** + **principal**:
>
> ```text
> /plugin marketplace add faradayfan/baseline
> /plugin install baseline@baseline   # prompts for URL + principal, then wires everything
> ```
>
> Context injection then runs in every project; memory capture is opt-in per repo
> (`touch .baseline-capture`). See [plugin/README.md](plugin/README.md).

### CLI alternative

If you use the standalone `claude` CLI instead of the extension:

```bash
claude mcp add baseline /ABSOLUTE/PATH/TO/baseline/bin/baseline \
  -e DATABASE_URL="postgres://baseline:baseline@localhost:5433/baseline?sslmode=disable" \
  -e MEMORY_SOURCE=none -e BASELINE_MCP_STDIO=true -e BASELINE_MCP_PRINCIPAL=local-dev
```

## Local cluster (Docker Desktop Kubernetes)

Run the **full stack** — Baseline + Mem0 + Neo4j + the fact/memory merge — on
Docker Desktop's built-in Kubernetes, with **Ollama running natively on macOS**
for GPU speed. Fully self-hosted, **no vendor API keys**, and fast (memory-add is
~1s on Apple Silicon vs. minutes on a Pi CPU).

**Why Ollama on the host, not in a container:** Docker Desktop's Linux VM has no
GPU passthrough, so a containerized Ollama is CPU-only (slow). Native `ollama
serve` uses Metal/GPU; pods reach it at `host.docker.internal:11434`.

### One-time host setup

```bash
# Enable Kubernetes in Docker Desktop settings (context: docker-desktop).
brew install ollama
ollama serve &                    # runs the host Ollama service (GPU)
ollama pull qwen2.5:3b            # LLM for memory extraction
ollama pull nomic-embed-text     # embedder (768 dims)
```

### Bring it up

```bash
make local-up      # builds 3 images, loads them into the node, helm installs
kubectl --context docker-desktop -n baseline get pods -w
```

Baseline is reachable at **`http://localhost:8080`** (Docker Desktop binds the
`LoadBalancer` service to localhost — no MetalLB, no port-forward). Images are
built locally and imported into the node's containerd, so there's no registry.

### Seed + demo

```bash
make local-seed                              # CONTEXT=docker-desktop ./deploy/seed.sh

# add a personal memory (GPU-fast extraction via host Ollama):
API=$(kubectl --context docker-desktop -n baseline get pod -l app.kubernetes.io/name=mem0-api \
  -o jsonpath='{.items[0].metadata.name}')
kubectl --context docker-desktop -n baseline exec "$API" -- python3 -c "
import urllib.request, json
req = urllib.request.Request('http://localhost:8000/memories',
  data=json.dumps({'messages':[{'role':'user','content':'I prefer to deploy on Fridays.'}],'user_id':'john'}).encode(),
  headers={'Content-Type':'application/json'}, method='POST')
print(urllib.request.urlopen(req, timeout=120).read().decode())"

# the merge, fully local + keyless:
curl -s "http://localhost:8080/v1/context?include_memories=true&actor_id=john" \
  -H 'X-Baseline-Principal: john' | python3 -m json.tool
```

Point your `.mcp.json` (or the hook's `BASELINE_CONTEXT_URL`) at
`http://localhost:8080` — same shape as the remote, just localhost. Tear down with
`make local-down` (add `CLEAN=1` to drop PVCs).

### Dashboard (read-only UI)

A separate Vite/React SPA gives a browser view of facts, the promotion inbox,
per-fact audit trails, namespaces, and a "what an agent sees" context preview —
so you stop reaching for `psql`/`curl` to inspect state.

```bash
# hot-reload dev against a running backend on :8080 (Vite on :5173):
make ui-dev                 # or: cd frontend && pnpm dev
#   VITE_BACKEND_TARGET=http://localhost:8080 by default; override to a port-forward.

# containerized: `make local-up` builds + deploys it alongside the stack:
open http://localhost:8081  # LoadBalancer -> localhost (baseline holds :8080)
```

Identity is the **`X-Baseline-Principal` header**, surfaced as a **"view as"**
control in the header (default `john`, stored in localStorage). It is read-only
and **spoofable by design** — the same trust model as the rest of this local
POC, and read-only means it can mutate nothing. This is a single-user inspector,
**not** the org governance console: real auth (OIDC) and write actions
(propose/approve from the UI) are deliberate follow-ups. Authoring still goes
through the governed MCP/REST path.

> If memory adds return `"results": []`, check `kubectl logs` for the mem0-api pod.
> A `expected 1536 dimensions, not 768` error means a pgvector table was created at
> the OpenAI size. Mem0 keeps **two** vector tables — `memories` **and**
> `mem0migrations` — and BOTH must be 768; a stale `mem0migrations` at 1536 fails
> writes even after `memories` is fixed. Drop both and restart mem0-api so the
> patched image recreates them at 768:
> `DROP TABLE IF EXISTS memories, mem0migrations CASCADE;` (as the `postgres`
> superuser in mem0-postgres), then
> `kubectl -n baseline delete pod -l app.kubernetes.io/name=mem0-api`.
> (Note this clears stored memories — re-add them.) Separately, the local
> `qwen2.5:3b` extractor is conservative: a `"results": []` with no dim error in the
> logs just means Mem0's LLM judged the text not memory-worthy — phrase captures as
> clear, declarative facts/preferences.

### Memory capture (harness → Mem0, via Baseline)

The spec puts memory _capture_ outside Baseline: Mem0 answers "what has this agent
seen?", fed by the agent runtime (§1, §11.2). To wire **Claude Code** into that
path, Baseline exposes a thin **out-of-band** write-proxy and a Stop hook drives it:

- **`POST /v1/memories`** (`{content, actor_id?, metadata?}`, `X-Baseline-Principal`)
  pass-throughs to Mem0's write API. It is a documented exception to the §11
  read-only boundary — it does **not** touch the fact store or `/context` resolver,
  and rides a separate `memory.Writer` capability (the read-only `memory.Source`
  port is unchanged). Standards-only (`MEMORY_SOURCE=none`) returns **501**.
- **Stop hook** [`.claude/hooks/capture-memory.sh`](.claude/hooks/capture-memory.sh)
  (registered in `.claude/settings.json`): when a reply contains a
  **`[remember: …]`** tag, the hook scrapes it and POSTs it to
  `$BASELINE_CONTEXT_URL/v1/memories` as `$BASELINE_PRINCIPAL`. Nothing is captured
  without the explicit tag. Mem0 then runs its own LLM extraction (so stored text
  may be rephrased), and the memory surfaces in `/context` as `source: memory`.

```bash
# manual smoke test of the proxy:
curl -s -X POST http://localhost:8080/v1/memories \
  -H 'Content-Type: application/json' -H 'X-Baseline-Principal: john' \
  -d '{"content":"I always run go vet before pushing"}' | python3 -m json.tool
# then confirm it merges into the read path:
curl -s "http://localhost:8080/v1/context?include_memories=true" \
  -H 'X-Baseline-Principal: john' | python3 -m json.tool
```

This is **capture**, not governance: a captured memory is raw and unreviewed. To
make it an official **fact**, it still goes through propose → review → approve.

## Remote (Raspberry Pi k3s cluster)

This deploys Baseline centrally and connects a local Claude to it over the
network — emulating org onboarding. Identity is still the `X-Baseline-Principal`
header (a POC placeholder for OIDC/mTLS); only run this on a trusted LAN.

### Deploy

```bash
# one-time: log in to the in-cluster registry (plain HTTP, add to Docker
# "insecure-registries"): docker login <REGISTRY_HOST>:5000

# one-time (and on PG version bumps): build + push the custom Postgres image
# (Bitnami base + pgvector — stock Bitnami lacks the `vector` extension).
make pi-pg-image

make pi-deploy            # buildx arm64 -> push -> helm dep build -> helm upgrade on k3s
kubectl --context k3s -n baseline get pods -w
```

> Postgres runs the Bitnami subchart pointed at that custom pgvector image, so the
> subchart's NFS/volume-permissions handling works (a plain pgvector Deployment
> trips over NFS root-squash on `nfs-client`).

Baseline comes up on the MetalLB IP **<BASELINE_LB_IP>** (port 8080), serving the
REST API, `/healthz`, `/readyz`, and the MCP endpoint at **`/mcp`**.

```bash
curl http://<BASELINE_LB_IP>:8080/healthz     # {"status":"ok"}
curl http://<BASELINE_LB_IP>:8080/readyz      # {"status":"ready"} once Postgres is up
```

### Onboard (seed + grant roles)

```bash
PRINCIPAL=john ./deploy/seed.sh                 # org namespace + grants + sample fact
PRINCIPAL=reviewer-bob ROLES=reviewer ./deploy/seed.sh   # a 2nd principal to approve with
```

### Connect Claude to the remote server

Point your MCP config at the remote URL (no local binary, no local Postgres).
Identity travels in the header:

```json
{
  "mcpServers": {
    "baseline": {
      "type": "http",
      "url": "http://<BASELINE_LB_IP>:8080/mcp",
      "headers": { "X-Baseline-Principal": "john" }
    }
  }
}
```

Reload the extension. `get_context` / `search_facts` / `propose_fact` now run
against the **cluster**. Because identity is per-request, a teammate using the
same URL with their own `X-Baseline-Principal` sees only their entitled
namespaces.

### Full governance loop across two principals

Separation of duties means a principal can't approve its own proposal. To run a
complete propose → approve → `/context`:

1. As `john` (contributor): `propose_fact` → it lands in `pending`.
2. As `reviewer-bob` (reviewer, seeded above): `review_promotion` with
   `action=approve` → the fact goes `active`.
3. As `john`: `get_context` → the new fact appears.

(Do step 2 from a second Claude session whose `.mcp.json` sets
`X-Baseline-Principal: reviewer-bob`, or via curl against `/mcp`.)

### Mem0 (personal memories merged into /context)

Phase 2 adds a **Mem0** memory backend so `/context` merges an actor's personal
memories with the governed facts (spec §10): facts rank above memories, deduped
by canonical_key, each item tagged `source: fact|memory`.

It is opt-in via `mem0.enabled` (the Pi overlay enables it). The stack runs on
the cluster: the Mem0 API server, its own pgvector Postgres, and Neo4j (graph
memory). **Mem0's LLM + embedder use the OpenAI API** — self-hosted Ollama was
tried first but Pi CPUs are too slow/weak for Mem0's JSON extraction (small models
emit invalid JSON; inference is ~minutes). Ollama is left in the chart (disabled)
for a future GPU node.

Setup:

```bash
# Mem0's API server lacks the DB/graph drivers its own stores need; this image
# adds them on top of stock (run once):
make pi-mem0-image

# Put your OpenAI key in deploy/pi/secrets.yaml (gitignored):
#   mem0:
#     apiKey: "<random admin key>"
#     jwtSecret: "<random>"
#     openaiApiKey: "sk-..."
make pi-deploy
```

Seed a memory for an actor (triggers OpenAI extraction):

```bash
API=$(kubectl --context k3s -n baseline get pod -l app.kubernetes.io/name=mem0-api \
  -o jsonpath='{.items[0].metadata.name}')
kubectl --context k3s -n baseline exec "$API" -- python3 -c "
import urllib.request, json
req = urllib.request.Request('http://localhost:8000/memories',
  data=json.dumps({'messages':[{'role':'user','content':'I prefer to deploy on Fridays.'}],'user_id':'john'}).encode(),
  headers={'Content-Type':'application/json'}, method='POST')
print(urllib.request.urlopen(req, timeout=90).read().decode())"
```

Then over the remote MCP, `get_context` with `include_memories: true` returns the
facts first, the memory below, each `source`-tagged. Note: with the OpenAI
embedder, memory *text* leaves the cluster (Baseline's own fact embeddings stay
local and separate).

## Poking the HTTP API directly

Useful for debugging without MCP. Identity comes from the `X-Baseline-Principal`
header (dev `HeaderAuthenticator`; production uses OIDC/mTLS):

```bash
DATABASE_URL="postgres://baseline:baseline@localhost:5433/baseline?sslmode=disable" \
MEMORY_SOURCE=none BASELINE_ADDR=:8088 ./bin/baseline &

curl -s localhost:8088/healthz
curl -s localhost:8088/v1/context -H 'X-Baseline-Principal: local-dev'
curl -s localhost:8088/v1/facts   -H 'X-Baseline-Principal: local-dev'
```

## Day-to-day

```bash
# after a reboot — the container persists data but stops:
docker start baseline-pg

# after code changes (the extension launches the binary fresh each session):
go build -o ./bin/baseline ./cmd/baseline

# run the test suite:
go test -short ./...   # unit only, no Docker
go test ./...          # full, needs Docker for the pgvector testcontainer
```

## Things to know about the model

- **Identity is fixed per MCP session.** Every tool call runs as
  `BASELINE_MCP_PRINCIPAL`. That principal can only act where it holds RBAC
  grants — that's why setup grants `local-dev` three roles. To act as someone
  else, change the principal in `.mcp.json` and grant that name roles.

- **Separation of duties is enforced.** A principal **cannot approve its own
  proposal** (spec §14.6), even with the reviewer role. To exercise a full
  propose → approve → `/context` loop, approve as a _different_ principal — e.g.
  grant a second name the reviewer role and approve via curl with a different
  `X-Baseline-Principal`, or add a second MCP server entry with that principal.

- **Baseline injects nothing on its own.** It's a pull-only retrieval service:
  facts reach the model only when the agent calls `get_context` / `search_facts`.
  There is no automatic context injection (by design, and MCP servers can't push
  into context). Automatic per-turn injection would live in the harness (e.g. a
  hook that calls `GET /context` before each turn), not in Baseline.

## Troubleshooting

- **Tools don't appear after reload** — confirm `.mcp.json` `command` is an
  absolute path and the binary exists (`ls -l bin/baseline`); re-approve the
  project server when VS Code prompts.
- **Tools return empty** — make sure `dev-setup.sh` ran (seeds data) and the
  principal in `.mcp.json` matches a granted principal. A principal with no
  grants legitimately sees nothing.
- **`DATABASE_URL is required` / connection refused** — the container isn't up:
  `docker start baseline-pg`. Check the port matches (`5433` by default).
- **Protocol/parse errors in the extension** — Baseline logs to **stderr** so
  stdout stays pure JSON-RPC; if you wrapped the binary in a script, make sure it
  doesn't echo to stdout.

## Tearing down

```bash
docker rm -f baseline-pg   # removes the container AND its data
rm -rf bin                 # remove the built binary
```
