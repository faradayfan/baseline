#!/usr/bin/env bash
# Stop hook: capture [remember: ...] tags from my final reply into Baseline's
# out-of-band memory store (which proxies to Mem0). This wires the Claude Code
# harness into the memory-capture path the spec assumes an agent provides
# (Mem0 answers "what has this agent seen?", fed by the agent runtime).
#
# OPT-IN: unlike context injection (which fires everywhere), capture is gated so
# it does NOT fire in unrelated repos. It runs only when EITHER:
#   - a `.baseline-capture` marker file exists in the project root, OR
#   - BASELINE_CAPTURE is set to a truthy value (1/true/yes).
# This keeps "[remember: …]" from silently writing memories in every project.
#
# Trigger: I emit `[remember: <text>]` in a reply when something is worth keeping,
# optionally typed: `[remember:procedural: <text>]`. The cognitive TYPE (one of
# semantic | procedural | episodic) is sent as metadata.type on the memory so it
# can inform later treatment (e.g. which tier: it gets if promoted to a fact).
# Explicit type wins; an untyped `[remember: ...]` defaults to type:semantic.
# This hook scrapes those spans from the last assistant turn and POSTs each to
#   <backend_url>/v1/memories  (principal = configured principal).
# Mem0 then runs its own extraction; the memory later surfaces in /context.
#
# Reads the Stop-hook JSON event on stdin: {"transcript_path": "...", ...}.
# Stays silent (exit 0) on every non-happy path so it never blocks the session.

set -euo pipefail

url="${CLAUDE_PLUGIN_OPTION_BACKEND_URL:-${BASELINE_CONTEXT_URL:-}}"
principal="${CLAUDE_PLUGIN_OPTION_PRINCIPAL:-${BASELINE_PRINCIPAL:-}}"
token="${CLAUDE_PLUGIN_OPTION_API_TOKEN:-${BASELINE_API_TOKEN:-}}"

command -v curl >/dev/null 2>&1 || exit 0
command -v python3 >/dev/null 2>&1 || exit 0

# --- opt-in gate (checked FIRST, so the not-configured notice below only fires
# in repos that actually opted into capture — not every repo) -------------------
project_dir="${CLAUDE_PROJECT_DIR:-$PWD}"
capture_on=""
case "${BASELINE_CAPTURE:-}" in 1|true|TRUE|yes|YES) capture_on=1 ;; esac
[ -f "$project_dir/.baseline-capture" ] && capture_on=1
[ -n "$capture_on" ] || exit 0
# ------------------------------------------------------------------------------

# Opted into capture but no backend configured → self-explain (stderr, visible to
# user, doesn't enter context), then exit cleanly. Defaults should prevent this.
if [ -z "$url" ]; then
  echo "Baseline plugin: capture is enabled here but no backend_url is configured — [remember: …] tags can't be saved. Set it in /plugin config (baseline), or see plugin/README.md." >&2
  exit 0
fi

event="$(cat)"

# Parsing/POST in python3 (robust JSONL handling); pass the event via env, NOT
# stdin — stdin is consumed by the `<<'PY'` heredoc delivering this script.
BASELINE_HOOK_EVENT="$event" \
  BASELINE_URL="${url%/}" \
  BASELINE_PRINCIPAL_RESOLVED="$principal" \
  BASELINE_TOKEN="$token" python3 - <<'PY' || exit 0
import json, os, re, sys, urllib.request

try:
    event = json.loads(os.environ.get("BASELINE_HOOK_EVENT", ""))
except Exception:
    sys.exit(0)

path = event.get("transcript_path")
if not path or not os.path.exists(path):
    sys.exit(0)

# Collect assistant text from the CURRENT TURN — every assistant text block since
# the most recent GENUINE user prompt. A single turn produces many assistant
# messages (text interleaved with tool_use), and a [remember:] marker usually sits
# in an earlier text block, not the final message (which is often a tool_use with
# no text). Reading only the last message silently misses the marker.
#
# CRITICAL: tool results are recorded as `type:"user"` messages whose content is a
# `tool_result` block — they are NOT real user prompts. Resetting the turn on those
# would wipe markers written before a tool ran (the common case, since a capture is
# usually followed by tool calls). So reset ONLY on a genuine user prompt: a `user`
# message carrying actual text (a string, or a `text` content block), never one
# that's only tool_result(s). This scopes capture to one real turn (Stop fires per
# turn) without being fooled by mid-turn tool results.
def is_real_user_prompt(rec):
    parts = rec.get("message", {}).get("content", [])
    if isinstance(parts, str):
        return parts.strip() != ""
    if isinstance(parts, list):
        return any(isinstance(p, dict) and p.get("type") == "text" and p.get("text")
                   for p in parts)
    return False

