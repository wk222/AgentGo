package bridge

import (
	"log"
	"strings"

	"agentgo/internal/config"
	"agentgo/internal/docchunk"
	"agentgo/internal/memory"
	"agentgo/internal/plugin"
	"agentgo/internal/tools"
)

// chunkerPlugin mounts the adaptive document chunker ported from WeKnora
// (internal/docchunk) so that long memories are split on document structure
// instead of on blank lines.
//
// The plugin provides the "chunker" service, and installs itself into memory
// through memory.SetChunker. It does that instead of memory importing docchunk
// so that memory builds and is tested without the chunker (the architecture
// test TestConcretePluginsAreOnlyReachedThroughTheAssembly keeps it that way)
// and keeps its own splitter whenever this plugin is off.
//
// It is compiled into every build and off by default: switching splitters
// changes what recall returns, so it is measured first, on the company's own
// documents, against the built-in splitter (TestRetrievalChunkerAB in this
// package, or agentgo-eval retrieval). A build that must not carry it at all
// would need a build tag; none
// does today (the code costs ~100 KB, see docs/weknora-pageindex-digestion-plan.md).
//
//	AGENTGO_DOCCHUNK=1              turn it on
//	AGENTGO_DOCCHUNK_STRATEGY=auto  pick a tier (default auto)
func chunkerPlugin() (plugin.Plugin, bool) {
	if !config.DocChunk.On() {
		return plugin.Plugin{}, false
	}
	return plugin.Plugin{
		Name:     "chunker",
		Provides: []string{svcChunker},
		Inject:   []string{svcTools},
		Apply: func(c *plugin.Context) error {
			strategy := strings.TrimSpace(config.DocChunkStrategy.Get())
			svc := docchunk.NewService(strategy)
			reg, err := plugin.Use[*tools.Registry](c, svcTools)
			if err != nil {
				return err
			}
			c.Effect("document chunk preview hook", reg.SetDocumentChunker(func(text string, size int) tools.DocumentChunks {
				parentCfg := docchunk.SplitterConfig{ChunkSize: size * 4, ChunkOverlap: 80, Strategy: svc.Strategy()}
				childCfg := docchunk.SplitterConfig{ChunkSize: size, ChunkOverlap: min(80, size/4), Strategy: svc.Strategy()}
				split := docchunk.SplitParentChild(text, parentCfg, childCfg)
				var starts []int
				for _, ch := range split.Parents {
					starts = append(starts, ch.Start)
				}
				for _, ch := range split.Children {
					starts = append(starts, ch.Start)
				}
				headings := docchunk.HeadingContexts(text, starts)
				var out tools.DocumentChunks
				for i, ch := range split.Parents {
					if ch.ContextHeader == "" {
						ch.ContextHeader = headings[i]
					}
					out.Parents = append(out.Parents, tools.DocumentChunk{Start: ch.Start, End: ch.End, ContextHeader: ch.ContextHeader})
				}
				for i, ch := range split.Children {
					if ch.ContextHeader == "" {
						ch.ContextHeader = headings[len(split.Parents)+i]
					}
					child := tools.DocumentChunk{Start: ch.Start, End: ch.End, ContextHeader: ch.ContextHeader}
					if ch.ParentIndex >= 0 {
						index := ch.ParentIndex
						child.ParentIndex = &index
					}
					out.Children = append(out.Children, child)
				}
				return out
			}))
			restore := memory.SetChunker(svc.Chunk)
			c.Effect("memory chunk hook", restore)
			if err := c.Provide(svcChunker, svc); err != nil {
				return err
			}
			log.Printf("[chunker] adaptive splitting on (strategy=%s)", svc.Strategy())
			return nil
		},
	}, true
}
