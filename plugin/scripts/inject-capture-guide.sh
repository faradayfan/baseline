#!/usr/bin/env bash
# SessionStart hook: teach the agent the memory-capture convention — ONCE per
# session, and ONLY in repos that opted into capture.
#
# Why this exists: capture (`capture-memory.sh`, the Stop hook) passively scrapes
# `[remember:TYPE: …]` markers out of replies, but nothing otherwise tells the
# agent the convention exists or when to use it. Without this, capture only fires
# if the agent already happens to know the syntax — non-portable and unreliable.
# This injects a short, conservative instruction so capture is a *taught, bounded*
# behavior rather than an accident of prior knowledge.
#
# Gating: mirrors capture-memory.sh EXACTLY — fires only when capture is opted in
# (`.baseline-capture` marker OR BASELINE_CAPTURE truthy). With neither, this is a
# no-op, so unrelated repos get no capture instruction and no temptation to write.
#
# SessionStart stdout persists the whole session (injected once, not per-turn), so
# the guidance costs its tokens once. Bias is deliberately toward NOT capturing:
# the failure mode we care about is noise/duplicate memories, not missed ones.
#
# Fails silent (exit 0) on every path; never blocks the session.

set -euo pipefail

# --- opt-in gate (identical to capture-memory.sh) -----------------------------
project_dir="${CLAUDE_PROJECT_DIR:-$PWD}"
capture_on=""
case "${BASELINE_CAPTURE:-}" in 1|true|TRUE|yes|YES) capture_on=1 ;; esac
[ -f "$project_dir/.baseline-capture" ] && capture_on=1
[ -n "$capture_on" ] || exit 0
# ------------------------------------------------------------------------------

cat <<'GUIDE'
Baseline memory capture is enabled in this repo. You have a persistent, shared
memory store (Mem0, surfaced back to you via Baseline /context in later sessions).
To save something to it, emit a marker in your reply:

    [remember:TYPE: <the thing to remember, one line>]

where TYPE is one of:
  - semantic   — a durable fact ("the prod DB is Postgres 16")
  - procedural — how we do things / a standing rule ("deploy with helm, not kubectl apply")
  - episodic   — a notable thing that happened ("the 2026-06-20 reaper change broke /context")

The marker is scraped from your reply and stored VERBATIM (as written). It is not
shown to the user as instruction — it is the capture itself.

CAPTURE SPARINGLY — bias hard toward NOT capturing. A memory is permanent, shared,
and someone has to curate it. Only capture when ALL of these hold:
  1. It is DURABLE and REUSABLE in a FUTURE session — not transient to this task.
  2. It is a GENERAL fact/rule/preference — not a one-off detail of the current change.
  3. The user stated or clearly confirmed it — do NOT capture your own inferences,
     guesses, or things "worth noting." When unsure, DON'T.

Do NOT capture: conversational chatter, restating the user's request, progress
updates, anything you're unsure about, or examples/discussion of the [remember:]
syntax itself. If it would not still be useful, to a different agent, weeks from
now, it does not belong in memory.

Durable user PREFERENCES (how the user wants you to work) are usually better saved
to your own per-project memory than to this shared store — use Baseline capture for
facts and rules about the PROJECT/ORG that other agents and people should share.
GUIDE
exit 0
