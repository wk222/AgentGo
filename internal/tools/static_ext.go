package tools

import "sync"

// Plugins can contribute tools that are always visible to the model (static),
// instead of only being discoverable through tool_search. Each declares whether
// it mutates files so read-only session modes can hide it.
var (
	extStaticMu  sync.RWMutex
	extStatic    = map[string]bool{} // tool name -> mutating
	extStaticGen = map[string]int64{}
	extStaticSeq int64
)

// RegisterStaticTool marks name as always-visible. mutating tools are withheld
// from read-only (focused) modes. The returned func withdraws the mark.
func RegisterStaticTool(name string, mutating bool) (dispose func()) {
	extStaticMu.Lock()
	defer extStaticMu.Unlock()
	extStaticSeq++
	seq := extStaticSeq
	extStatic[name] = mutating
	extStaticGen[name] = seq
	return func() {
		extStaticMu.Lock()
		defer extStaticMu.Unlock()
		if extStaticGen[name] == seq {
			delete(extStatic, name)
			delete(extStaticGen, name)
		}
	}
}

// PluginStaticTools returns a copy of name -> mutating for plugin-declared static tools.
func PluginStaticTools() map[string]bool {
	extStaticMu.RLock()
	defer extStaticMu.RUnlock()
	out := make(map[string]bool, len(extStatic))
	for k, v := range extStatic {
		out[k] = v
	}
	return out
}

func isPluginStatic(name string) bool {
	extStaticMu.RLock()
	defer extStaticMu.RUnlock()
	_, ok := extStatic[name]
	return ok
}
