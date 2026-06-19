-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS vector;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE namespaces (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name       text UNIQUE NOT NULL,
  kind       text NOT NULL CHECK (kind IN ('user','team','project','org')),
  parent_id  uuid REFERENCES namespaces(id),
  policy     jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE facts (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  namespace_id      uuid NOT NULL REFERENCES namespaces(id),
  statement         text NOT NULL,
  subject           jsonb NOT NULL,            -- {type, scope?, qualifiers?} (§4.6)
  canonical_key     text NOT NULL,            -- = normalize(subject); never client-set
  status            text NOT NULL CHECK (status IN
                      ('proposed','in_review','active','superseded','revoked','expired','rejected')),
  confidence        numeric(3,2),
  source_memory_ids text[] NOT NULL DEFAULT '{}',
  provenance        jsonb NOT NULL DEFAULT '{}'::jsonb,
  valid_from        timestamptz,
  valid_to          timestamptz,
  supersedes_id     uuid REFERENCES facts(id),
  superseded_by_id  uuid REFERENCES facts(id),
  tags              text[] NOT NULL DEFAULT '{}',
  metadata          jsonb NOT NULL DEFAULT '{}'::jsonb,
  embedding         vector(768),
  created_by        text NOT NULL,
  approved_by       text[] NOT NULL DEFAULT '{}',
  version           int NOT NULL DEFAULT 1,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- Core invariant (§4.1, §14.2): at most one active fact per (namespace, canonical_key).
-- +goose StatementBegin
CREATE UNIQUE INDEX facts_active_unique
  ON facts (namespace_id, canonical_key) WHERE status = 'active';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX facts_embedding_idx ON facts USING hnsw (embedding vector_cosine_ops);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX facts_namespace_status_idx ON facts (namespace_id, status);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE promotion_requests (
  id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  fact_id              uuid NOT NULL REFERENCES facts(id),
  target_namespace_id  uuid NOT NULL REFERENCES namespaces(id),
  proposed_statement   text NOT NULL,
  state                text NOT NULL CHECK (state IN
                         ('pending','in_review','changes_requested','approved','rejected')),
  candidate_memory_ids text[] NOT NULL DEFAULT '{}',
  proposer             text NOT NULL,
  reviews              jsonb NOT NULL DEFAULT '[]'::jsonb,  -- [{reviewer,decision,comment,at}]
  required_approvals   int NOT NULL,
  auto_promote_decision jsonb,                              -- {engine,decision,reason,matched_rule} | null
  conflict_with        uuid REFERENCES facts(id),
  idempotency_key      text,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- Dedupe POSTs per proposer (§13 idempotency).
-- +goose StatementBegin
CREATE UNIQUE INDEX promotion_requests_idem_unique
  ON promotion_requests (proposer, idempotency_key) WHERE idempotency_key IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX promotion_requests_inbox_idx
  ON promotion_requests (target_namespace_id, state);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE memberships (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  principal    text NOT NULL,
  namespace_id uuid NOT NULL REFERENCES namespaces(id),
  role         text NOT NULL CHECK (role IN
                 ('reader','contributor','reviewer','namespace_admin')),
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (principal, namespace_id, role)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX memberships_principal_idx ON memberships (principal);
-- +goose StatementEnd

-- Append-only (§4.5). No UPDATE/DELETE is ever issued against this table.
-- +goose StatementBegin
CREATE TABLE audit_events (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  at           timestamptz NOT NULL DEFAULT now(),
  principal    text NOT NULL,
  action       text NOT NULL,
  subject_type text NOT NULL,
  subject_id   uuid,
  from_state   text,
  to_state     text,
  detail       jsonb NOT NULL DEFAULT '{}'::jsonb
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX audit_events_subject_idx ON audit_events (subject_type, subject_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS audit_events;
DROP TABLE IF EXISTS memberships;
DROP TABLE IF EXISTS promotion_requests;
DROP TABLE IF EXISTS facts;
DROP TABLE IF EXISTS namespaces;
-- +goose StatementEnd
