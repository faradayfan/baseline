import { useState } from "react";
import { Link } from "react-router-dom";
import { useApi, useNamespaceMap } from "../api";
import type { Fact } from "../api";
import { Async, StatusBadge, TagChips, fmtDate } from "../components";

const STATUSES = ["", "active", "proposed", "revoked", "superseded", "expired"];

export default function FactsView() {
  const api = useApi();
  const nsName = useNamespaceMap();
  const [status, setStatus] = useState("active");
  const [tags, setTags] = useState("");
  const [q, setQ] = useState("");

  return (
    <div className="space-y-4">
      <div className="flex items-end justify-between gap-4">
        <h1 className="text-xl font-semibold">Facts</h1>
        <div className="flex flex-wrap items-end gap-3">
          <div>
            <div className="label">Status</div>
            <select className="input" value={status} onChange={(e) => setStatus(e.target.value)}>
              {STATUSES.map((s) => (
                <option key={s} value={s}>
                  {s || "all"}
                </option>
              ))}
            </select>
          </div>
          <div>
            <div className="label">Tags (any-match, comma-sep)</div>
            <input
              className="input w-48"
              value={tags}
              placeholder="e.g. tooling,security"
              onChange={(e) => setTags(e.target.value)}
            />
          </div>
          <div>
            <div className="label">Search (q)</div>
            <input
              className="input w-40"
              value={q}
              onChange={(e) => setQ(e.target.value)}
            />
          </div>
        </div>
      </div>

      <Async<Fact[]>
        load={() => api.listFacts({ status, tags, q })}
        deps={[status, tags, q, api]}
      >
        {(facts) =>
          facts.length === 0 ? (
            <div className="card text-muted">No facts match.</div>
          ) : (
            <div className="card overflow-x-auto p-0">
              <table className="min-w-full divide-y divide-gray-100">
                <thead className="bg-gray-50">
                  <tr>
                    <th className="th">Statement</th>
                    <th className="th">Canonical key</th>
                    <th className="th">Status</th>
                    <th className="th">Namespace</th>
                    <th className="th">Tags</th>
                    <th className="th">Valid to</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100">
                  {facts.map((f) => (
                    <tr key={f.id} className="hover:bg-gray-50">
                      <td className="td max-w-md">
                        <Link to={`/facts/${f.id}`} className="text-brand-600 hover:underline">
                          {f.statement}
                        </Link>
                      </td>
                      <td className="td font-mono text-xs">{f.canonical_key}</td>
                      <td className="td">
                        <StatusBadge status={f.status} />
                      </td>
                      <td className="td">{nsName(f.namespace_id)}</td>
                      <td className="td">
                        <TagChips tags={f.tags} />
                      </td>
                      <td className="td whitespace-nowrap text-muted">{fmtDate(f.valid_to)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )
        }
      </Async>
    </div>
  );
}
