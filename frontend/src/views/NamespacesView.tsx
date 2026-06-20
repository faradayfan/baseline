import { useApi } from "../api";
import type { Member, Namespace } from "../api";
import { Async } from "../components";

function Members({ id }: { id: string }) {
  const api = useApi();
  return (
    <Async<Member[]> load={() => api.listMembers(id)} deps={[id, api]}>
      {(members) =>
        members.length === 0 ? (
          <span className="text-muted">no members</span>
        ) : (
          <span className="flex flex-wrap gap-1">
            {members.map((m) => (
              <span key={m.principal} className="chip bg-gray-100 text-gray-700">
                {m.principal}
                <span className="ml-1 text-muted">{m.role}</span>
              </span>
            ))}
          </span>
        )
      }
    </Async>
  );
}

const kindOrder = { org: 0, project: 1, team: 2, user: 3 } as const;

export default function NamespacesView() {
  const api = useApi();
  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold">Namespaces</h1>
      <Async<Namespace[]> load={() => api.listNamespaces()} deps={[api]}>
        {(namespaces) => {
          const sorted = [...namespaces].sort(
            (a, b) =>
              (kindOrder[a.kind] ?? 9) - (kindOrder[b.kind] ?? 9) ||
              a.name.localeCompare(b.name),
          );
          return (
            <div className="card overflow-x-auto p-0">
              <table className="min-w-full divide-y divide-gray-100">
                <thead className="bg-gray-50">
                  <tr>
                    <th className="th">Name</th>
                    <th className="th">Kind</th>
                    <th className="th">Required approvals</th>
                    <th className="th">Auto-promote</th>
                    <th className="th">Members</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100">
                  {sorted.map((n) => (
                    <tr key={n.id} className="align-top hover:bg-gray-50">
                      <td className="td font-medium">{n.name}</td>
                      <td className="td">
                        <span className="chip bg-gray-100 text-gray-700">{n.kind}</span>
                      </td>
                      <td className="td">{n.policy?.required_approvals ?? "—"}</td>
                      <td className="td">
                        {n.policy?.auto_promote?.engine ? (
                          <span className="font-mono text-xs">
                            {n.policy.auto_promote.engine}
                          </span>
                        ) : (
                          <span className="text-muted">human review</span>
                        )}
                      </td>
                      <td className="td">
                        <Members id={n.id} />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          );
        }}
      </Async>
    </div>
  );
}
