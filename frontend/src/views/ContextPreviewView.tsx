import { useState } from "react";
import { useApi } from "../api";
import type { ContextItem } from "../api";
import { usePrincipal } from "../principal";
import { Async, TagChips, MemoryType } from "../components";

// ContextPreviewView renders exactly what an agent receives from GET /context as
// the selected principal — the dogfood view. Facts rank above memories;
// changing the "view as" principal (in the header) changes what comes back,
// making entitlement scoping visible.
export default function ContextPreviewView() {
  const api = useApi();
  const { principal } = usePrincipal();
  const [includeMemories, setIncludeMemories] = useState(true);
  const [tags, setTags] = useState("");

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-xl font-semibold">Context preview</h1>
        <p className="text-sm text-muted">
          What <span className="font-medium">{principal}</span> would receive from{" "}
          <code className="font-mono text-xs">GET /context</code> — the agent's-eye view.
        </p>
      </div>

      <div className="card flex flex-wrap items-end gap-4">
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={includeMemories}
            onChange={(e) => setIncludeMemories(e.target.checked)}
          />
          Include personal memories
        </label>
        <div>
          <div className="label">Tags (any-match, comma-sep)</div>
          <input
            className="input w-56"
            value={tags}
            placeholder="e.g. tooling,security"
            onChange={(e) => setTags(e.target.value)}
          />
        </div>
      </div>

      <Async<ContextItem[]>
        load={() => api.getContext({ include_memories: includeMemories, tags })}
        deps={[includeMemories, tags, principal, api]}
      >
        {(items) =>
          items.length === 0 ? (
            <div className="card text-muted">Empty context for this principal/filter.</div>
          ) : (
            <ol className="space-y-2">
              {items.map((it, i) => (
                <li key={i} className="card flex items-start gap-3 py-3">
                  <span
                    className={`chip ${
                      it.source === "fact"
                        ? "bg-brand-100 text-brand-700"
                        : "bg-gray-100 text-gray-600"
                    }`}
                  >
                    {it.source}
                  </span>
                  <div className="flex-1">
                    <div className="text-sm">{it.statement}</div>
                    <div className="mt-1 flex items-center gap-3">
                      {it.canonical_key ? (
                        <span className="font-mono text-xs text-muted">{it.canonical_key}</span>
                      ) : null}
                      {/* Facts carry tags; memories carry a cognitive type in
                          metadata.type (Mem0 has no tags). Show whichever applies. */}
                      {it.source === "memory" ? (
                        <MemoryType metadata={it.metadata} />
                      ) : (
                        <TagChips tags={it.tags} />
                      )}
                    </div>
                  </div>
                </li>
              ))}
            </ol>
          )
        }
      </Async>
    </div>
  );
}
