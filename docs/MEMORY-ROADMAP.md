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

## Phase 2 — typed capture *(SHIPPED — minimal slice)*

Phase 1 used only `tier:` (delivery). Phase 2 adds the cognitive **type** on the
**write** side. Capture was single-channel (`[remember: …]` → Mem0, undifferentiated).

**Shipped:**

- **Typed capture:** `[remember:TYPE: …]` where TYPE ∈ `semantic | procedural |
  episodic`. Explicit type wins; untyped `[remember: …]` defaults to `semantic`; an
  unrecognized prefix is treated as untyped (the prefix stays in the text).
- **Routing decision (settled): all → Mem0 with `metadata.type`.** Everything goes to
  `/v1/memories` carrying `{type}`; the type is recorded, not used to auto-create
  facts. This avoids the separation-of-duties wall — promotion to a governed fact
  stays a deliberate, reviewed human step (the proposer can't self-approve). The
  backend already forwards `metadata` end-to-end (mem0 adapter → Mem0).
- **Verbatim storage (`infer=false`).** Mem0's `POST /memories` runs an extraction
  LLM that rewrites/drops input (the local `qwen2.5:3b` silently dropped longer
  procedural captures). Since `[remember:]` is deliberate, intentionally-phrased
  capture, the hook now posts `infer:false` → Mem0 stores the text **verbatim**, no
  extraction. Threaded through `memory.AddOpts{Infer *bool}` → mem0 adapter →
  `/v1/memories`, and exposed on Mem0's REST `MemoryCreate` via the
  `deploy/mem0-api` image patch (stock OSS omits the field). *This is the right mode
  precisely because we've pre-judged the capture; extraction stays reserved for
  Phase 3 episodic auto-capture, where distilling a transcript is the goal — that's
  the path a bigger local Ollama extraction model would serve.*
- Touch points: `Stop` hook (`capture-memory.sh`), `internal/memory` + handler,
  `deploy/mem0-api/patch_config.py`; docs in `plugin/README.md`.

**Deferred within the type story (Phase 2b, when the governance dependency is ready):**

- **Type → `tier:` on promotion:** a captured memory's `type:` should suggest its
  `tier:` when promoted (procedural→often `tier:always`, semantic→`tier:relevant`).
  Needs the promotion path, which needs the solo-approver/auto-promote story (Phase 5).
- **Type-routed destinations:** sending some types to `propose_fact` instead of Mem0
  — deferred because the Stop hook would have to construct a `subject`/namespace and
  the proposals pile up un-approvable (separation of duties).
- **Open decision (still deferred):** keep `type:`/`tier:` as **tag conventions**, or
  promote to first-class **`memory_type`/`delivery` columns** on `facts`. Tag
  convention = zero schema change; column = queryable/validated. Decide when
  type-specific behavior actually needs enforcing.

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

## Phase 6 — Memory mining (proposal-only fact inference)

Closes the loop the whole system is named for. Today the memory→fact transition is
**entirely human-initiated**: someone has to *notice* that a recurring memory is
fact-worthy, re-author it into a structured `subject`/`statement`, and propose it. Mem0
answers "what has the agent seen?"; nothing automatically asks "...and what of that
should the org adopt?" This phase adds a server-side analyzer that **mines memories and
proposes candidate facts** — and *only* proposes.

**The load-bearing constraint (decided): the analyzer proposes; it never promotes.**
It can only ever fill the review inbox. A human (or a namespace's separately-configured
`AutoPromoteEngine`) owns the decision to make a candidate an active fact. This keeps
the analyzer strictly *additive* to the trust model — it introduces **no new write path
to facts**, only a new *source of proposals*. An org that wants every memory-derived
fact eyeballed simply doesn't auto-promote that namespace; the candidates land in the
human review queue. The analyzer's principal (`engine:memory-miner/vN`) is the
**proposer**, so the existing separation-of-duties gate (`distinctApprovers()` excludes
the proposer, unconditionally) already forbids it from ever counting toward approval —
enforced for free, no new code.

**Pipeline — only the analyzer is new; everything downstream already exists:**

```text
Mem0 memories ─▶ [analyzer] ─▶ candidate proposals ─▶ review inbox / AutoPromoteEngine ─▶ facts
   (raw)         cluster +       POST /promotions       (human promotes; engine is          (active,
                 infer subject   w/ candidate_memory_ids  opt-in per namespace)               audited)
```

| Stage | What it does | Status |
| --- | --- | --- |
| **Cluster** | Group memories that say the same thing (own 768-dim embedder). Recurrence is the signal — one memory is noise; the same thing across 5 sessions is fact-worthy. | new |
| **Infer subject** | Derive a structured `{type, scope, qualifiers}` from free text. The genuinely novel inference — and **LLM-dependent**, the very thing the fact write-path avoids. Must be a *suggestion*, never authoritative. | new |
| **Draft statement** | Produce a clean `proposed_statement`. | new (easy) |
| **Conflict-check** | Does the inferred `canonical_key` collide with an active fact? | **free** — `POST /promotions` already sets `conflict_with` |
| **Propose** | `POST /promotions` with `candidate_memory_ids` = the cluster. | **free** — provenance field exists for exactly this |
| **Decide** | Human review, or a namespace's opt-in engine. | **free** — never the analyzer's call |

`candidate_memory_ids` on `PromotionRequest` was added for precisely this: provenance
from a machine-generated proposal. The analyzer is "the thing that finally populates it
at scale."

**Fail-closed on inference (mirrors the AutoPromoteEngine discipline).** If subject
inference is low-confidence, the analyzer must **not** guess a `canonical_key` — drop
the candidate, or emit it flagged "needs a human to supply the subject." A *wrong*
subject is worse than no proposal: it pollutes deterministic conflict detection. The
inferred subject is a *proposal to be validated*, never ground truth — the moment an
inferred subject could write `canonical_key` directly, the determinism §14 protects
would break. The LLM is reintroduced in the *right* place (candidate generation),
downstream of which conflict-check and review stay deterministic.

**Where it runs.** The existing **reaper** (`BASELINE_REAP=true`, a background worker on
the same binary) is the model: a second periodic job (`BASELINE_MINE=true`, or a tick in
the reaper) that scans, clusters, infers, and proposes. Stateless, restartable, fits the
process model. Must be **version-isolated** (`memory-miner/vN` — registering `v2` doesn't
change what `v1` proposed) and **auditable** (proposals carry `principal=engine:memory-miner/vN`).

**Open questions (deferred with the phase):**

- *Idempotency/churn:* don't re-propose the same cluster every tick — track which
  memory clusters have already yielded a (pending or rejected) proposal so a rejected
  candidate doesn't reappear forever.
- *De-noising the source:* mine which memory types? Semantic/procedural are fact-shaped;
  episodic ("what happened") usually isn't — likely filter to `type:semantic|procedural`.
- *Promote-and-prune:* once a memory's content is adopted as a fact, the memory still
  surfaces (lower-ranked) in `/context`. Whether to annotate/suppress the now-redundant
  memory is the memory→fact *ergonomics* gap noted under Phase 2b — same open thread.

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
