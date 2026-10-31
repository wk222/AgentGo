# Local Powernap LSP client

Derived from github.com/charmbracelet/x/powernap v0.1.6, pkg/lsp, under its MIT license (included).

The upstream client has no process configuration hook. This small source copy adds processutil.HideWindow at subprocess creation so Windows GUI builds do not open a console for language servers. Protocol, config and transport packages continue to use the pinned upstream dependency. Keep these files aligned when upgrading Powernap; no Eino source is changed.
