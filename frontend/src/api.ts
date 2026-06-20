import { useCallback, useMemo } from "react";
import { usePrincipal } from "./principal";

// --- response shapes (mirror the Go structs the endpoints return) ---

export interface Subject {
  type: string;
  scope?: string;
  qualifiers?: Record<string, string>;
}

export interface Provenance {
  origin_actor?: string;
  origin_session?: string;
  derived_from?: string[];
  rationale?: string;
}

export type FactStatus =
  | "proposed"
  | "active"
  | "revoked"
  | "superseded"
  | "expired";

export interface Fact {
  id: string;
  namespace_id: string;
  statement: string;
  subject: Subject;
  canonical_key: string;
  status: FactStatus;
  confidence?: number;
  source_memory_ids?: string[];
  provenance: Provenance;
  valid_from?: string;
  valid_to?: string;
  supersedes_id?: string;
  superseded_by_id?: string;
  tags?: string[];
  metadata?: Record<string, unknown>;
  created_by: string;
  approved_by?: string[];
  version: number;
  created_at: string;
  updated_at: string;
}

// audit.Event has no JSON tags, so Go marshals Go field names (PascalCase).
export interface AuditEvent {
  Principal: string;
  Action: string;
  SubjectType: string;
  SubjectID: string;
  FromState: string;
  ToState: string;
  Detail: unknown;
}

export type PromotionState =
  | "pending"
  | "in_review"
  | "changes_requested"
  | "approved"
  | "rejected";

export interface Review {
  reviewer: string;
  decision: "approve" | "reject" | "request_changes";
  comment?: string;
  at: string;
}

export interface Promotion {
  id: string;
  fact_id: string;
  target_namespace_id: string;
  proposed_statement: string;
  state: PromotionState;
  proposer: string;
  reviews: Review[];
  required_approvals: number;
  conflict_with?: string;
  created_at: string;
}

export interface Namespace {
  id: string;
  name: string;
  kind: "user" | "team" | "project" | "org";
  parent_id?: string;
  policy: {
    required_approvals: number;
    allowed_proposers?: string[];
    auto_promote?: { engine: string; rules?: unknown };
  };
  created_at: string;
}

export interface Member {
  principal: string;
  role: string;
}

export type ContextSource = "fact" | "memory";

export interface ContextItem {
  source: ContextSource;
  statement: string;
  namespace_id?: string;
  canonical_key?: string;
  confidence?: number;
  valid_to?: string;
  tags?: string[];
  metadata?: Record<string, unknown>;
}

// --- query option types ---

export interface FactQuery {
  status?: string;
  tags?: string;
  namespace?: string;
  q?: string;
  limit?: number;
}

export interface ContextQuery {
  include_memories?: boolean;
  tags?: string;
  limit?: number;
}

function qs(params: Record<string, string | number | boolean | undefined>): string {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== "" && v !== false) sp.set(k, String(v));
  }
  const s = sp.toString();
  return s ? `?${s}` : "";
}

// useApi returns typed read helpers over Baseline's GET endpoints. Identity
// rides on the X-Baseline-Principal header (the dev/POC identity mechanism) —
// there are no cookies. Every request carries the currently-selected principal.
export function useApi() {
  const { principal } = usePrincipal();

  const get = useCallback(
    async <T>(path: string): Promise<T> => {
      const res = await fetch(`/api${path}`, {
        headers: { "X-Baseline-Principal": principal },
      });
      if (!res.ok) {
        const detail = await res.text().catch(() => "");
        if (res.status === 401 || res.status === 403) {
          throw new Error(
            `not authorized as "${principal}" (${res.status})${detail ? `: ${detail}` : ""}`,
          );
        }
        throw new Error(`${res.status} ${res.statusText}${detail ? `: ${detail}` : ""}`);
      }
      if (res.status === 204) return undefined as T;
      return res.json() as Promise<T>;
    },
    [principal],
  );

  return useMemo(
    () => ({
      listFacts: (q: FactQuery = {}) => get<Fact[]>(`/facts${qs({ ...q })}`),
      getFact: (id: string) => get<Fact>(`/facts/${id}`),
      getFactHistory: (id: string) => get<AuditEvent[]>(`/facts/${id}/history`),

      listPromotions: (q: { state?: string; namespace?: string; proposer?: string } = {}) =>
        get<Promotion[]>(`/promotions${qs({ ...q })}`),
      getPromotion: (id: string) => get<Promotion>(`/promotions/${id}`),

      listNamespaces: () => get<Namespace[]>(`/namespaces`),
      getNamespace: (id: string) => get<Namespace>(`/namespaces/${id}`),
      listMembers: (id: string) => get<Member[]>(`/namespaces/${id}/members`),

      getContext: (q: ContextQuery = {}) => get<ContextItem[]>(`/context${qs({ ...q })}`),
    }),
    [get],
  );
}

// useNamespaceMap loads namespaces once and returns id→name lookup helper. Facts
// and promotions carry namespace_id; the UI shows the friendly name.
import { useEffect, useState } from "react";
export function useNamespaceMap(): (id?: string) => string {
  const api = useApi();
  const [map, setMap] = useState<Record<string, string>>({});
  useEffect(() => {
    let live = true;
    api
      .listNamespaces()
      .then((ns) => {
        if (!live) return;
        const m: Record<string, string> = {};
        for (const n of ns) m[n.id] = n.name;
        setMap(m);
      })
      .catch(() => {
        /* a non-entitled principal may not list every namespace; fall back to id */
      });
    return () => {
      live = false;
    };
  }, [api]);
  return (id?: string) => (id ? (map[id] ?? id.slice(0, 8)) : "—");
}
