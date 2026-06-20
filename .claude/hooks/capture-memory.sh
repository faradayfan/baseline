#!/usr/bin/env bash
# Stop hook: capture [remember: ...] tags from my final reply into Baseline's
# out-of-band memory store (which proxies to Mem0). This wires the Claude Code
# harness into the memory-capture path the spec assumes an agent provides
# (§1/§11: Mem0 answers "what has this agent seen?", fed by the agent runtime).
#
# Trigger: I emit `[remember: <text>]` in a reply when something is worth keeping.
# This hook scrapes those spans from the last assistant turn and POSTs each to
#   $BASELINE_CONTEXT_URL/v1/memories  (principal = $BASELINE_PRINCIPAL)
# Mem0 then runs its own extraction; the memory later surfaces in /context as
# source:memory. Nothing is captured unless the explicit tag is present.
#
# Reads the Stop-hook JSON event on stdin: {"transcript_path": "...", ...}.
# Stays silent (exit 0) on every non-happy path so it never blocks the session.

set -euo pipefail

# No backend configured → nothing to do (same gate as the context hook).
[ -n "${BASELINE_CONTEXT_URL:-}" ] || exit 0
command -v curl >/dev/null 2>&1 || exit 0
command -v python3 >/dev/null 2>&1 || exit 0

event="$(cat)"

# Extract the [remember: ...] spans from the LAST assistant message in the
# transcript, then POST each. All parsing is in python3 (robust JSONL handling);
# the shell just feeds it the event + env.
# Pass the event via env, NOT stdin: stdin is consumed by the `<<'PY'` heredoc
# that delivers this script to python3, so sys.stdin is unavailable here.
BASELINE_HOOK_EVENT="$event" \
  BASELINE_CONTEXT_URL="$BASELINE_CONTEXT_URL" \
  BASELINE_PRINCIPAL="${BASELINE_PRINCIPAL:-}" python3 - <<'PY' || exit 0
import json, os, re, sys, urllib.request

try:
    event = json.loads(os.environ.get("BASELINE_HOOK_EVENT", ""))
except Exception:
    sys.exit(0)

path = event.get("transcript_path")
if not path or not os.path.exists(path):
    sys.exit(0)

# Find the last assistant message's text in the JSONL transcript.
last_text = None
try:
    with open(path, "r") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                rec = json.loads(line)
            except Exception:
                continue
            if rec.get("type") != "assistant":
                continue
            msg = rec.get("message", {})
            parts = msg.get("content", [])
            if isinstance(parts, str):
                last_text = parts
            else:
                texts = [p.get("text", "") for p in parts
                         if isinstance(p, dict) and p.get("type") == "text"]
                if texts:
                    last_text = "\n".join(texts)
except Exception:
    sys.exit(0)

if not last_text:
    sys.exit(0)

# [remember: ...] — non-greedy, allows it to span until the closing bracket.
spans = [m.strip() for m in re.findall(r"\[remember:\s*(.+?)\]", last_text, re.IGNORECASE | re.DOTALL)]
spans = [s for s in spans if s]
if not spans:
    sys.exit(0)

base = os.environ["BASELINE_CONTEXT_URL"].rstrip("/")
principal = os.environ.get("BASELINE_PRINCIPAL", "")
saved = []
for text in spans:
    body = json.dumps({"content": text}).encode()
    req = urllib.request.Request(
        base + "/v1/memories", data=body, method="POST",
        headers={"Content-Type": "application/json",
                 "X-Baseline-Principal": principal})
    try:
        with urllib.request.urlopen(req, timeout=5) as resp:
            resp.read()
        saved.append(text)
    except Exception:
        # Silently skip a failed write (e.g. 501 standards-only) — never block.
        pass

# Surface a small confirmation to the user via stderr (Stop-hook stdout is not
# shown; stderr at exit 0 is visible in transcript/verbose).
if saved:
    sys.stderr.write("Baseline: captured %d memory(ies): %s\n"
                     % (len(saved), " | ".join(saved)))
PY
exit 0
