import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import type { AuditEvent, FactStatus, PromotionState } from "./api";

// StatusBadge colors a fact status by its lifecycle semantics.
export function StatusBadge({ status }: { status: FactStatus | PromotionState | string }) {
  const cls: Record<string, string> = {
    active: "bg-green-100 text-green-800",
    approved: "bg-green-100 text-green-800",
    proposed: "bg-amber-100 text-amber-800",
    pending: "bg-amber-100 text-amber-800",
    in_review: "bg-amber-100 text-amber-800",
    changes_requested: "bg-orange-100 text-orange-800",
    revoked: "bg-red-100 text-red-800",
    rejected: "bg-red-100 text-red-800",
    superseded: "bg-gray-200 text-gray-700",
    expired: "bg-gray-200 text-gray-700",
  };
  return (
    <span className={`chip ${cls[status] ?? "bg-gray-100 text-gray-700"}`}>{status}</span>
  );
}

// TagChips renders tags; authoritative:true is highlighted (it bypasses filters
// and wins precedence).
export function TagChips({ tags }: { tags?: string[] }) {
  if (!tags || tags.length === 0) return <span className="text-muted">—</span>;
  return (
    <span className="flex flex-wrap gap-1">
      {tags.map((t) => (
        <span
          key={t}
          className={`chip ${
            t === "authoritative:true"
              ? "bg-brand-100 text-brand-700"
              : "bg-gray-100 text-gray-700"
          }`}
        >
          {t}
        </span>
      ))}
    </span>
  );
}

// MemoryType renders a memory's cognitive type from its `metadata.type`
// (semantic | procedural | episodic). Memories come from Mem0 and have no `tags`
// — the type lives in metadata — so this is the memory-side analog of TagChips.
const memTypeColor: Record<string, string> = {
  semantic: "bg-sky-100 text-sky-800",
  procedural: "bg-violet-100 text-violet-800",
  episodic: "bg-amber-100 text-amber-800",
};
export function MemoryType({ metadata }: { metadata?: Record<string, unknown> }) {
  const t = metadata && typeof metadata.type === "string" ? metadata.type : undefined;
  if (!t) return <span className="text-muted">—</span>;
  return <span className={`chip ${memTypeColor[t] ?? "bg-gray-100 text-gray-700"}`}>type:{t}</span>;
}

// AuditTimeline renders a fact's append-only history (oldest → newest).
export function AuditTimeline({ events }: { events: AuditEvent[] }) {
  if (events.length === 0) return <p className="text-muted">No audit events.</p>;
  return (
    <ol className="space-y-3">
      {events.map((e, i) => (
        <li key={i} className="flex gap-3">
          <div className="mt-1 h-2 w-2 flex-none rounded-full bg-brand-500" />
          <div className="text-sm">
            <div className="font-medium">
              {e.Action}
              {e.FromState || e.ToState ? (
                <span className="text-muted">
                  {" "}
                  {e.FromState || "∅"} → {e.ToState || "∅"}
                </span>
              ) : null}
            </div>
            <div className="text-muted">by {e.Principal || "—"}</div>
            {e.Detail && Object.keys(e.Detail as object).length > 0 ? (
              <pre className="mt-1 overflow-x-auto rounded bg-gray-50 p-2 text-xs text-gray-700">
                {JSON.stringify(e.Detail, null, 2)}
              </pre>
            ) : null}
          </div>
        </li>
      ))}
    </ol>
  );
}

// Async wraps a data-loading view: shows loading / error / the children.
export function Async<T>({
  load,
  deps,
  children,
}: {
  load: () => Promise<T>;
  deps: unknown[];
  children: (data: T) => ReactNode;
}) {
  const [state, setState] = useState<
    { kind: "loading" } | { kind: "error"; msg: string } | { kind: "ok"; data: T }
  >({ kind: "loading" });

  useEffect(() => {
    let live = true;
    setState({ kind: "loading" });
    load()
      .then((data) => live && setState({ kind: "ok", data }))
      .catch((e) => live && setState({ kind: "error", msg: String(e.message ?? e) }));
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);

  if (state.kind === "loading") return <p className="text-muted">Loading…</p>;
  if (state.kind === "error")
    return (
      <div className="card border-red-200 bg-red-50 text-sm text-red-800">{state.msg}</div>
    );
  return <>{children(state.data)}</>;
}

export function ShortId({ id }: { id?: string }) {
  if (!id) return <span className="text-muted">—</span>;
  return (
    <span className="font-mono text-xs" title={id}>
      {id.slice(0, 8)}
    </span>
  );
}

export function fmtDate(s?: string): string {
  if (!s) return "—";
  const d = new Date(s);
  if (isNaN(d.getTime())) return s;
  return d.toLocaleString();
}
