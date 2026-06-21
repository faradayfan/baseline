# Baseline — Local Architecture (Docker Desktop k8s + host Ollama)

The system as it actually runs **locally** (`make local-up`): a Docker Desktop
Kubernetes cluster in the `baseline` namespace, with **Ollama running natively on
the Mac** (reached via `host.docker.internal:11434`). Verified against the live
deployment, not idealized.

> Scope: this is the _local_ topology. The Pi deployment differs (Mem0 on OpenAI,
> no Baseline embedder → substring search). See `deploy/pi/values.yaml`.

## The one-paragraph story

A **Claude Code** session (the agent) talks to **Baseline** three ways: hooks
inject governed facts into the prompt, the MCP tools let the agent search/propose
facts, and a Stop hook captures `[remember:]` memories. Baseline is a **stateless
Go service** whose source of truth is its **own pgvector Postgres** (facts +
governance + audit). It _reads_ personal memories at request time from **Mem0**
(which has its own Postgres + Neo4j), and merges them below facts in `/context`.
Both Baseline and Mem0 use the **same host Ollama** for embeddings (and Mem0 also
for its extraction LLM). A **dashboard** reads Baseline over HTTP. A **reaper**
CronJob expires stale facts.

## Component / data-flow diagram

```mermaid
flowchart TB
    subgraph HOST["🖥️  Mac host (outside the cluster)"]
        OLLAMA["Ollama (native)<br/>:11434<br/>nomic-embed-text 768d · qwen2.5:3b"]
        CC["Claude Code session<br/>(Baseline plugin)"]
        DEV["Browser / curl"]
    end

    subgraph K8S["☸️  Docker Desktop k8s — namespace: baseline"]
        direction TB

        subgraph BL["Baseline (stateless Go)"]
            SVC["baseline-baseline<br/>LoadBalancer :8080<br/>HTTP API + /mcp + MCP-over-HTTP"]
            REAP["baseline-reaper<br/>CronJob */15m<br/>(same image, BASELINE_REAP)"]
        end
        UI["baseline-ui<br/>LoadBalancer :8081<br/>read-only dashboard"]
        BLPG[("baseline-postgres<br/>pgvector · StatefulSet<br/>facts · namespaces · audit · embeddings")]

        subgraph MEM["Mem0 (personal memory)"]
            M0["baseline-mem0-api<br/>ClusterIP :8000"]
            M0PG[("baseline-mem0-postgres<br/>pgvector · memory vectors")]
            NEO[("baseline-neo4j<br/>:7687 · memory graph")]
        end
    end

    %% --- agent / client edges ---
    CC -- "SessionStart + UserPromptSubmit hooks<br/>GET /v1/context (+tags, include_memories)" --> SVC
    CC -- "MCP tools: search_facts · propose_fact ·<br/>get_context · review_promotion<br/>(HTTP /mcp, X-Baseline-Principal)" --> SVC
    CC -- "Stop hook: POST /v1/memories<br/>[remember:TYPE:] verbatim (infer=false)" --> SVC
    DEV -- "HTTP" --> UI
    UI -- "GET /v1/facts, /promotions, /audit" --> SVC

    %% --- baseline internal edges ---
    SVC -- "facts · governance · audit (pgx)" --> BLPG
    REAP -- "expire past valid_to" --> BLPG
    SVC -- "embed fact on activation +<br/>embed q for /facts?q= search" --> OLLAMA

    %% --- memory edges ---
    SVC -- "read memories at request time<br/>(MemorySource: List/Search/Get)<br/>+ write-through POST /memories" --> M0
    M0 -- "vectors" --> M0PG
    M0 -- "graph relations" --> NEO
    M0 -- "extraction LLM + embeddings" --> OLLAMA

    classDef bl fill:#dbeafe,stroke:#2563eb,color:#1e3a8a;
    classDef store fill:#ede9fe,stroke:#7c3aed,color:#4c1d95;
    classDef mem fill:#dcfce7,stroke:#16a34a,color:#14532d;
    classDef host fill:#fef3c7,stroke:#d97706,color:#7c2d12;
    class SVC,REAP,UI bl;
    class BLPG,M0PG,NEO store;
    class M0 mem;
    class OLLAMA,CC,DEV host;
```

## What talks to what (verified edges)

