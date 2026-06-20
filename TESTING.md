# Testing

Every feature ships with **unit tests and integration tests**. Integration
coverage includes **API tests** for anything exposed over HTTP. A milestone is
not done until its tests are green, including the relevant SPEC §14 conformance
invariants.

## Layers

| Layer | What it covers | Depends on | Lives in |
| ----- | -------------- | ---------- | -------- |
| **Unit** | Pure logic, no I/O. Table-driven. | nothing | `*_test.go` beside the code |
| **Integration — repo** | Real SQL against Postgres+pgvector. | Docker | `*_test.go` using `storetest` |
| **Integration — API** | Real chi router + handlers + DB, driven over HTTP. | Docker | `*_test.go` using `storetest.NewAPI` |
| **Conformance** | The §14 invariants against a live server. | Docker | `test/conformance/` |

Examples by layer:
- **Unit** — `normalize()`/canonical-key derivation, policy defaults, the fact
  state-machine transition table, RBAC matrix evaluation, `simple/v1` rule matching.
- **Repo** — namespace CRUD, the `facts_active_unique` invariant, supersession lineage.
- **API** — auth/RBAC enforced at the edge, `If-Match`/409 optimistic concurrency,
  `Idempotency-Key` dedupe, `/context` never leaking outside entitlements, the
  separation-of-duties gate exercised through `POST /promotions/{id}:approve`.

## The harness (`internal/storetest`)

One pgvector container per test **package**, booted in `TestMain` and torn down
after. Add this to any package with integration tests:

```go
func TestMain(m *testing.M) { storetest.Main(m) }
```

Then, inside tests, get an isolated DB handle:

- `h := storetest.Shared(t)` — the package's harness.
- `h.Tx(t)` — a transaction rolled back at test end. **Default**: fast, fully
  isolated. Cannot observe COMMITs or cross-transaction behavior.
- `h.FreshDB(t)` — a brand-new database cloned from the migrated template,
  dropped at test end. Use for **committed-state and concurrency** tests that
  tx-rollback can't exercise (the unique-index invariant §14.2, optimistic
  concurrency 409 §14.8).
- `storetest.NewAPI(t, h, buildHandler)` — a fresh DB + the real handler behind
  an `httptest.Server`; drive it with `api.Do(t, method, path, body, headers)`
  and read responses with `storetest.DecodeJSON`.

Integration tests **self-skip on `testing.Short()`**, and `storetest.Main`
boots no container under `-short` — so unit tests run fast and Docker-free.

## Running

```bash
go test -short ./...     # unit only — no Docker, fast (use in tight loops)
go test ./...            # everything — needs Docker for pgvector
go test -run TestName ./internal/<pkg>   # a single test
```

CI (deferred) will run the full suite; locally prefer `-short` while iterating
and the full run before declaring a milestone done.
