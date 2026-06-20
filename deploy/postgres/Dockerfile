# Bitnami PostgreSQL 16 + the pgvector extension.
#
# Why: Baseline requires `CREATE EXTENSION vector`, but stock Bitnami Postgres
# does not ship pgvector. Building FROM the Bitnami base keeps the Bitnami Helm
# chart's battle-tested NFS/permissions handling (its volumePermissions init
# container, non-root uid 1001, entrypoint, env vars) — which a plain
# pgvector/pgvector Deployment lacks (it trips over NFS root-squash chown).
#
# Technique: copy the already-compiled vector.so + extension SQL/control files
# out of the official pgvector image (PG 16, Debian) into Bitnami's extension and
# lib dirs. No compiler/pg_config needed — PostgreSQL guarantees module ABI
# compatibility within a major version, so a 16.x-built vector.so loads on
# Bitnami's 16.x. Both images are arm64-capable for the Pi cluster.
#
# Build (multi-arch, for the Pi registry):
#   docker buildx build --platform linux/arm64 \
#     -t <REGISTRY_HOST>:5000/baseline-postgresql:16-pgvector --push deploy/postgres

ARG PGVECTOR_IMAGE=pgvector/pgvector:pg16
ARG BITNAMI_IMAGE=bitnamilegacy/postgresql:16

FROM ${PGVECTOR_IMAGE} AS pgv

FROM ${BITNAMI_IMAGE}

# Bitnami runs as uid 1001 / gid root; copy as root then it's read by 1001.
USER root

# Shared object → Bitnami's pkglibdir.
COPY --from=pgv /usr/lib/postgresql/16/lib/vector.so \
                /opt/bitnami/postgresql/lib/vector.so

# Extension SQL + control files → Bitnami's sharedir/extension.
COPY --from=pgv /usr/share/postgresql/16/extension/vector* \
                /opt/bitnami/postgresql/share/extension/

# Back to the non-root runtime user the Bitnami entrypoint expects.
USER 1001
