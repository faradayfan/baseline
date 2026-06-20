# Baseline — Claude Code plugin

Packages Baseline's Claude Code integration as one installable plugin, so a
developer can wire their Claude Code to an org's Baseline with a single install
instead of hand-assembling hooks, an MCP config, and env vars.

It bundles three things:

1. **Context injection** (`UserPromptSubmit` hook) — prepends the caller's
   Baseline `/context` (governed org facts + personal memories) above every
   prompt. Fires in **every** project the plugin is enabled in: the org baseline
   is meant to be everywhere.
2. **Memory capture** (`Stop` hook) — when a reply contains a `[remember: …]`
   tag, posts that text to Baseline's out-of-band `/v1/memories` (→ Mem0).
   **Opt-in** (see below) so it does not fire in unrelated repos.
3. **MCP tools** (HTTP MCP server) — exposes `get_context`, `search_facts`,
   `propose_fact`, `list_my_promotions`, `review_promotion` against
   `<backend_url>/mcp`, authenticated per-user with `X-Baseline-Principal`.

## Install

```text
/plugin marketplace add faradayfan/baseline        # or a git URL to your fork/host
/plugin install baseline
```

On enable you're prompted once for:

| Config | Example | Notes |
| ------ | ------- | ----- |
| **Baseline URL** | `https://baseline.acme.com` | your deployment, no trailing slash |
| **Your principal** | `jane@acme.com` | sent as `X-Baseline-Principal` (dev identity; real deploys use OIDC) |
| **API token** *(optional)* | — | bearer token if your Baseline requires auth; blank for the open dev/POC |

Then `/reload-plugins` (or restart) to activate. Context injection starts
immediately; the MCP tools appear in the tool list.

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
