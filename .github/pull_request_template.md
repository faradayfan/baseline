<!--
PR title must follow Conventional Commits (e.g. feat(facts): …, fix(mcp): …) —
release notes are generated from it.
-->

## What & why

<!-- What does this change do, and why? Link any related issue. -->

## How it was tested

<!-- Commands run, new/updated tests, manual verification. -->

## Checklist

- [ ] `gofmt -l .` is clean and `golangci-lint run` passes
- [ ] `go test ./...` passes (incl. the conformance suite if relevant)
- [ ] Bug fixes include a test that failed before the fix
- [ ] PR title is a Conventional Commit
- [ ] Docs/spec updated if behavior or contracts changed
