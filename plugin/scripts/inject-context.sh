#!/usr/bin/env bash
# UserPromptSubmit hook: inject the caller's Baseline /context (org facts +
# personal memories) above each prompt. Fires in EVERY project the plugin is
# enabled in — the org baseline is meant to be everywhere.
#
# Config comes from the plugin's userConfig (prompted at install), exposed as
# CLAUDE_PLUGIN_OPTION_*; falls back to the legacy BASELINE_* env vars so a
# hand-wired repo keeps working. Silent (exit 0) on every non-happy path.

set -euo pipefail

url="${CLAUDE_PLUGIN_OPTION_BACKEND_URL:-${BASELINE_CONTEXT_URL:-}}"
principal="${CLAUDE_PLUGIN_OPTION_PRINCIPAL:-${BASELINE_PRINCIPAL:-}}"
token="${CLAUDE_PLUGIN_OPTION_API_TOKEN:-${BASELINE_API_TOKEN:-}}"

[ -n "$url" ] || exit 0
command -v curl >/dev/null 2>&1 || exit 0

# Optional bearer auth (real deployments); omitted for the open dev/POC. Build
# the header as a plain string (empty when no token) — an empty array expansion
# under `set -u` is an error on older bash.
auth_args=()
[ -n "$token" ] && auth_args=(-H "Authorization: Bearer $token")

body="$(curl -s --max-time 5 "${url%/}/v1/context?include_memories=true" \
  -H "X-Baseline-Principal: ${principal}" ${auth_args[@]+"${auth_args[@]}"} 2>/dev/null || true)"

[ -n "$body" ] || exit 0
printf 'Baseline org facts + your memories (source: fact|memory):\n%s\n' "$body"
exit 0