| From                    | To                       | Protocol / path                                          | Purpose                                                                                            |
| ----------------------- | ------------------------ | -------------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| Claude Code (hooks)     | baseline `:8080`         | `GET /v1/context`                                        | inject tier'd facts (+ memories) into the prompt                                                   |
| Claude Code (MCP)       | baseline `:8080/mcp`     | MCP-over-HTTP, `X-Baseline-Principal`                    | `search_facts` (semantic), `propose_fact`, `get_context`, `review_promotion`, `list_my_promotions` |
| Claude Code (Stop hook) | baseline `:8080`         | `POST /v1/memories`                                      | capture `[remember:TYPE:]` verbatim (`infer=false`)                                                |
| Dashboard               | baseline `:8080`         | `GET /v1/facts · /promotions · /audit`                   | read-only governance views                                                                         |
| Baseline                | **its own** pgvector     | pgx/v5                                                   | facts, namespaces, audit, **fact embeddings** (source of truth)                                    |
| Baseline                | **host Ollama** `:11434` | `POST /api/embeddings`                                   | embed facts on activation; embed `q` for semantic search                                           |
| Baseline                | Mem0 `:8000`             | `MemorySource` (read) + `POST /memories` (write-through) | read personal memories at request time; capture pass-through                                       |
| Mem0                    | mem0-postgres (pgvector) | —                                                        | memory vectors                                                                                     |
| Mem0                    | Neo4j `:7687`            | bolt                                                     | memory graph relations                                                                             |
| Mem0                    | **host Ollama** `:11434` | —                                                        | extraction LLM (`qwen2.5:3b`) + embeddings (`nomic-embed-text`)                                    |
| Reaper CronJob          | baseline pgvector        | pgx                                                      | expire facts past `valid_to` (every 15m)                                                           |

## Load-bearing facts the picture encodes

- **Two separate pgvector databases.** Baseline owns facts in
  `baseline-postgres`; Mem0 owns memories in `baseline-mem0-postgres`. Baseline
  **never writes vectors to Mem0** — only neutral text/metadata crosses the
  boundary. Baseline owns its fact embeddings.
- **Baseline is the only governance authority.** Facts, promotions (propose →
  review → approve, separation-of-duties), and the append-only audit all live in
  Baseline's DB. Mem0 answers "what did this agent see?"; Baseline answers "what
  does the org officially know?"
- **`/context` is the merge point.** It returns precedence-resolved active facts
  in the caller's entitled namespaces, then merges personal memories _below_ all
  facts (memories never override a fact).
- **Ollama is shared but used for two different jobs.** Baseline uses it only for
  **embeddings** (semantic search + fact embedding). Mem0 uses it for both
  embeddings _and_ its extraction/dedup **LLM**.
- **Stateless service, two run-modes from one image.** The same `baseline:dev`
  image serves HTTP, runs the reaper (`BASELINE_REAP`), and can backfill
  embeddings (`BASELINE_EMBED_BACKFILL`) — mode chosen by env var.
- **Identity is a dev header today.** `X-Baseline-Principal` (HeaderAuthenticator)
  — real OIDC/mTLS is the deferred production seam.

---

## Sequence diagrams

Four temporal flows that the static picture above can't show: how a fact is
**proposed → reviewed → activated** (human path), how it's **auto-promoted**
(engine path), and how active facts get **injected into an agent's context**
(both the once-per-session and per-turn paths). Verified against the code in
`internal/promotions/service.go`, `internal/facts/`, and the plugin hooks.

### 1. Propose → review → approve (the human governance path)

A contributor proposes a fact; the server derives its canonical identity and
detects conflicts; distinct reviewers approve; on the threshold approval the fact
goes **active** (superseding any prior fact for the same key) — every transition
writing exactly one audit event.

```mermaid
sequenceDiagram
    autonumber
    actor Alice as Alice (contributor)
    actor Bob as Bob (reviewer)
    participant API as Baseline server
    participant PG as pgvector DB
    participant OL as Ollama

    Note over Alice,PG: PROPOSE
    Alice->>API: POST /v1/promotions<br/>{subject, statement, target_namespace, tags}
    API->>API: canonical_key = normalize(subject)<br/>(server-derived, never client-set)
    API->>PG: FindActiveByKey(ns, key) → conflict?
    API->>PG: insert fact (status=proposed) +<br/>promotion (snapshot required_approvals)
    API->>PG: audit: fact.proposed
    API-->>Alice: 201 {promotion_id, fact_id, conflict_with?}

    Note over Alice,API: SUBMIT FOR REVIEW
    Alice->>API: POST /promotions/{id}/submit
    API->>PG: promotion→in_review · audit: promotion.submitted

    Note over Bob,PG: REVIEW (separation of duties: Alice≠approver)
    Bob->>API: POST /promotions/{id}/approve
    API->>API: distinct non-proposer approvals ≥ required?
    alt threshold NOT reached
        API->>PG: record approval · audit: promotion.approved (state stays in_review)
        API-->>Bob: 200 (awaiting more approvals)
    else threshold reached
        opt conflicting active fact exists
            API->>PG: supersede prior fact (lineage both ways)<br/>audit: fact.superseded
        end
        API->>PG: fact→active
        API->>OL: Embed(subject + statement)
        OL-->>API: vector 768d
        API->>PG: store fact embedding — best-effort, NULL on failure
        API->>PG: audit: fact.activated + promotion.approved
        API-->>Bob: 200 (fact is now active)
    end
```

