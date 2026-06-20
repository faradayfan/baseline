# Baseline — Claude Code plugin

Packages Baseline's Claude Code integration as one installable plugin, so a
developer can wire their Claude Code to an org's Baseline with a single install
instead of hand-assembling hooks, an MCP config, and env vars.

It bundles three things:

1. **Tiered context injection** (`SessionStart` + `UserPromptSubmit` hooks) — loads
   the **always-on** facts once per session and the **relevant** facts per turn,
   instead of dumping every fact into every prompt. See *Tiered injection* below.
2. **Memory capture** (`Stop` hook) — when a reply contains a `[remember: …]`
   tag, posts that text to Baseline's out-of-band `/v1/memories` (→ Mem0).
   **Opt-in** (see below) so it does not fire in unrelated repos.
3. **MCP tools** (HTTP MCP server) — exposes `get_context`, `search_facts`,
   `propose_fact`, `list_my_promotions`, `review_promotion` against
   `<backend_url>/mcp`, authenticated per-user with `X-Baseline-Principal`.

## Tiered injection — keeping context clean as facts scale

Injecting every fact into every prompt does not scale (at 100 facts that's
~1.5k tokens *per turn*, mostly irrelevant). So a fact's **delivery tier** — a
`tier:` tag — controls *when* it is injected, decoupled from *what* it is:

| Tag on the fact | Injected | By which hook |
| --- | --- | --- |
| `tier:always` | **once per session** | `SessionStart` → the mandatory guardrails the agent must always know |
| `tier:relevant` | **per turn, only if it matches this project's topics** | `UserPromptSubmit` |
| `tier:ondemand` *(and any untagged fact)* | **never auto-injected** | the agent pulls it via the `search_facts` / `get_context` MCP tools when it needs it |

**Default is lean:** a fact with no `tier:` tag is *not* auto-injected — you opt
facts into injection, rather than opting out of a firehose.

**Per-turn relevance comes from a project file.** Create **`.baseline-topics`** in
the repo root listing the topic tags this project cares about (comma- and/or
newline-separated; `#` starts a comment):

```text
# .baseline-topics
deploy
backend
go
```

The `UserPromptSubmit` hook then injects only `tier:relevant` facts tagged with one
of those topics. No `.baseline-topics` → nothing relevance-injected (lean).

> `tier:` is **orthogonal to `authoritative:true`**. `authoritative` is a governance
> property (a mandatory baseline that also *wins precedence*); `tier:` is purely a
> *delivery* property (when it's injected). A fact can be either, both, or neither.

## Install

```text
/plugin marketplace add faradayfan/baseline        # or a git URL to your fork/host
/plugin install baseline@baseline                  # plugin-name@marketplace-name
```

On enable you're prompted once for:

| Config | Example | Default | Notes |
| ------ | ------- | ------- | ----- |
| **Baseline URL** | `https://baseline.acme.com` | `http://localhost:8080` | your deployment, no trailing slash |
| **Your principal** | `jane@acme.com` | `local-dev` | sent as `X-Baseline-Principal` (dev identity; real deploys use OIDC) |
| **API token** *(optional)* | — | — | bearer token if your Baseline requires auth; blank for the open dev/POC |

The defaults make a **local POC** (`make local-up`, backend on `localhost:8080`)
work with no answers at all. **Org users** override the URL with their deployment.

Then reload (restart the session, or `/reload-plugins` if your client has it) to
activate. Context injection starts immediately; the MCP tools appear in the tool list.

### If the config prompt didn't fire

The `userConfig` prompt fires on the plugin's *enable* transition. Some clients
(notably older VS Code extension builds) don't always surface it, leaving the
plugin enabled-but-unconfigured — the MCP server then can't connect (its URL is an
unsubstituted `${user_config.backend_url}`) and the hooks print a one-line notice
on stderr. The defaults above usually prevent this; if you still hit it, set the
config by hand in `~/.claude/settings.json` (the documented storage location) and
reload your client:

```json
{
  "pluginConfigs": {
    "baseline@baseline": {
      "options": { "backend_url": "http://localhost:8080", "principal": "local-dev" }
    }
  }
}
```

Use your real deployment URL + identity for a hosted Baseline. `api_token` is
`sensitive`, so it is **not** stored here — it goes to the system keychain; leave
it out of this file (the open dev/POC needs no token).

## Connected but seeing no facts? You need a namespace membership

Facts in Baseline are **namespace-scoped**, and a principal only sees a namespace's
facts if it holds a **membership** there. A principal with no membership sees
**nothing** from `/context`, `search_facts`, or `get_context` — there is no
"org-wide, visible to everyone" tier. This is by design (the governance model gates
read access on entitlement).

So a working install has **two** requirements, not one:

1. The plugin points at Baseline with your principal (the config above), **and**
2. **someone has granted that principal a membership** in the relevant namespace.

If `search_facts` returns an empty list but the dashboard shows facts, the cause is
almost always a principal mismatch — you're connected as a principal that was never
granted membership (e.g. the default `local-dev`, or a new identity nobody onboarded
yet). The fix is to onboard that principal:

```bash
# local cluster:   PRINCIPAL=<your-id> CONTEXT=docker-desktop ./deploy/seed.sh
# remote cluster:  PRINCIPAL=<your-id> CONTEXT=k3s ./deploy/seed.sh
```

The seed script creates the `org` namespace (idempotent) and grants the principal
its roles; its summary prints which namespaces that principal can now read.

## Opting in to memory capture

Capture is gated so `[remember: …]` doesn't silently write memories everywhere.
Enable it per project in either way:

```bash
touch .baseline-capture          # marker file in the project root (commit or gitignore it)
# — or —
export BASELINE_CAPTURE=1         # env var for the session/shell
```

With neither present, the `Stop` hook is a no-op. With either, a reply containing
`[remember: John prefers pnpm]` posts that memory to Baseline; Mem0 runs its own
extraction and it later surfaces in `/context` as `source: memory`.

## Config resolution

Scripts read the plugin's install-time config (`CLAUDE_PLUGIN_OPTION_BACKEND_URL`,
`…_PRINCIPAL`, `…_API_TOKEN`) and fall back to the legacy `BASELINE_CONTEXT_URL` /
`BASELINE_PRINCIPAL` / `BASELINE_API_TOKEN` env vars — so a hand-wired repo keeps
working unchanged. Every hook fails silent (exit 0) on any error and never blocks
the session.

## Local development

```bash
claude --plugin-dir ./plugin      # load this directory as a plugin without a marketplace
```

The scripts are plain bash + python3 (no build step). They are the productized
form of the repo-local hooks in `.claude/` — once you adopt the plugin, those can
be removed.
