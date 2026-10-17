# Vendored: Crush

This directory is a source copy of the packages of [Crush](https://github.com/charmbracelet/crush)
that AgentGo compiles in. It is **not** modified by AgentGo beyond the import-path rewrite below.

| | |
|---|---|
| Upstream | https://github.com/charmbracelet/crush |
| Copied from | the author's fork `wk222/crush` working tree, based on upstream commit `bdcf796c` (`chore: auto-update files`) |
| License | Functional Source License, Version 1.1, MIT Future License — see `LICENSE.md` next to this file. Copyright notices in the copied files are kept as they were. |
| Import path | `github.com/charmbracelet/crush/...` → `agentgo/third_party/crush/...` (only change to the upstream code) |

## What is here

73 packages, i.e. the closure of what AgentGo imports:

- `internal/**` — Crush's LSP client, code tools (edit, view, grep, diagnostics, …), config, permission,
  session/db, MCP, oauth and the terminal UI. The terminal UI is what `cmd/agentgo-tui` runs.
- `pkg/codetools`, `pkg/embedtui` — the two public entry points the author added in the fork (they were never
  pushed upstream, which is why AgentGo used to build only next to a local `../crush` checkout).

Tests and `testdata` were not copied. Non-Go files that Go embeds (prompt templates, builtin skills) were.

## Why vendored

AgentGo used `replace github.com/charmbracelet/crush => ../crush`. That module path lived only on one
machine, and `pkg/` was untracked in it, so nobody else (or CI) could build AgentGo. Copying the code in makes a
plain `git clone` build.

## Rules

- AgentGo's own code must not edit files here. Fix behavior in `internal/codetools` or `internal/bridge`
  (adapters), or refresh this copy.
- The 900/1800-line file limit applies to AgentGo's code, not to this directory.
- Keep the license file and the copyright headers. The license restricts *competing use*; check it before
  distributing AgentGo as a product.

## Refreshing

Run `scripts/vendor-crush.py <path-to-crush-checkout>` (copies the same package set and rewrites imports),
then `go mod tidy && go build ./...`.
