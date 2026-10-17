package ideruntime

import "sync"

var (
	rgOnce sync.Once
	rgBin  string
)

// RgBinary returns the ripgrep executable the IDE runtime uses (the embedded
// copy on Windows, else the one on PATH), or "" when there is none. It is
// resolved once and shared, so other tools (the code tools' grep and glob) get
// the same fast path as the IDE search without owning a SearchService.
func RgBinary() string {
	rgOnce.Do(func() {
		rgBin, _ = (&SearchService{}).resolveRgBinary()
	})
	return rgBin
}
