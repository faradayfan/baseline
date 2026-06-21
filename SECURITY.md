# Security Policy

## Reporting a vulnerability

Please report security vulnerabilities **privately** — do not open a public
issue.

- Preferred: use GitHub's [private vulnerability
  reporting](https://github.com/faradayfan/baseline/security/advisories/new)
  (Security → Advisories → Report a vulnerability).
- Or email **faradayfan@protonmail.com**.

Include enough detail to reproduce: affected version/commit, impact, and a
proof-of-concept if you have one. We aim to acknowledge reports within a few days.

## Scope

Baseline is a governance layer over an agent memory store. Of particular interest:

- **Entitlement / namespace scoping** — any path where `/context`, `/facts`, or
  the MCP tools return facts outside the caller's entitled namespaces.
- **Separation of duties** — any way a proposer can approve their own promotion,
  or approval gates can be bypassed.
- **Audit integrity** — any way to mutate or suppress the append-only audit log.
- **Authentication** — note that `HeaderAuthenticator` is a documented
  **dev-only** mechanism; production deployments are expected to wire OIDC/mTLS.
  Reports about the dev header itself are known/by-design.

## Supported versions

This project is pre-1.0 and under active development; security fixes target the
latest release and `main`.
