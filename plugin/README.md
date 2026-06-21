# Baseline — Claude Code plugin

Packages Baseline's Claude Code integration as one installable plugin, so a
developer can wire their Claude Code to an org's Baseline with a single install
instead of hand-assembling hooks, an MCP config, and env vars.

It bundles three things:

1. **Tiered context injection** (`SessionStart` + `UserPromptSubmit` hooks) — loads
   the **always-on** facts once per session and the **relevant** facts per turn,
   instead of dumping every fact into every prompt. See *Tiered injection* below.
2. **Memory capture** (`Stop` hook) — when a reply contains a `[remember: …]`
   tag (optionally typed, e.g. `[remember:procedural: …]`), posts that text to
   Baseline's out-of-band `/v1/memories` (→ Mem0) with its cognitive type as
   metadata. **Opt-in** (see below) so it does not fire in unrelated repos.
3. **MCP tools** (HTTP MCP server) — exposes `get_context`, `search_facts`,
   `list_namespaces`, `propose_fact`, `submit_promotion`, `list_my_promotions`,
   `review_promotion` against `<backend_url>/mcp`, authenticated per-user with
   `X-Baseline-Principal`. Together these let an agent walk the whole authoring
   flow: discover a namespace → propose a fact → submit it for review → (a
   different reviewer) approve it.

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

### The agent is *taught* the convention (only when opted in)

Enabling capture also turns on a second `SessionStart` hook
(`inject-capture-guide.sh`) that injects a short instruction teaching the agent the
`[remember:TYPE: …]` convention, the three types, and — emphatically — **when not to
capture**. This is gated by the *same* opt-in (`.baseline-capture` / `BASELINE_CAPTURE`),
so unrelated repos get neither the capture path nor the instruction.

The guidance biases hard toward **not** capturing: capture only durable, reusable,
user-confirmed facts/rules — never inferences, transient task detail, or chatter. The
mechanism for unprompted capture exists, but the agent is instructed to use it
sparingly, because the failure mode that bites is noisy/duplicate memories someone
then has to curate, not the occasional missed one. Without this hook, capture would
only ever fire when an agent already happened to know the syntax — non-portable; with
it, capture is a taught, bounded behavior.

### Typing a captured memory

A capture can carry a **cognitive type** — `semantic` (what's true), `procedural`
(how to do things), or `episodic` (what happened) — written into the marker:

```text
[remember:procedural: deploy services with `helm upgrade`, not kubectl apply]
[remember:episodic: the reaper refactor on 2026-06-20 broke the context resolver]
[remember: John prefers pnpm over npm]        # untyped → defaults to semantic
```

The type is sent as `metadata.type` on the memory (Mem0 preserves it). Explicit
type wins; an untyped `[remember: …]` defaults to `semantic`; an unrecognized
prefix (e.g. `[remember:foo: …]`) is treated as untyped and the `foo:` stays part
of the text. The type informs later treatment — e.g. which `tier:` a memory would
get if it's promoted to a governed fact. (Promotion to a fact remains a deliberate,
reviewed step; capture only records the memory and its type.)

**Captures are stored verbatim.** The hook posts `infer: false`, so Baseline tells
Mem0 to store the text **as written** — no extraction LLM rewriting it ("I prefer
Fridays" → "Prefers Friday afternoons") or silently dropping it. A `[remember:]` is
deliberate, intentionally-phrased capture; second-guessing it is the wrong mode.
This needs the **patched mem0-api image** (`deploy/mem0-api`) — the stock OSS image
ignores `infer` and re-extracts. (Extraction mode is reserved for future episodic
auto-capture, where distilling a transcript is the point.)

## Config resolution

Scripts read the plugin's install-time config (`CLAUDE_PLUGIN_OPTION_BACKEND_URL`,
`…_PRINCIPAL`, `…_API_TOKEN`) and fall back to the legacy `BASELINE_CONTEXT_URL` /
`BASELINE_PRINCIPAL` / `BASELINE_API_TOKEN` env vars — so a hand-wired repo keeps
working unchanged. Every hook fails silent (exit 0) on any error and never blocks
the session.

### Surviving plugin updates (config durability)

A plugin **update can clear** the install-time config (`pluginConfigs` in
`settings.json`), which would silently turn the hooks off. Because the hooks fall
back to the `BASELINE_*` env vars (which *do* survive updates), set them in your
**user** `~/.claude/settings.json` as a durable backstop:

```json
{
  "env": {
    "BASELINE_CONTEXT_URL": "http://localhost:8080",
    "BASELINE_PRINCIPAL": "you"
  }
}
```

With this, the inject/capture hooks keep working across updates even if
`pluginConfigs` is wiped. (Caveat: the **MCP server** in `.mcp.json` uses
`${user_config.*}` and can't read these env vars — restore `pluginConfigs` if the
MCP tools go missing after an update.)

## Updating the plugin (avoiding silent staleness)

This plugin **omits `version` from `plugin.json` on purpose** — Claude Code then
keys updates on the **git commit SHA**, so every pushed commit is a new version and
`/plugin update` actually advances the installed copy.

> ⚠️ If you *add* a `version` field, Claude Code pins to that string: pushing new
> commits **has no effect** and `/plugin update` reports "already at the latest
> version" — the installed hooks silently stay stale while the repo moves on. (This
> bit us: the install was frozen at an old commit while every push appeared to
> succeed.) Keep `version` out, or bump it on every shippable change.

Dev/update loop:

```text
edit plugin/ → commit → push → /plugin update baseline@baseline → /reload-plugins
```

`/reload-plugins` switches the running hooks/MCP to the freshly-updated copy without
restarting the session.

## Local development

The scripts are plain bash + python3 (no build step). The dev loop for hook/MCP
changes goes through the marketplace:

```text
edit plugin/ → commit → push → /plugin update baseline@baseline → /reload-plugins
```

`/reload-plugins` switches the running hooks/MCP to the freshly-updated copy
without restarting the session. It's slower than ideal (every change needs a
commit + push), but it's reliable.

> **Tried and didn't work (VS Code extension):** loading the working tree in place
> via a `~/.claude/skills/baseline-dev` symlink (`@skills-dir` plugin) — the
> extension did not register a symlinked skills-dir entry, so the hooks never
> fired. `claude --plugin-dir ./plugin` is the CLI-only equivalent (no extension
> support). If you find a working live-reload path for the extension, replace this
> note.

To test a hook change without going through the plugin at all, invoke the script
directly with a crafted transcript (this is how the hooks are unit-tested):

```bash
T=$(mktemp); printf '%s\n' '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"[remember:procedural: test]"}]}}' > "$T"
CLAUDE_PLUGIN_OPTION_BACKEND_URL=http://localhost:8080 CLAUDE_PLUGIN_OPTION_PRINCIPAL=you \
  BASELINE_CAPTURE=1 CLAUDE_PROJECT_DIR=. bash plugin/scripts/capture-memory.sh <<< "{\"transcript_path\":\"$T\"}"
```
