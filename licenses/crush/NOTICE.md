# Crush attribution

AgentGo's code tools were digested from [Crush](https://github.com/charmbracelet/crush)
(upstream commit `bdcf796c`, via the author's fork `wk222/crush`), which is licensed under the
Functional Source License, Version 1.1, MIT Future License (`LICENSE.md` next to this file).
Copyright notices of the original authors are kept in that license text.

The vendored copy (`third_party/crush`) was removed on 2026-10-17 after the native implementations
were measured to behave the same (`docs/plan-crush-digestion.md`, D7). It remains in git history
(commit `d647700` and earlier). What is left in the tree are **derived** parts, each marked in its
file header:

| AgentGo file | Derived from |
|---|---|
| `internal/codelsp/*` | Crush `internal/lsp` and the LSP agent tools (built on `charmbracelet/x/powernap`) |
| `internal/codetools/search.go`, `search_ignore.go`, `search_ls.go` | Crush grep/glob/ls tools and `fsext` (`search_walk.go` is independent code) |
| `internal/coderuntime/fs_*.go` | Crush view/edit/multiedit/write tools (parameters and model-visible messages) |
| `internal/codetools/testdata/crush_golden.json` | Recorded outputs of the original Crush tools, used as a regression oracle |

`internal/coderuntime/edit_match.go` and `edit_hint.go` (edit matching and error hints) were rewritten
on 2026-10-26 with a different structure and wording, and are no longer derived from Crush. They
were measured with the rename-proof token n-gram comparison against the Crush corpus at 8-9%,
the same range as independently written files in this repository.

The license restricts *competing use*; check it before distributing AgentGo as a product.
