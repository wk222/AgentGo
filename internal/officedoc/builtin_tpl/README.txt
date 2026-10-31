Built-in PowerPoint templates (create_pptx / inspect_pptx: "builtin:<name>")
==========================================================================

Every *.pptx / *.potx in this folder is compiled into the AgentGo executable
(go:embed). The file name without its extension is the template name:
company.pptx is used as  template: "builtin:company".

The template files are NOT committed (see .gitignore): they carry a company's
logo and background art. This README is the only tracked file, so a fresh clone
still builds, just without built-in templates (builtin:<name> then answers
"none in this build" and the model asks for a template file instead).

Making one from a company deck
------------------------------

    $env:BUILTIN_TPL_SRC  = 'D:\path\to\company-deck.pptx'
    $env:BUILTIN_TPL_NAME = 'company'          # optional, default "company"
    go test ./internal/officedoc -run TestMakeBuiltinTemplate -count=1 -v

This keeps the masters, layouts, themes and the logo/art they use, byte for
byte. It removes the deck's content: every slide is replaced by an empty one on
the same layout (so the default cover and content layouts still follow the
original), and notes, charts, photos, comment authors, revision history and
document properties are dropped.

Rebuild AgentGo afterwards (scripts/release-win7.ps1 does). The release
machine needs the file in this folder; it is not in the repository.
