package memory

import "sync"

// ChunkFunc splits content into chunks of at most maxChunkSize with the given
// overlap. It matches the shape of ChunkContent so a better splitter can be
// swapped in without the memory package importing it.
//
// The unit of maxChunkSize is the splitter's own: ChunkContent counts bytes,
// while ChunkContentRunes and the adaptive splitter count characters. Ingest
// asks for 1200 whichever it is, which for Chinese means about 400 characters
// in bytes and 1200 in characters, so swapping splitters also changes how big
// the chunks are.
type ChunkFunc func(content string, maxChunkSize, overlap int) []string

var (
	chunkHookMu sync.RWMutex
	chunkHook   ChunkFunc
)

// SetChunker installs a replacement for ChunkContent used when long memories are
// split at ingest time. Passing nil restores the built-in splitter.
//
// The hook exists so the adaptive document chunker (internal/docchunk) can be
// mounted by a plugin without this package importing it. That matters for the
// Win7 build, where the chunker is compiled out entirely and memory must still
// work: the default stays in this file and is always available.
//
// The returned func restores the previous value, so a plugin's Scope can undo it
// on stop or reload.
func SetChunker(fn ChunkFunc) (restore func()) {
	chunkHookMu.Lock()
	prev := chunkHook
	chunkHook = fn
	chunkHookMu.Unlock()
	return func() {
		chunkHookMu.Lock()
		chunkHook = prev
		chunkHookMu.Unlock()
	}
}

// chunkContent uses the installed splitter when there is one, and the built-in
// splitter otherwise.
func chunkContent(content string, maxChunkSize, overlap int) []string {
	chunkHookMu.RLock()
	fn := chunkHook
	chunkHookMu.RUnlock()
	if fn != nil {
		return fn(content, maxChunkSize, overlap)
	}
	return ChunkContent(content, maxChunkSize, overlap)
}

// SplitContent splits content exactly the way memory ingest will split it: with
// the installed chunker when a plugin mounted one, otherwise with the built-in
// paragraph splitter.
//
// It is exported so callers that show the user what will be indexed (a document
// preview, a chunk-count estimate) agree with what ingest actually stores,
// instead of reimplementing the sizing rules.
func SplitContent(content string, maxChunkSize, overlap int) []string {
	return chunkContent(content, maxChunkSize, overlap)
}
