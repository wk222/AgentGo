package workflow

import (
	"fmt"
	"strings"
	"sync"
)

var (
	extMu        sync.RWMutex
	extExecutors = map[string]NodeExecutor{}
	extSeq       int64
	extGen       = map[string]int64{}
)

// RegisterNodeExecutor lets a plugin contribute a workflow node type. Built-in
// types cannot be shadowed and a type can only be contributed once at a time.
// Registries created afterwards (every workflow run builds a fresh one) see it;
// the returned func withdraws it.
func RegisterNodeExecutor(typ string, e NodeExecutor) (dispose func(), err error) {
	typ = strings.ToLower(strings.TrimSpace(typ))
	if typ == "" || e == nil {
		return nil, fmt.Errorf("workflow: node type and executor are required")
	}
	if newBuiltinRegistry().Get(typ) != nil {
		return nil, fmt.Errorf("workflow: node type %q is built in", typ)
	}
	extMu.Lock()
	defer extMu.Unlock()
	if _, ok := extExecutors[typ]; ok {
		return nil, fmt.Errorf("workflow: node type %q is already contributed", typ)
	}
	extSeq++
	seq := extSeq
	extExecutors[typ] = e
	extGen[typ] = seq
	return func() {
		extMu.Lock()
		defer extMu.Unlock()
		if extGen[typ] == seq {
			delete(extExecutors, typ)
			delete(extGen, typ)
		}
	}, nil
}

// NodeTypeAvailable reports whether a node type can currently be executed.
func NodeTypeAvailable(typ string) bool { return NewExecutorRegistry().Get(typ) != nil }
