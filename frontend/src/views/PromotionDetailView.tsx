import { Link, useParams } from "react-router-dom";
import { useApi, useNamespaceMap } from "../api";
import type { Promotion } from "../api";
import { Async, ShortId, StatusBadge, fmtDate } from "../components";

const decisionChip: Record<string, string> = {
  approve: "bg-green-100 text-green-800",
  reject: "bg-red-100 text-red-800",
  request_changes: "bg-orange-100 text-orange-800",
};

export default function PromotionDetailView() {
  const { id = "" } = useParams();
  const api = useApi();
  const nsName = useNamespaceMap();

  return (
    <div className="space-y-4">
      <Link to="/inbox" className="text-sm text-brand-600 hover:underline">
        ← Promotion inbox
      </Link>

      <Async<Promotion> load={() => api.getPromotion(id)} deps={[id, api]}>
        {(p) => {
          const approvals = p.reviews.filter((r) => r.decision === "approve").length;
          return (
            <>
              <div className="flex items-start justify-between gap-4">
                <h1 className="text-xl font-semibold">{p.proposed_statement}</h1>
                <StatusBadge status={p.state} />
              </div>

              <div className="card grid grid-cols-2 gap-4 md:grid-cols-3">
                <div>
                  <div className="label">Proposer</div>
                  <div className="text-sm">{p.proposer}</div>
                </div>
                <div>
                  <div className="label">Target namespace</div>
                  <div className="text-sm">{nsName(p.target_namespace_id)}</div>
                </div>
                <div>
                  <div className="label">Approvals</div>
                  <div className="text-sm">
                    {approvals}/{p.required_approvals}
                  </div>
                </div>
                <div>
                  <div className="label">Fact</div>
                  <div className="text-sm">
                    <ShortId id={p.fact_id} />
                  </div>
                </div>
                <div>
                  <div className="label">Conflict with</div>
                  <div className="text-sm">
                    <ShortId id={p.conflict_with} />
                  </div>
                </div>
                <div>
                  <div className="label">Created</div>
                  <div className="text-sm">{fmtDate(p.created_at)}</div>
                </div>
              </div>

              <div className="card">
                <h2 className="mb-3 text-sm font-semibold">Reviews</h2>
                {p.reviews.length === 0 ? (
                  <p className="text-muted">No reviews yet.</p>
                ) : (
                  <ul className="space-y-2">
                    {p.reviews.map((r, i) => (
                      <li key={i} className="flex items-start gap-2 text-sm">
                        <span className={`chip ${decisionChip[r.decision] ?? "bg-gray-100"}`}>
                          {r.decision}
                        </span>
                        <div>
                          <span className="font-medium">{r.reviewer}</span>{" "}
                          <span className="text-muted">{fmtDate(r.at)}</span>
                          {r.comment ? <div className="text-gray-700">{r.comment}</div> : null}
                        </div>
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            </>
          );
        }}
      </Async>
    </div>
  );
}
