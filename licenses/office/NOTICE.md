# Office tools and built-in office skills

The Office tools (`internal/officedoc`) and the built-in skills (`internal/skills/builtin/office*`)
are original code and text written for AgentGo. **No source code or prose from the projects below
was copied.** They were reviewed on 2026-10-24/26 and shaped some design decisions, so they are
acknowledged here. Licenses are as recorded in that review; check the upstream repositories for
their current terms.

| Project | License | What it influenced |
|---|---|---|
| [qax-os/excelize](https://github.com/qax-os/excelize) | BSD-3-Clause | Used as a Go dependency (v2.11.0) for `.xlsx` reading, editing and writing. Its license applies to the library as linked into the binary. |
| [siril9/presentation-skill](https://github.com/siril9/presentation-skill) | MIT | The idea of planning a deck as an outline first, titles that state a conclusion, and a layout check after generation. Re-implemented as `create_pptx` / `inspect_pptx` in Go. |
| [deepseek-ai/deepseek-harness `skill-office`](https://github.com/deepseek-ai/deepseek-harness/tree/master/packages/skill/skill-office) | MIT | The split into a router skill plus one skill per file type, and generate-then-verify as a workflow. Rewritten in Chinese around AgentGo's own Go tools. |
| [openai/skills](https://github.com/openai/skills) | see upstream | Only the `SKILL.md` convention (a heading and a short trigger description). |
| [cxuu/golang-skills](https://github.com/cxuu/golang-skills) | CC BY (see upstream) | Not used. If a Go coding skill pack is added later, keep its attribution notice next to it. |

The PPTX generator writes the Office Open XML parts itself with the Go standard library; the
embedded chart workbooks are written with excelize.
