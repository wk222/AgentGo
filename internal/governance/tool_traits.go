package governance

import "sync"

// ToolTraits is what a tool declares about itself, so the policy does not have
// to know tool names. Plugins register traits for the tools they contribute
// and withdraw them when unloaded; the policy consults them at decision time,
// so a tool added later is covered without editing any name list here.
type ToolTraits struct {
	Source   string // contributing plugin or subsystem, for audit
	Mutating bool   // changes files in the workspace
}

var (
	traitsMu  sync.RWMutex
	traits    = map[string]ToolTraits{}
	traitsGen = map[string]int64{}
	traitsSeq int64
)

// RegisterToolTraits declares traits for name. The returned func withdraws
// them, unless a later registration of the same name has replaced them.
func RegisterToolTraits(name string, t ToolTraits) (dispose func()) {
	traitsMu.Lock()
	defer traitsMu.Unlock()
	traitsSeq++
	seq := traitsSeq
	traits[name] = t
	traitsGen[name] = seq
	return func() {
		traitsMu.Lock()
		defer traitsMu.Unlock()
		if traitsGen[name] == seq {
			delete(traits, name)
			delete(traitsGen, name)
		}
	}
}

// ToolTraitsOf returns the declared traits of a tool, if any.
func ToolTraitsOf(name string) (ToolTraits, bool) {
	traitsMu.RLock()
	defer traitsMu.RUnlock()
	t, ok := traits[name]
	return t, ok
}

// declaredMutating reports a tool that declared itself file-changing.
func declaredMutating(name string) bool {
	t, ok := ToolTraitsOf(name)
	return ok && t.Mutating
}

// mutationNeedsApproval: every mode except open holds declared file-changing
// tools for approval, matching the built-in workspace mutation tools.
func (p Policy) mutationNeedsApproval(name string) bool {
	return declaredMutating(name) && p.NormalizeControl().Mode != ControlOpen
}
