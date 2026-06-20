#!/usr/bin/env bash
#
# seed.sh — seed a namespace + role grants on the DEPLOYED Baseline (Pi cluster),
# emulating org onboarding. Runs psql inside the in-cluster Postgres pod via
# kubectl exec (the DB is ClusterIP-only, not exposed on the LAN).
#
# Usage:
#   ./deploy/seed.sh                       # grants 'john' reader+contributor+reviewer on org
#   PRINCIPAL=alice ./deploy/seed.sh       # different principal
#   CONTEXT=k3s NAMESPACE=baseline ./deploy/seed.sh
#
# Add a teammate as reviewer (to exercise the full propose->approve loop without
# hitting separation-of-duties):
#   PRINCIPAL=reviewer-bob ROLES=reviewer ./deploy/seed.sh

set -euo pipefail

CONTEXT="${CONTEXT:-k3s}"
NAMESPACE="${NAMESPACE:-baseline}"
PRINCIPAL="${PRINCIPAL:-john}"
ROLES="${ROLES:-reader contributor reviewer}"
PG_USER="${PG_USER:-baseline}"
PG_DB="${PG_DB:-baseline}"

kc() { kubectl --context "$CONTEXT" -n "$NAMESPACE" "$@"; }

echo "==> finding the Postgres pod"
POD=$(kc get pod -l app.kubernetes.io/name=postgres -o jsonpath='{.items[0].metadata.name}')
echo "    $POD"

# The Bitnami subchart stores the app user's password in <release>-postgres under
# `password`. Pull it so psql can authenticate non-interactively (allow override).
if [ -z "${PGPASSWORD:-}" ]; then
  PGSECRET="${PGSECRET:-$(kc get secret -l app.kubernetes.io/name=postgres -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)}"
  PGPASSWORD=$(kc get secret "$PGSECRET" -o jsonpath='{.data.password}' 2>/dev/null | base64 -d)
fi
if [ -z "${PGPASSWORD:-}" ]; then
  echo "ERROR: could not resolve the Postgres password (set PGPASSWORD=... to override)" >&2
  exit 1
fi

# psql inside the pod with the password supplied via env.
psql_pod() { kc exec -i "$POD" -- env PGPASSWORD="$PGPASSWORD" psql -U "$PG_USER" -d "$PG_DB" "$@"; }

# Build the role-grant VALUES list.
ROLE_ARRAY=$(printf "'%s'," $ROLES); ROLE_ARRAY="ARRAY[${ROLE_ARRAY%,}]"

echo "==> seeding org namespace + granting '$PRINCIPAL' [$ROLES] + a sample fact"
psql_pod >/dev/null <<SQL
INSERT INTO namespaces (name, kind, policy)
VALUES ('org', 'org', '{"required_approvals":1}')
ON CONFLICT (name) DO NOTHING;

INSERT INTO memberships (principal, namespace_id, role)
SELECT '${PRINCIPAL}', id, r FROM namespaces n, unnest(${ROLE_ARRAY}) AS r
WHERE n.name = 'org'
ON CONFLICT DO NOTHING;

INSERT INTO facts (namespace_id, statement, subject, canonical_key, status, tags, created_by, valid_from)
SELECT id, 'All production deploys must go through CI.',
       '{"type":"deploy.policy","scope":"global"}'::jsonb, 'deploy.policy:global',
       'active', '{}', 'seed', now()
FROM namespaces WHERE name='org'
ON CONFLICT DO NOTHING;
SQL

echo "==> summary"
psql_pod -tc \
  "SELECT '  namespaces: '||count(*) FROM namespaces
   UNION ALL SELECT '  grants for ${PRINCIPAL}: '||count(*) FROM memberships WHERE principal='${PRINCIPAL}'
   UNION ALL SELECT '  active facts: '||count(*) FROM facts WHERE status='active';"

echo
# URL hint depends on where this was seeded: localhost for Docker Desktop, the
# MetalLB IP for the Pi cluster. Override with BASELINE_URL if needed.
if [ -z "${BASELINE_URL:-}" ]; then
  case "$CONTEXT" in
    docker-desktop) BASELINE_URL="http://localhost:8080" ;;
    *)              BASELINE_URL="http://<BASELINE_LB_IP>:8080" ;;
  esac
fi
echo "Done. Point your Claude MCP config at ${BASELINE_URL}/mcp with"
echo "header X-Baseline-Principal: ${PRINCIPAL} (see RUNNING.md)."
