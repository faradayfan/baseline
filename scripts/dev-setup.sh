#!/usr/bin/env bash
#
# dev-setup.sh — one-shot local setup for Baseline.
# Starts Postgres+pgvector, builds the binary, applies migrations (on first run
# of the binary), and seeds a usable namespace + grants + a sample fact.
#
# Idempotent: safe to re-run. Reuses an existing container and skips seeded rows.
#
# Usage:  ./scripts/dev-setup.sh
# Env override examples:
#   PG_PORT=5444 PRINCIPAL=me ./scripts/dev-setup.sh

set -euo pipefail
cd "$(dirname "$0")/.."

PG_PORT="${PG_PORT:-5433}"
PG_CONTAINER="${PG_CONTAINER:-baseline-pg}"
PRINCIPAL="${PRINCIPAL:-local-dev}"
DB_URL="postgres://baseline:baseline@localhost:${PG_PORT}/baseline?sslmode=disable"

echo "==> 1/4  Postgres + pgvector (${PG_CONTAINER} on :${PG_PORT})"
if docker ps --format '{{.Names}}' | grep -qx "${PG_CONTAINER}"; then
  echo "    already running"
elif docker ps -a --format '{{.Names}}' | grep -qx "${PG_CONTAINER}"; then
  docker start "${PG_CONTAINER}" >/dev/null
  echo "    started existing container"
else
  docker run -d --name "${PG_CONTAINER}" \
    -e POSTGRES_PASSWORD=baseline -e POSTGRES_USER=baseline -e POSTGRES_DB=baseline \
    -p "${PG_PORT}:5432" pgvector/pgvector:pg16 >/dev/null
  echo "    created new container"
fi

echo -n "    waiting for postgres"
for _ in $(seq 1 30); do
  if docker exec "${PG_CONTAINER}" pg_isready -U baseline >/dev/null 2>&1; then echo " ✓"; break; fi
  echo -n "."; sleep 1
done

echo "==> 2/4  Build ./bin/baseline"
go build -o ./bin/baseline ./cmd/baseline
echo "    built"

echo "==> 3/4  Apply migrations (one-shot run of the binary)"
# The binary auto-migrates on startup. Run the reaper mode once: it connects,
# migrates, and exits — a clean way to apply schema without leaving a server up.
DATABASE_URL="${DB_URL}" MEMORY_SOURCE=none BASELINE_REAP=true ./bin/baseline >/dev/null 2>&1
echo "    migrations applied"

echo "==> 4/4  Seed namespace, grants for '${PRINCIPAL}', and a sample fact"
docker exec -i "${PG_CONTAINER}" psql -U baseline -d baseline >/dev/null <<SQL
INSERT INTO namespaces (name, kind, policy)
VALUES ('org', 'org', '{"required_approvals":1}')
ON CONFLICT (name) DO NOTHING;

INSERT INTO memberships (principal, namespace_id, role)
SELECT '${PRINCIPAL}', id, r FROM namespaces n,
  unnest(ARRAY['reader','contributor','reviewer']) AS r
WHERE n.name = 'org'
ON CONFLICT DO NOTHING;

INSERT INTO facts (namespace_id, statement, subject, canonical_key, status, tags, created_by, valid_from)
SELECT id, 'All production deploys must go through CI.',
       '{"type":"deploy.policy","scope":"global"}'::jsonb, 'deploy.policy:global',
       'active', '{}', 'seed', now()
FROM namespaces WHERE name='org'
ON CONFLICT DO NOTHING;
SQL
echo "    seeded"

echo
echo "Done. Summary:"
docker exec "${PG_CONTAINER}" psql -U baseline -d baseline -tc \
  "SELECT '  namespaces: '||count(*) FROM namespaces
   UNION ALL SELECT '  grants for ${PRINCIPAL}: '||count(*) FROM memberships WHERE principal='${PRINCIPAL}'
   UNION ALL SELECT '  active facts: '||count(*) FROM facts WHERE status='active';"
echo
echo "Next: write .mcp.json (see RUNNING.md) and reload the Claude extension."
