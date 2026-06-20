import { useEffect, useState } from "react";
import { NavLink, Outlet } from "react-router-dom";
import { usePrincipal } from "./principal";

const NAV = [
  { to: "/facts", label: "Facts" },
  { to: "/inbox", label: "Promotion inbox" },
  { to: "/namespaces", label: "Namespaces" },
  { to: "/context", label: "Context preview" },
];

// ReadyIndicator polls /readyz (served at the root, outside /v1) so the header
// shows whether the backend is reachable.
function ReadyIndicator() {
  const [ready, setReady] = useState<boolean | null>(null);
  useEffect(() => {
    let live = true;
    const check = () =>
      fetch("/readyz")
        .then((r) => live && setReady(r.ok))
        .catch(() => live && setReady(false));
    check();
    const t = setInterval(check, 15000);
    return () => {
      live = false;
      clearInterval(t);
    };
  }, []);
  const color = ready == null ? "bg-gray-300" : ready ? "bg-green-500" : "bg-red-500";
  return (
    <span className="flex items-center gap-1.5 text-xs text-muted" title="backend /readyz">
      <span className={`h-2 w-2 rounded-full ${color}`} />
      {ready == null ? "checking" : ready ? "ready" : "unreachable"}
    </span>
  );
}

// PrincipalControl is the "view as" box. This is the dashboard's only identity
// mechanism (X-Baseline-Principal). It is read-only and spoofable by design —
// the same trust model as the rest of the local POC.
function PrincipalControl() {
  const { principal, setPrincipal } = usePrincipal();
  const [draft, setDraft] = useState(principal);
  useEffect(() => setDraft(principal), [principal]);
  return (
    <form
      className="flex items-center gap-2"
      onSubmit={(e) => {
        e.preventDefault();
        setPrincipal(draft);
      }}
    >
      <label className="label" htmlFor="principal">
        View as
      </label>
      <input
        id="principal"
        className="input w-36"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        spellCheck={false}
      />
      <button className="btn-ghost" type="submit" disabled={draft.trim() === principal}>
        Apply
      </button>
    </form>
  );
}

export default function AppShell() {
  return (
    <div className="flex min-h-full flex-col">
      <header className="flex items-center justify-between border-b border-gray-200 bg-white px-6 py-3">
        <div className="flex items-center gap-3">
          <span className="text-lg font-semibold tracking-tight">Baseline</span>
          <span className="chip bg-gray-100 text-gray-600">read-only</span>
          <ReadyIndicator />
        </div>
        <PrincipalControl />
      </header>
      <div className="flex flex-1">
        <nav className="w-52 flex-none border-r border-gray-200 bg-white p-3">
          <ul className="space-y-1">
            {NAV.map((n) => (
              <li key={n.to}>
                <NavLink
                  to={n.to}
                  className={({ isActive }) =>
                    `block rounded-lg px-3 py-2 text-sm font-medium ${
                      isActive
                        ? "bg-brand-100 text-brand-700"
                        : "text-gray-700 hover:bg-gray-50"
                    }`
                  }
                >
                  {n.label}
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>
        <main className="flex-1 overflow-x-auto p-6">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
