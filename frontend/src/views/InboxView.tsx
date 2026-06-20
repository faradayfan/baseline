import { useState } from "react";
import { Link } from "react-router-dom";
import { useApi, useNamespaceMap } from "../api";
import type { Promotion } from "../api";
import { Async, StatusBadge, fmtDate } from "../components";

const STATES = ["", "pending", "in_review", "changes_requested", "approved", "rejected"];

export default function InboxView() {
  const api = useApi();
  const nsName = useNamespaceMap();
  const [state, setState] = useState("");

  return (
    <div className="space-y-4">
      <div className="flex items-end justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold">Promotion inbox</h1>
          <p className="text-sm text-muted">
            The governance queue. Read-only — review actions happen via the MCP tools / API.
          </p>
        </div>
        <div>
          <div className="label">State</div>
          <select className="input" value={state} onChange={(e) => setState(e.target.value)}>
            {STATES.map((s) => (
              <option key={s} value={s}>
                {s || "all"}
              </option>
            ))}
          </select>
        </div>
      </div>

      <Async<Promotion[]> load={() => api.listPromotions({ state })} deps={[state, api]}>
        {(promos) =>
          promos.length === 0 ? (
            <div className="card text-muted">No promotions.</div>
          ) : (
            <div className="card overflow-x-auto p-0">
              <table className="min-w-full divide-y divide-gray-100">
                <thead className="bg-gray-50">
                  <tr>
                    <th className="th">Proposed statement</th>
                    <th className="th">State</th>
                    <th className="th">Proposer</th>
                    <th className="th">Target namespace</th>
                    <th className="th">Approvals</th>
                    <th className="th">Created</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100">
                  {promos.map((p) => {
                    const approvals = p.reviews.filter((r) => r.decision === "approve").length;
                    return (
                      <tr key={p.id} className="hover:bg-gray-50">
                        <td className="td max-w-md">
                          <Link to={`/inbox/${p.id}`} className="text-brand-600 hover:underline">
                            {p.proposed_statement}
                          </Link>
                        </td>
                        <td className="td">
                          <StatusBadge status={p.state} />
                        </td>
                        <td className="td">{p.proposer}</td>
                        <td className="td">{nsName(p.target_namespace_id)}</td>
                        <td className="td whitespace-nowrap">
                          {approvals}/{p.required_approvals}
                          {p.conflict_with ? (
                            <span className="ml-2 chip bg-orange-100 text-orange-800">
                              conflict
                            </span>
                          ) : null}
                        </td>
                        <td className="td whitespace-nowrap text-muted">{fmtDate(p.created_at)}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )
        }
      </Async>
    </div>
  );
}
