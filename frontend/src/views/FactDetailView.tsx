import { Link, useParams } from "react-router-dom";
import { useApi, useNamespaceMap } from "../api";
import type { AuditEvent, Fact } from "../api";
import { Async, AuditTimeline, ShortId, StatusBadge, TagChips, fmtDate } from "../components";

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="label">{label}</div>
      <div className="text-sm">{children}</div>
    </div>
  );
}

export default function FactDetailView() {
  const { id = "" } = useParams();
  const api = useApi();
  const nsName = useNamespaceMap();

  return (
    <div className="space-y-4">
      <Link to="/facts" className="text-sm text-brand-600 hover:underline">
        ← Facts
      </Link>

      <Async<Fact> load={() => api.getFact(id)} deps={[id, api]}>
        {(f) => (
          <>
            <div className="flex items-start justify-between gap-4">
              <h1 className="text-xl font-semibold">{f.statement}</h1>
              <StatusBadge status={f.status} />
            </div>

            <div className="card grid grid-cols-2 gap-4 md:grid-cols-3">
              <Field label="Canonical key">
                <span className="font-mono text-xs">{f.canonical_key}</span>
              </Field>
              <Field label="Namespace">{nsName(f.namespace_id)}</Field>
              <Field label="Subject type">{f.subject?.type || "—"}</Field>
              <Field label="Created by">{f.created_by}</Field>
              <Field label="Approved by">
                {f.approved_by?.length ? f.approved_by.join(", ") : "—"}
              </Field>
              <Field label="Version">{f.version}</Field>
              <Field label="Valid from">{fmtDate(f.valid_from)}</Field>
              <Field label="Valid to">{fmtDate(f.valid_to)}</Field>
              <Field label="Tags">
                <TagChips tags={f.tags} />
              </Field>
              <Field label="Supersedes">
                <ShortId id={f.supersedes_id} />
              </Field>
              <Field label="Superseded by">
                <ShortId id={f.superseded_by_id} />
              </Field>
              {f.provenance?.rationale ? (
                <Field label="Rationale">{f.provenance.rationale}</Field>
              ) : null}
            </div>

            <div className="card">
              <h2 className="mb-3 text-sm font-semibold">Audit trail</h2>
              <Async<AuditEvent[]>
                load={() => api.getFactHistory(id)}
                deps={[id, api]}
              >
                {(events) => <AuditTimeline events={events} />}
              </Async>
            </div>
          </>
        )}
      </Async>
    </div>
  );
}