last_text = None
try:
    turn_chunks = []
    with open(path, "r") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                rec = json.loads(line)
            except Exception:
                continue
            rtype = rec.get("type")
            if rtype == "user":
                if is_real_user_prompt(rec):
                    turn_chunks = []  # genuine new turn — drop prior assistant text
                # else: a tool_result masquerading as user — do NOT reset.
                continue
            if rtype != "assistant":
                continue
            parts = rec.get("message", {}).get("content", [])
            if isinstance(parts, str):
                if parts:
                    turn_chunks.append(parts)
            else:
                for p in parts:
                    if isinstance(p, dict) and p.get("type") == "text" and p.get("text"):
                        turn_chunks.append(p["text"])
    if turn_chunks:
        last_text = "\n".join(turn_chunks)
except Exception:
    sys.exit(0)

if not last_text:
    sys.exit(0)

# Match ONLY `[remember:TYPE: text]` with an explicit TYPE
# (semantic | procedural | episodic), single-line, text not containing `]`.
#
# An explicit type is REQUIRED (no bare `[remember: …]`) and the match cannot span
# newlines or a `]`. This is deliberate: these markers are discussed constantly in
# prose ("the `[remember:]` tag", "10 `[remember:semantic` markers…"), and a loose
# pattern matched that meta-discussion, creating false captures. Requiring the full
# `[remember:TYPE: …]` shape makes a real capture unambiguous and impossible to
# trigger by merely talking about the syntax.
TYPES = {"semantic", "procedural", "episodic"}

# A capture is a PLACEHOLDER (illustrative syntax, not a real memory) when its text
# is just an ellipsis / angle-bracket stand-in / "text" filler. This is the actual
# recurring-bug fix: the strict regex still matches `[remember:procedural: …]` when
# that span is written as an EXAMPLE while explaining the convention (in prose, the
# README, the capture-guide). `…` is a valid text capture, so the regex can't
# exclude it — but it is never a memory anyone wants. The "don't capture the syntax"
# instruction is prose to the agent; the hook must ENFORCE it, since the agent
# doesn't run the hook. Reject these spans outright.
#
# Match is on the placeholder SHAPE, not a blocklist of phrases: text that, once
# stripped of ellipses / <…>-brackets / surrounding quotes, has no real content.
def is_placeholder(text):
    t = text.strip()
    # Bare ellipsis forms: "…", "...", "… …", unicode or ascii.
    if re.fullmatch(r"[.…\s]+", t):
        return True
    # Angle-bracket stand-ins: "<the thing to remember>", "<text>", "<...>".
    if re.fullmatch(r"<[^>]*>", t):
        return True
    # Generic filler tokens used in docs/examples.
    if t.strip("<>\"'` ").lower() in {"text", "the thing to remember",
                                      "the thing to remember, one line",
                                      "your memory here", "memory", "example"}:
        return True
    return False

spans = []
for mtype, text in re.findall(
        r"\[remember:(semantic|procedural|episodic):\s*([^\]\n]+?)\s*\]",
        last_text, re.IGNORECASE):
    text = text.strip()
    if text and not is_placeholder(text):
        spans.append((mtype.lower(), text))
if not spans:
    sys.exit(0)

base = os.environ["BASELINE_URL"]
principal = os.environ.get("BASELINE_PRINCIPAL_RESOLVED", "")
token = os.environ.get("BASELINE_TOKEN", "")
saved = []
for mtype, text in spans:
    # infer=false → store the text VERBATIM. A [remember:] capture is deliberate
    # and intentionally phrased; we don't want Mem0's extraction LLM to rewrite or
    # drop it. (Requires the patched mem0-api image; stock ignores the field.)
    body = json.dumps({"content": text, "metadata": {"type": mtype},
                       "infer": False}).encode()
    headers = {"Content-Type": "application/json",
               "X-Baseline-Principal": principal}
    if token:
        headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(base + "/v1/memories", data=body,
                                 method="POST", headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=5) as resp:
            resp.read()
        saved.append("%s:%s" % (mtype, text))
    except Exception:
        pass  # 501 standards-only, network, etc. — never block.

if saved:
    sys.stderr.write("Baseline: captured %d memory(ies): %s\n"
                     % (len(saved), " | ".join(saved)))
PY
exit 0
