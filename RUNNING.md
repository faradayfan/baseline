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

### CLI alternative

If you use the standalone `claude` CLI instead of the extension:

```bash
claude mcp add baseline /ABSOLUTE/PATH/TO/baseline/bin/baseline \
  -e DATABASE_URL="postgres://baseline:baseline@localhost:5433/baseline?sslmode=disable" \
  -e MEMORY_SOURCE=none -e BASELINE_MCP_STDIO=true -e BASELINE_MCP_PRINCIPAL=local-dev
```

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