**Key invariants in this flow:** `canonical_key` is derived server-side from the
structured `subject` (step 2), never parsed from prose; the proposer can **never**
be a counted approver (separation of duties); supersede-then-activate ordering
honors the one-active-fact-per-key unique index; embedding is **best-effort** and
never blocks activation.

### 2. Propose → auto-promote (the engine path)

When the target namespace pins an `AutoPromoteEngine` (e.g. `team` →
`simple/v1`) and the candidate matches the rules, activation happens **inside the
propose transaction** — no human review. It **fails closed**: any engine error,
unknown engine, or non-match falls through to the human path above.

```mermaid
sequenceDiagram
    autonumber
    actor Agent as Agent / contributor
    participant API as Baseline server
    participant ENG as AutoPromoteEngine<br/>(simple/v1)
    participant PG as pgvector DB
    participant OL as Ollama

    Agent->>API: POST /v1/promotions {subject, statement, ns=team, …}
    API->>API: canonical_key = normalize(subject)
    API->>PG: insert fact (proposed) + promotion · audit: fact.proposed
    API->>ENG: Evaluate(candidate, rules)
    alt engine says auto-promote
        opt conflict exists
            API->>PG: supersede prior · audit: fact.superseded<br/>(principal = engine:simple/v1)
        end
        API->>PG: fact→active · tag auto:true
        API->>OL: Embed(...) → store embedding (best-effort)
        API->>PG: audit: fact.auto_promoted<br/>(principal = engine:simple/v1)
        API-->>Agent: 201 (fact already active)
    else error / no match → FAIL CLOSED
        API-->>Agent: 201 (promotion stays pending → human review = flow #1)
    end
```

**Why fail-closed matters:** uncertainty never auto-approves. The audit
attributes the action to `engine:simple/v1` (not a human), and the fact carries
`auto:true` — so an auto-promoted fact is always distinguishable and traceable.

### 3a. Inject ALWAYS-ON facts (once per session)

At session start the plugin pulls the mandatory guardrails — `tier:always` facts
— and prints them into the agent's context. They persist the whole session, paid
for once.

```mermaid
sequenceDiagram
    autonumber
    participant CC as Claude Code<br/>(SessionStart hook)
    participant API as Baseline server
    participant PG as pgvector DB

    Note over CC: new session begins
    CC->>API: GET /v1/context?tags=tier:always<br/>(X-Baseline-Principal)
    API->>API: resolve caller's readable namespaces (entitlements)
    API->>PG: SELECT active facts in those namespaces,<br/>tier:always always passes the tag filter
    API->>API: precedence-resolve per canonical_key<br/>user ▸ project ▸ team ▸ org · authoritative wins
    API-->>CC: [active tier:always facts]
    CC->>CC: print as "Baseline guardrails" →<br/>persists for the whole session
```

### 3b. Inject RELEVANT facts (per turn)

On every user prompt, the plugin injects only facts that are **both**
`tier:relevant` **and** tagged with a topic this repo declares in
`.baseline-topics` — plus the caller's personal memories (ranked below facts).
Because Baseline's tag filter is OR-overlap, the hook queries by topic then
**client-side-filters** to the AND with `tier:relevant`.

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant CC as Claude Code<br/>(UserPromptSubmit hook)
    participant API as Baseline server
    participant PG as pgvector DB
    participant M0 as Mem0

    User->>CC: submits a prompt
    CC->>CC: read .baseline-topics → topics=[deploy,backend,…]<br/>(no file → skip, lean default)
    CC->>API: GET /v1/context?include_memories=true&tags={topics}
    API->>API: scope to readable namespaces
    API->>PG: active facts matching ANY topic (OR)<br/>+ authoritative:true / tier:always always-pass
    API->>API: precedence-resolve per canonical_key
    API->>M0: read personal memories (MemorySource)
    M0-->>API: memories (text + metadata.type)
    API->>API: merge: memories BELOW all facts<br/>(never override a fact)
    API-->>CC: facts (precedence-ordered) then memories
    CC->>CC: keep only items ALSO tagged tier:relevant<br/>(the AND), inject into this turn
    Note over CC,User: agent answers with relevant facts in context
```

**The two-tier design in one line:** `tier:always` is the _push-once_ baseline
(SessionStart); `tier:relevant` + `.baseline-topics` is the _push-per-turn-when-
on-topic_ set (UserPromptSubmit); everything else is `tier:ondemand` — the agent
pulls it via the `search_facts` / `get_context` MCP tools only when it needs it.
