// Package config is the one place where AgentGo reads its AGENTGO_* runtime
// settings. Every setting is declared once (name, type, default, group, doc) in
// flags.go and read through a typed handle, so the full set of knobs can be
// listed, documented and tested. The process environment stays the backing
// store: launchers and tests set the variable, code reads the handle.
//
// internal/arch forbids reading AGENTGO_* with os.Getenv anywhere else.
package config

import (
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Kind is the value type of a flag.
type Kind string

const (
	KindBool   Kind = "bool"
	KindString Kind = "string"
	KindInt    Kind = "int"
	KindFloat  Kind = "float"
)

// Group orders flags in the settings "advanced" view.
type Group string

const (
	GroupModel    Group = "model"
	GroupAgent    Group = "agent"
	GroupMemory   Group = "memory"
	GroupMatrix   Group = "matrix"
	GroupGateway  Group = "gateway"
	GroupPlugins  Group = "plugins"
	GroupSandbox  Group = "sandbox"
	GroupWorkflow Group = "workflow"
	GroupDebug    Group = "debug"
)

// Info describes one declared flag.
type Info struct {
	Name    string `json:"name"`
	Kind    Kind   `json:"kind"`
	Default string `json:"default"`
	Group   Group  `json:"group"`
	Doc     string `json:"doc"`
	Secret  bool   `json:"secret,omitempty"` // value is never shown
}

// Entry is a flag with its current effective value.
type Entry struct {
	Info
	Value string `json:"value"`
	Set   bool   `json:"set"` // true when the environment overrides the default
}

var (
	regMu    sync.Mutex
	registry = map[string]Info{}
)

func register(i Info) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := registry[i.Name]; dup {
		panic("config: flag declared twice: " + i.Name)
	}
	registry[i.Name] = i
}

func raw(name string) (string, bool) {
	v, ok := os.LookupEnv(name)
	v = strings.TrimSpace(v)
	return v, ok && v != ""
}

// set / unset wrap the environment; they exist so that launchers (cmd/*) do not
// touch AGENTGO_* with os.Setenv directly.
func set(name, value string) { _ = os.Setenv(name, value) }

func setDefault(name, value string) {
	if _, ok := raw(name); !ok {
		set(name, value)
	}
}

// Bool is an on/off flag. "1/true/on/yes" is on, "0/false/off/no" is off, anything
// else (including unset) falls back to the declared default.
type Bool struct{ info Info }

// On reports the effective value.
func (f Bool) On() bool {
	on, ok := f.Lookup()
	if !ok {
		return f.info.Default == "1"
	}
	return on
}

// Lookup returns the explicit value; ok is false when the flag is unset or unparsable.
func (f Bool) Lookup() (on, ok bool) {
	v, has := raw(f.info.Name)
	if !has {
		return false, false
	}
	switch strings.ToLower(v) {
	case "1", "true", "on", "yes":
		return true, true
	case "0", "false", "off", "no":
		return false, true
	}
	return false, false
}

// OnOr returns the explicit value, or fallback when the flag is unset. It models
// "unset follows another switch" flags.
func (f Bool) OnOr(fallback bool) bool {
	if on, ok := f.Lookup(); ok {
		return on
	}
	return fallback
}

func (f Bool) Name() string { return f.info.Name }

// Set overrides the flag for this process and its children.
func (f Bool) Set(on bool) {
	if on {
		set(f.info.Name, "1")
	} else {
		set(f.info.Name, "0")
	}
}

// SetDefault applies a launcher default unless the user already set the flag.
func (f Bool) SetDefault(on bool) {
	if on {
		setDefault(f.info.Name, "1")
	} else {
		setDefault(f.info.Name, "0")
	}
}

// Str is a string flag.
type Str struct{ info Info }

// Get returns the trimmed value, or the declared default when unset.
func (f Str) Get() string {
	if v, ok := raw(f.info.Name); ok {
		return v
	}
	return f.info.Default
}

// IsSet reports whether the user provided a value.
func (f Str) IsSet() bool { _, ok := raw(f.info.Name); return ok }

func (f Str) Name() string { return f.info.Name }

func (f Str) Set(v string) { set(f.info.Name, v) }

// SetDefault applies a launcher default unless the user already set the flag.
func (f Str) SetDefault(v string) { setDefault(f.info.Name, v) }

// Int is an integer flag.
type Int struct{ info Info }

// Get returns the value, or the declared default when unset or invalid.
func (f Int) Get() int {
	def, _ := strconv.Atoi(f.info.Default)
	v, ok := raw(f.info.Name)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func (f Int) Name() string { return f.info.Name }

// Float is a floating point flag.
type Float struct{ info Info }

// Get returns the value, or the declared default when unset or invalid.
func (f Float) Get() float64 {
	def, _ := strconv.ParseFloat(f.info.Default, 64)
	v, ok := raw(f.info.Name)
	if !ok {
		return def
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return n
}

func (f Float) Name() string { return f.info.Name }

func newBool(name string, def bool, g Group, doc string) Bool {
	d := "0"
	if def {
		d = "1"
	}
	i := Info{Name: name, Kind: KindBool, Default: d, Group: g, Doc: doc}
	register(i)
	return Bool{i}
}

func newStr(name, def string, g Group, doc string) Str {
	i := Info{Name: name, Kind: KindString, Default: def, Group: g, Doc: doc}
	register(i)
	return Str{i}
}

func newSecret(name string, g Group, doc string) Str {
	i := Info{Name: name, Kind: KindString, Group: g, Doc: doc, Secret: true}
	register(i)
	return Str{i}
}

func newInt(name string, def int, g Group, doc string) Int {
	i := Info{Name: name, Kind: KindInt, Default: strconv.Itoa(def), Group: g, Doc: doc}
	register(i)
	return Int{i}
}

func newFloat(name string, def float64, g Group, doc string) Float {
	i := Info{Name: name, Kind: KindFloat, Default: strconv.FormatFloat(def, 'f', -1, 64), Group: g, Doc: doc}
	register(i)
	return Float{i}
}

// Snapshot lists every declared flag with its effective value, grouped and
// sorted by name. Secret values are masked.
func Snapshot() []Entry {
	regMu.Lock()
	defer regMu.Unlock()
	out := make([]Entry, 0, len(registry))
	for _, i := range registry {
		v, set := raw(i.Name)
		if !set {
			v = i.Default
		}
		if i.Secret && set {
			v = "********"
		}
		out = append(out, Entry{Info: i, Value: v, Set: set})
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Group != out[b].Group {
			return out[a].Group < out[b].Group
		}
		return out[a].Name < out[b].Name
	})
	return out
}

// Raw returns the trimmed value of a flag ("" when unset). It exists for code
// that takes an injectable lookup function (pure planners with unit tests);
// everything else should use the typed handles.
func Raw(name string) string {
	v, _ := raw(name)
	return v
}

// Declared reports whether name is a registered flag.
func Declared(name string) bool {
	regMu.Lock()
	defer regMu.Unlock()
	_, ok := registry[name]
	return ok
}
