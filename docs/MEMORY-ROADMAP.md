# Baseline — Agent Memory Roadmap

How Baseline plugs into the Claude Code harness loop to give an agent a principled,
scalable memory system. This is a **roadmap**, not a locked spec (cf. `SPEC.md`): it
captures the model we settled on, what's shipped, and the deferred phases — written so
any one phase can be picked up independently.

## The core framing: hooks are *moments*, not memory types

The mistake to avoid: "which hook owns which memory type?" Hooks are **moments in
time**; each moment suits a memory **operation** (read or write). A memory *type* is a
property of the data, and it gets read at one moment and written at another — no hook
"is" the episodic hook.

| Hook (moment) | Operation | Why this moment | Fits |
| --- | --- | --- | --- |
| `SessionStart` | **READ** (bulk, once) | Fresh context; the one cheap chance to load "what I must always know here." Output persists the whole session. | Procedural guardrails; mandatory baselines |
| `UserPromptSubmit` | **READ** (targeted, per-turn) | You see the user's actual ask. | Semantic, relevance-scoped to the turn |
| `PreToolUse` | **READ** (just-in-time) | About to *do* a specific thing — the most precise relevance signal. | Procedural/semantic about the tool target |
| `PostToolUse` | **WRITE** (observe) | A thing just happened — raw episodic material. | Episodic ("ran X → Y") |
| `Stop` | **WRITE** (distill) | Turn finished — capture what was decided/learned. | Flagged `[remember:]`, semantic candidates |
| `PreCompact` | **WRITE** (rescue) | Context about to be summarized away — last chance to persist. | Anything important living only in short-term |

Two independent axes describe a fact/memory:

- **`type:`** — *what it is* (cognitive): `semantic` (what's true) / `procedural`
  (how to do things) / `episodic` (what happened). Drives **write** routing.
- **`tier:`** — *when it's injected* (delivery): `always` / `relevant` / `ondemand`.
  Drives **read** routing. Orthogonal to `type:` and to `authoritative` (governance).

The split matters: a procedural fact can be `tier:always` (a guardrail) *or*
`tier:relevant` (a how-to that's only relevant in context). Type ≠ delivery.

---

## Phase 1 — Tiered read path *(SHIPPED)*

**Problem:** the plugin injected *all* facts into *every* prompt — doesn't scale
(~1.5k tokens/turn at 100 facts, mostly irrelevant).

**Solution:** a fact's `tier:` tag controls *when* it's injected.

| `tier:` | Injected | Hook |
| --- | --- | --- |
| `tier:always` | once per session | `SessionStart` → `inject-session.sh` (`?tags=tier:always`) |
| `tier:relevant` | per-turn, if it matches the project's `.baseline-topics` | `UserPromptSubmit` → `inject-context.sh` |
| `tier:ondemand` / untagged | never auto-injected | agent pulls via `search_facts`/`get_context` MCP tools |

- Default is **lean**: untagged → not auto-injected (opt *in*, not opt *out*).
- Relevance signal = repo-local **`.baseline-topics`** (declared topic tags); the
  UserPromptSubmit hook queries by topic and client-side-filters to `tier:relevant`
  (the backend tag filter is OR-overlap, so the AND is done in the hook).
- Backend: `tier:always` is a second always-pass tag-filter bypass alongside
  `authoritative:true` — delivery only, **precedence untouched** (§14.9 conformance
  green). `internal/contextsvc/contextsvc.go`, `internal/facts/repo.go`.
- `deploy/seed.sh` reproduces the tiered demo facts idempotently.

**Result:** per-turn cost scales with *relevant* facts, not *total* facts; guardrails
paid once per session.

---

## Phase 2 — `type:` axis + typed write path *(NEXT)*

Phase 1 used only `tier:` (delivery). `type:` earns its keep here, on the **write**
side. Today capture is single-channel: `[remember: …]` → Mem0, undifferentiated.

- **Typed capture:** `[remember:procedural: …]` / `[remember:semantic: …]` (or infer
  the type). The type tag routes the write:
  - `type:semantic` durable facts → propose into Baseline (governed) *or* Mem0.
  - `type:procedural` how-to/guardrails → propose as facts, likely `tier:always`.
  - `type:episodic` → a session/event log (Phase 3), not the fact store.
- **The link to Phase 1:** a captured memory's `type:` should suggest its `tier:` on
  the way back out. Tag it right on the way in; route it right on the way out.
- **Open decision (deferred from Phase 1):** keep `type:`/`tier:` as **tag
  conventions**, or promote to first-class **`memory_type` / `delivery` columns** on
  `facts`. Tag convention = zero schema change, flexible; column = queryable,
  validated, enables type-specific `/context` behavior. Decide when type-specific
  behavior actually needs enforcing.
- Touch points: `Stop` hook (`capture-memory.sh`) — parse the type; the mem0 adapter
  / `propose_fact` routing; `plugin/README.md`.

## Phase 3 — Episodic capture (the thin layer today)

"What happened in past sessions" is the biggest gap. Mem0 captures *some* distilled
episodes; there's no real session log.

- **Auto-capture at `PostToolUse`/`Stop`:** record what happened (decisions, what
  broke, what was tried) without an explicit `[remember:]` — episodic memory is just
  *what occurred*. Needs a noise filter (don't log every `ls`).
- **`SessionEnd`/`Stop` consolidation:** summarize the session into one episodic
  record ("this session we refactored the reaper; X broke").
- **`PreCompact` rescue:** persist important short-term context before it's summarized
  away — pure episodic safety net.
- **Read side:** episodic is `tier:ondemand` mostly — pulled when resuming related
  work or debugging a recurring issue, not injected every turn.
- Storage question: Mem0 with `type:episodic`, or a dedicated session-log store.

## Phase 4 — Smarter relevance for `tier:relevant`

Phase 1's relevance signal is `.baseline-topics` (project-declared, deterministic).
Evolve it:

- **Prompt-keyword match:** extract topics from the user's prompt text (cheap; fragile
  on vague prompts like "fix this").
- **Semantic similarity:** embed the prompt, inject top-K relevant facts. Needs the
  deferred embedding-ranked search (`?q=` is substring today, §"deferred" in
  `CLAUDE.md`). Dynamic, "smart", nondeterministic.
- **`PreToolUse` just-in-time:** the most precise signal — "about to deploy → pull
  deploy facts now." Per-tool-call, so guard against noise.

## Phase 5 — Org-wide read (open governance question)

Surfaced while building: facts are namespace-scoped; a principal with no membership
sees nothing. "Org-wide baseline that every agent gets" (SPEC §11.2 framing) is **not**
built — there's no global-read tier. If onboarding-without-membership is desired, this
needs either an auto-grant on onboarding or a genuine global-read tier on the org
namespace. Deliberately deferred; documented in `plugin/README.md` ("seeing no facts").

---

## Cross-cutting notes

- **`authoritative:true` stays governance-only.** It wins precedence (§14.9) and is a
  filter bypass. `tier:` is delivery-only. Keep the two from re-merging.
- **The plugin is the policy layer; the backend stays minimal.** Phase 1 added exactly
  one backend bypass; all tier *policy* lives in the hooks. Prefer keeping it that way
  — push complexity to the plugin/convention layer, not the governance core.
- **Push vs pull is the unifying lens.** Procedural/guardrails → push (the agent won't
  know to ask). Semantic/episodic → pull (large, per-turn-irrelevant; fetch on need).
  `tier:` is the encoding of that decision.
