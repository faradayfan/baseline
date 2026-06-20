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
# Trigger: I emit `[remember: <text>]` in a reply when something is worth keeping.
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

[ -n "$url" ] || exit 0
command -v curl >/dev/null 2>&1 || exit 0
command -v python3 >/dev/null 2>&1 || exit 0

# --- opt-in gate ---------------------------------------------------------------
project_dir="${CLAUDE_PROJECT_DIR:-$PWD}"
capture_on=""
case "${BASELINE_CAPTURE:-}" in 1|true|TRUE|yes|YES) capture_on=1 ;; esac
[ -f "$project_dir/.baseline-capture" ] && capture_on=1
[ -n "$capture_on" ] || exit 0
# ------------------------------------------------------------------------------

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
            parts = rec.get("message", {}).get("content", [])
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

spans = [m.strip() for m in re.findall(r"\[remember:\s*(.+?)\]", last_text,
                                       re.IGNORECASE | re.DOTALL)]
spans = [s for s in spans if s]
if not spans:
    sys.exit(0)

base = os.environ["BASELINE_URL"]
principal = os.environ.get("BASELINE_PRINCIPAL_RESOLVED", "")
token = os.environ.get("BASELINE_TOKEN", "")
saved = []
for text in spans:
    body = json.dumps({"content": text}).encode()
    headers = {"Content-Type": "application/json",
               "X-Baseline-Principal": principal}
    if token:
        headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(base + "/v1/memories", data=body,
                                 method="POST", headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=5) as resp:
            resp.read()
        saved.append(text)
    except Exception:
        pass  # 501 standards-only, network, etc. — never block.

if saved:
    sys.stderr.write("Baseline: captured %d memory(ies): %s\n"
                     % (len(saved), " | ".join(saved)))
PY
exit 0
