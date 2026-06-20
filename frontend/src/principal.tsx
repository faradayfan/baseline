import { createContext, useCallback, useContext, useMemo, useState } from "react";
import type { ReactNode } from "react";

// Baseline's identity is the dev-only X-Baseline-Principal header (OIDC is
// deferred). For this read-only dashboard the "principal" is simply who we
// claim to be on each request — a "view as" control. It is spoofable, but the
// same is true everywhere else in the local/LAN POC, and read-only means it can
// mutate nothing. This is NOT real auth.

const STORAGE_KEY = "baseline.principal";

interface PrincipalCtx {
  principal: string;
  setPrincipal: (p: string) => void;
}

const Ctx = createContext<PrincipalCtx | null>(null);

export function PrincipalProvider({ children }: { children: ReactNode }) {
  const [principal, setPrincipalState] = useState<string>(
    () => localStorage.getItem(STORAGE_KEY) || "john",
  );

  const setPrincipal = useCallback((p: string) => {
    const v = p.trim() || "john";
    localStorage.setItem(STORAGE_KEY, v);
    setPrincipalState(v);
  }, []);

  const value = useMemo(() => ({ principal, setPrincipal }), [principal, setPrincipal]);
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function usePrincipal(): PrincipalCtx {
  const v = useContext(Ctx);
  if (!v) throw new Error("usePrincipal must be used within PrincipalProvider");
  return v;
}
