#!/usr/bin/env bash
# UserPromptSubmit hook: inject the RELEVANT tier of Baseline facts (+ personal
# memories) for THIS turn. Fires per-prompt; its output is per-turn context.
#
# This is the relevance-scoped half of tiered injection (the always-on half is
# inject-session.sh / SessionStart). It injects only facts that are BOTH:
#   - tagged `tier:relevant` (opted into per-turn injection), AND
#   - tagged with one of the topics this project declares in `.baseline-topics`.
# No `.baseline-topics` file → no relevance-injection (lean default). Facts that
# are untagged or `tier:ondemand` are never auto-injected — the agent pulls them
# via the search_facts / get_context MCP tools when it needs them.
#
# Why client-side tier filtering: Baseline's tag filter is OR-overlap, so
# `?tags=tier:relevant,backend,go` returns anything matching ANY of those. We want
# the AND of (tier:relevant) and (a topic), so we query by topics and then keep
# only items that ALSO carry tier:relevant — done in python below.
#
# Config: CLAUDE_PLUGIN_OPTION_* with BASELINE_* env fallback. Silent on every
# non-happy path; self-explains (stderr) only when truly unconfigured.

set -euo pipefail

url="${CLAUDE_PLUGIN_OPTION_BACKEND_URL:-${BASELINE_CONTEXT_URL:-}}"
principal="${CLAUDE_PLUGIN_OPTION_PRINCIPAL:-${BASELINE_PRINCIPAL:-}}"
token="${CLAUDE_PLUGIN_OPTION_API_TOKEN:-${BASELINE_API_TOKEN:-}}"

if [ -z "$url" ]; then
  echo "Baseline plugin: no backend_url configured — context injection is off. Set it in /plugin config (baseline), or see plugin/README.md → 'If the config prompt didn't fire'." >&2
  exit 0
fi
command -v curl >/dev/null 2>&1 || exit 0
command -v python3 >/dev/null 2>&1 || exit 0

# --- read project-declared topics --------------------------------------------
# .baseline-topics in the project root, comma- and/or newline-separated. No file
# → no topics → nothing relevance-injected this turn (lean default).
project_dir="${CLAUDE_PROJECT_DIR:-$PWD}"
topics=""
if [ -f "$project_dir/.baseline-topics" ]; then
  # strip comments (# ...), collapse newlines+commas into a single comma list.
  topics="$(sed -E 's/#.*$//' "$project_dir/.baseline-topics" \
    | tr '\n,' '  ' | tr -s ' ' | sed -E 's/^ +| +$//g; s/ /,/g')"
fi
[ -n "$topics" ] || exit 0
# -----------------------------------------------------------------------------

auth_args=()
[ -n "$token" ] && auth_args=(-H "Authorization: Bearer $token")

# Query by the declared topics (+ memories). The backend returns topic-matching
# facts (OR) plus any always-pass facts; python then keeps only tier:relevant
# items (so always-on guardrails — already loaded at SessionStart — aren't
# re-injected here) and renders them with memories.
body="$(curl -s --max-time 5 "${url%/}/v1/context?include_memories=true&tags=${topics}" \
  -H "X-Baseline-Principal: ${principal}" ${auth_args[@]+"${auth_args[@]}"} 2>/dev/null || true)"

[ -n "$body" ] || exit 0

BASELINE_BODY="$body" python3 - <<'PY' || exit 0
import json, os, sys

try:
    items = json.loads(os.environ.get("BASELINE_BODY", ""))
except Exception:
    sys.exit(0)
if not isinstance(items, list):
    sys.exit(0)

facts, memories = [], []
for it in items:
    src = it.get("source")
    tags = it.get("tags") or []
    if src == "memory":
        memories.append(it)
    elif src == "fact" and "tier:relevant" in tags:
        # Only the relevant tier is injected per-turn. Always-on guardrails were
        # loaded once at SessionStart; ondemand/untagged facts are pull-only.
        facts.append(it)

if not facts and not memories:
    sys.exit(0)

out = facts + memories  # facts first (higher precedence), then memories
print("Baseline facts relevant to this project + your memories (source: fact|memory):")
print(json.dumps(out))
PY
exit 0
