#!/usr/bin/env bash
# SessionStart hook: inject the ALWAYS-ON tier of Baseline facts ONCE per session.
#
# These are the mandatory guardrails the agent must always know (tagged
# `tier:always` in Baseline) — e.g. "run tests before committing", "use asdf".
# SessionStart stdout is loaded into context once and persists the whole session
# (it does NOT re-run every turn), so guardrails cost their tokens once instead of
# on every prompt. This is the always-on half of tiered injection; the per-turn,
# relevance-scoped half lives in inject-context.sh (UserPromptSubmit).
#
# The query `?tags=tier:always` returns exactly the always-on set: the backend's
# read-path filter lets tier:always facts bypass the tag match, so nothing else
# comes back. Config + auth + self-explain mirror inject-context.sh.

set -euo pipefail

url="${CLAUDE_PLUGIN_OPTION_BACKEND_URL:-${BASELINE_CONTEXT_URL:-}}"
principal="${CLAUDE_PLUGIN_OPTION_PRINCIPAL:-${BASELINE_PRINCIPAL:-}}"
token="${CLAUDE_PLUGIN_OPTION_API_TOKEN:-${BASELINE_API_TOKEN:-}}"

if [ -z "$url" ]; then
  echo "Baseline plugin: no backend_url configured — always-on facts not loaded. Set it in /plugin config (baseline), or see plugin/README.md." >&2
  exit 0
fi
command -v curl >/dev/null 2>&1 || exit 0

auth_args=()
[ -n "$token" ] && auth_args=(-H "Authorization: Bearer $token")

# tier:always facts only. No memories here — memories are not an always-on tier;
# they merge in per-turn via the UserPromptSubmit path.
body="$(curl -s --max-time 5 "${url%/}/v1/context?tags=tier:always" \
  -H "X-Baseline-Principal: ${principal}" ${auth_args[@]+"${auth_args[@]}"} 2>/dev/null || true)"

# Empty array or empty body → nothing to inject (no always-on facts configured).
[ -n "$body" ] && [ "$body" != "[]" ] || exit 0

printf 'Baseline guardrails (always-on org facts for this session):\n%s\n' "$body"
exit 0
