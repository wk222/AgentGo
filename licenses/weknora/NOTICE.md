# WeKnora attribution

Parts of AgentGo's document chunking were ported from
[WeKnora](https://github.com/Tencent/WeKnora) (Tencent), which is licensed under
the MIT license except for the third-party components it lists separately.

Copyright (C) 2026 Tencent. All rights reserved.

The ported code is a **derivative work**: the algorithms, strategy tiers, regex
patterns and test fixtures are WeKnora's; the package was renamed, its two
non-standard-library dependencies were removed, and the public surface was
narrowed. Each ported file carries a header saying so.

## What was ported

| AgentGo file | WeKnora origin | Changes |
|---|---|---|
| `internal/docchunk/splitter.go` | `internal/infrastructure/chunker/splitter.go` | `docparser.UnwrapLinkedImages` call now resolves inside the package |
| `internal/docchunk/strategy.go` | `internal/infrastructure/chunker/strategy.go` | `logger.Debugf` replaced by a package-local `debugf` gated by `SetDebug` |
| `internal/docchunk/patterns.go` | `internal/infrastructure/chunker/patterns.go` | unchanged apart from the package name |
| `internal/docchunk/tokens.go` | `internal/infrastructure/chunker/tokens.go` | unchanged apart from the package name |
| `internal/docchunk/heading_hierarchy.go` | `internal/infrastructure/chunker/heading_hierarchy.go` | unchanged apart from the package name |
| `internal/docchunk/heading_splitter.go` | `internal/infrastructure/chunker/heading_splitter.go` | unchanged apart from the package name |
| `internal/docchunk/header_tracker.go` | `internal/infrastructure/chunker/header_tracker.go` | unchanged apart from the package name |
| `internal/docchunk/fenced_code.go` | `internal/infrastructure/chunker/fenced_code.go` | unchanged apart from the package name |
| `internal/docchunk/validator.go` | `internal/infrastructure/chunker/validator.go` | unchanged apart from the package name |
| `internal/docchunk/profiler.go` | `internal/infrastructure/chunker/profiler.go` | unchanged apart from the package name |
| `internal/docchunk/heuristic_splitter.go` | `internal/infrastructure/chunker/heuristic_splitter.go` | unchanged apart from the package name |
| `internal/docchunk/markdown.go` | `internal/infrastructure/docparser/image_resolver.go` and `markdown_image_scanner.go` | only the Markdown code-literal range helpers and `UnwrapLinkedImages`, extracted so the chunker needs nothing outside the standard library |
| `internal/docchunk/*_test.go` | `internal/infrastructure/chunker/*_test.go` | package renamed; `parent_child_offsets_test.go` lost the `sourceloc`-dependent case (page/sheet citation), which needs packages AgentGo does not use |
| `internal/docchunk/service.go` | none | new: the adapter AgentGo's memory hook calls |
| `internal/visionocr/sanitize.go` | `internal/application/service/ocr_sanitizer.go` | HTML-to-Markdown library replaced by standard-library tag stripping and a small table-to-GFM converter; three logic fixes (table rows are excluded from repetition detection, tables are converted before HTML unwrapping, and the "too short after stripping" check no longer discards short tables) |

## What was deliberately not ported

- `github.com/yanyiwu/gojieba` and the `Jieba` global in WeKnora's
  `internal/types/evaluation.go`. It requires cgo, which would break AgentGo's
  `CGO_ENABLED=0` Win7 build. AgentGo's existing `tokenize`/`cjkPieces`
  (`internal/memory/scorer.go`) covers the same need without it.
- `searchutil/textutil.go` and `searchutil/chunkmerge.go` import WeKnora's
  `types` package for `types.Jieba` and `types.Chunk`. Only the parts that do not
  are relevant; the rest would drag the whole types package in.
- The Python `docreader/` service, `internal/application/service/*`, the router,
  the frontend and the vector-store adapters. None of it was copied.

## PageIndex

AgentGo's document-tree retrieval design follows the *idea* of
[PageIndex](https://github.com/VectifyAI/PageIndex) (tree index plus
reasoning-based retrieval, vectorless). No PageIndex source code was copied; it
is Python and uses a different runtime.

## MIT license text

```
Permission is hereby granted, free of charge, to any person obtaining
a copy of this software and associated documentation files (the
"Software"), to deal in the Software without restriction, including
without limitation the rights to use, copy, modify, merge, publish,
distribute, sublicense, and/or sell copies of the Software, and to
permit persons to whom the Software is furnished to do so, subject to
the following conditions:

The above copyright notice and this permission notice shall be
included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND
NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE
LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION
OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION
WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
```
