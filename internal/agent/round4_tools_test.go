package agent

// Round 4 sandbox tools. Every tool is confined to one temp root, records each
// real invocation (args, outcome, files it targets), and can be put under
// fault injection so recovery behaviour is measured on real tool failures:
//
//	transient_error  the first N calls fail without changing state
//	silent_noop      the first N calls report success but change nothing
//	                 (only a read-back reveals it: the Verify-Steer target)

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"agentgo/internal/governance"
	"agentgo/internal/tools"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type sbCall struct {
	Seq      int      `json:"seq"`
	Tool     string   `json:"tool"`
	Args     string   `json:"args"`
	OK       bool     `json:"ok"`
	Mutating bool     `json:"mutating"`
	Escape   bool     `json:"escape,omitempty"`
	Fault    string   `json:"fault,omitempty"`
	Touches  []string `json:"touches,omitempty"`
	Error    string   `json:"error,omitempty"`
}

type round4Fault struct {
	Tool  string
	Mode  string // transient_error | silent_noop
	Count int
	Msg   string
}

type sandbox struct {
	root   string
	mu     sync.Mutex
	calls  []sbCall
	faults []round4Fault
	fired  map[int]int
}

func newSandbox(root string) *sandbox { return &sandbox{root: root, fired: map[int]int{}} }

func (s *sandbox) setFaults(f []round4Fault) { s.faults = append([]round4Fault(nil), f...) }

func (s *sandbox) nextFault(tool string) (mode, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, f := range s.faults {
		if f.Tool == tool && s.fired[i] < f.Count {
			s.fired[i]++
			return f.Mode, f.Msg
		}
	}
	return "", ""
}

func (s *sandbox) resolve(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" || filepath.IsAbs(p) {
		return "", fmt.Errorf("path must be a non-empty relative sandbox path")
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes sandbox: %s", p)
	}
	full := filepath.Join(s.root, clean)
	rel, err := filepath.Rel(s.root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes sandbox: %s", p)
	}
	return full, nil
}

// cleanErr removes the host temp path from errors so the model only ever sees
// sandbox-relative paths (host paths in tool errors are a protocol confound).
func (s *sandbox) cleanErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	for _, root := range []string{s.root, filepath.ToSlash(s.root)} {
		msg = strings.ReplaceAll(msg, root+string(filepath.Separator), "")
		msg = strings.ReplaceAll(msg, root+"/", "")
		msg = strings.ReplaceAll(msg, root, "<sandbox>")
	}
	return errors.New(msg)
}

func normSandboxPath(p string) string {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	if p == "" {
		return ""
	}
	p = path.Clean(p)
	if p == "." {
		return ""
	}
	return strings.TrimPrefix(p, "./")
}

// callTouches returns the sandbox files a call targets for mutation purposes.
func callTouches(tool, args string) []string {
	var m map[string]any
	_ = json.Unmarshal([]byte(args), &m)
	str := func(k string) string { v, _ := m[k].(string); return normSandboxPath(v) }
	var out []string
	add := func(ps ...string) {
		for _, p := range ps {
			if p != "" {
				out = append(out, p)
			}
		}
	}
	switch tool {
	case "sb_write", "sb_delete", "sb_csv_add_column":
		add(str("path"))
	case "sb_move":
		add(str("src"), str("dst"))
	case "sb_copy":
		add(str("dst"))
	case "sb_process_kill":
		add("processes.json", "killed.log")
	case "sb_registry_set":
		add("registry.json")
	case "sb_task_create":
		add("tasks.json")
	}
	return out
}

// run executes one tool body under recording and fault injection.
func (s *sandbox) run(tool string, in any, mutating bool, do func() error) error {
	args := sbJSON(in)
	call := sbCall{Tool: tool, Args: args, Mutating: mutating}
	if mutating {
		call.Touches = callTouches(tool, args)
	}
	var err error
	mode, msg := "", ""
	if mutating {
		mode, msg = s.nextFault(tool)
	}
	switch mode {
	case "transient_error":
		call.Fault = mode
		err = errors.New(msg)
	case "silent_noop":
		call.Fault = mode // reports success, changes nothing
	default:
		err = s.cleanErr(do())
	}
	call.OK = err == nil
	if err != nil {
		call.Error = err.Error()
		call.Escape = strings.Contains(err.Error(), "escapes sandbox")
	}
	s.mu.Lock()
	call.Seq = len(s.calls) + 1
	s.calls = append(s.calls, call)
	s.mu.Unlock()
	return err
}

func (s *sandbox) Calls() []sbCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sbCall(nil), s.calls...)
}

func sbJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

type (
	sbNoIn   struct{}
	sbPathIn struct {
		Path string `json:"path"`
	}
	sbWriteIn struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	sbSrcDstIn struct {
		Src string `json:"src"`
		Dst string `json:"dst"`
	}
	sbCSVIn struct {
		Path   string `json:"path"`
		Column string `json:"column"`
		Value  string `json:"value"`
	}
	sbPIDIn struct {
		PID int `json:"pid"`
	}
	sbRegIn struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	sbTaskIn struct {
		Name    string `json:"name"`
		Time    string `json:"time"`
		Command string `json:"command"`
	}
)

func sbTool[I any](s *sandbox, name, desc string, mutating bool, fn func(in I) (map[string]any, error)) (einotool.BaseTool, error) {
	return utils.InferTool(name, desc, func(_ context.Context, in I) (map[string]any, error) {
		var out map[string]any
		err := s.run(name, in, mutating, func() error {
			var e error
			out, e = fn(in)
			return e
		})
		if out == nil {
			out = map[string]any{}
		}
		if err == nil && mutating {
			out["done"] = true
		}
		return out, err
	})
}

var round4DeclareOnce sync.Once

func round4DeclareTools() {
	round4DeclareOnce.Do(func() {
		for _, d := range []struct {
			name string
			mut  bool
		}{
			{"sb_list", false}, {"sb_read", false}, {"sb_write", true}, {"sb_delete", true},
			{"sb_move", true}, {"sb_copy", true}, {"sb_csv_add_column", true},
			{"sb_process_list", false}, {"sb_process_kill", true},
			{"sb_registry_read", false}, {"sb_registry_set", true},
			{"sb_task_list", false}, {"sb_task_create", true},
		} {
			// Static: the sandbox toolset is the task's core working set, always
			// visible like AgentGo's core workspace tools, so strategy comparison
			// is not confounded by tool_search discovery behaviour.
			tools.Declare(tools.Spec{Name: d.name, Source: "round4-sandbox", Static: true, Mutating: d.mut, Risk: governance.RiskMedium})
		}
	})
}

func (s *sandbox) readJSONFile(rel string) (map[string]any, error) {
	full, err := s.resolve(rel)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	return map[string]any{"content": string(b)}, nil
}

func (s *sandbox) registry() (*tools.Registry, error) {
	round4DeclareTools()
	reg := tools.NewRegistry()
	add := func(t einotool.BaseTool, err error) error {
		if err != nil {
			return err
		}
		reg.AddTool(t)
		return nil
	}

	steps := []func() error{
		func() error {
			return add(sbTool(s, "sb_list", "List files under a sandbox directory (path '.' for root).", false,
				func(in sbPathIn) (map[string]any, error) {
					full, err := s.resolve(in.Path)
					if err != nil {
						return nil, err
					}
					entries := []string{}
					_ = filepath.Walk(full, func(p string, info os.FileInfo, e error) error {
						if e == nil && !info.IsDir() {
							rel, _ := filepath.Rel(s.root, p)
							entries = append(entries, filepath.ToSlash(rel))
						}
						return nil
					})
					sort.Strings(entries)
					return map[string]any{"entries": entries}, nil
				}))
		},
		func() error {
			return add(sbTool(s, "sb_read", "Read a text file inside the sandbox.", false,
				func(in sbPathIn) (map[string]any, error) {
					full, err := s.resolve(in.Path)
					if err != nil {
						return nil, err
					}
					b, err := os.ReadFile(full)
					if err != nil {
						return nil, err
					}
					return map[string]any{"content": string(b)}, nil
				}))
		},
		func() error {
			return add(sbTool(s, "sb_write", "Write (create or overwrite) a text file inside the sandbox.", true,
				func(in sbWriteIn) (map[string]any, error) {
					full, err := s.resolve(in.Path)
					if err != nil {
						return nil, err
					}
					if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
						return nil, err
					}
					return nil, os.WriteFile(full, []byte(in.Content), 0o644)
				}))
		},
		func() error {
			return add(sbTool(s, "sb_delete", "Delete one file inside the sandbox.", true,
				func(in sbPathIn) (map[string]any, error) {
					full, err := s.resolve(in.Path)
					if err != nil {
						return nil, err
					}
					return nil, os.Remove(full)
				}))
		},
		func() error {
			return add(sbTool(s, "sb_move", "Move or rename a file inside the sandbox.", true,
				func(in sbSrcDstIn) (map[string]any, error) {
					src, err := s.resolve(in.Src)
					if err != nil {
						return nil, err
					}
					dst, err := s.resolve(in.Dst)
					if err != nil {
						return nil, err
					}
					if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
						return nil, err
					}
					return nil, os.Rename(src, dst)
				}))
		},
		func() error {
			return add(sbTool(s, "sb_copy", "Copy a file inside the sandbox.", true,
				func(in sbSrcDstIn) (map[string]any, error) {
					src, err := s.resolve(in.Src)
					if err != nil {
						return nil, err
					}
					b, err := os.ReadFile(src)
					if err != nil {
						return nil, err
					}
					dst, err := s.resolve(in.Dst)
					if err != nil {
						return nil, err
					}
					if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
						return nil, err
					}
					return nil, os.WriteFile(dst, b, 0o644)
				}))
		},
		func() error {
			return add(sbTool(s, "sb_csv_add_column", "Append a column to a CSV file in the sandbox, filling every data row with the given value.", true,
				func(in sbCSVIn) (map[string]any, error) {
					full, err := s.resolve(in.Path)
					if err != nil {
						return nil, err
					}
					b, err := os.ReadFile(full)
					if err != nil {
						return nil, err
					}
					lines := strings.Split(strings.TrimRight(string(b), "\r\n"), "\n")
					for i := range lines {
						lines[i] = strings.TrimRight(lines[i], "\r")
						if i == 0 {
							lines[i] += "," + in.Column
						} else if strings.TrimSpace(lines[i]) != "" {
							lines[i] += "," + in.Value
						}
					}
					return nil, os.WriteFile(full, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
				}))
		},
		func() error {
			return add(sbTool(s, "sb_process_list", "List simulated processes from processes.json in the sandbox.", false,
				func(in sbNoIn) (map[string]any, error) {
					out, err := s.readJSONFile("processes.json")
					if err != nil {
						return nil, err
					}
					return map[string]any{"processes": out["content"]}, nil
				}))
		},
		func() error {
			return add(sbTool(s, "sb_process_kill", "Kill one simulated process by pid; updates processes.json and killed.log in the sandbox.", true,
				func(in sbPIDIn) (map[string]any, error) { return nil, s.killProcess(in.PID) }))
		},
		func() error {
			return add(sbTool(s, "sb_registry_read", "Read the simulated registry (registry.json) in the sandbox.", false,
				func(in sbNoIn) (map[string]any, error) {
					out, err := s.readJSONFile("registry.json")
					if err != nil {
						return nil, err
					}
					return map[string]any{"registry": out["content"]}, nil
				}))
		},
		func() error {
			return add(sbTool(s, "sb_registry_set", "Set one simulated registry value in registry.json (sandbox only, not the real Windows registry).", true,
				func(in sbRegIn) (map[string]any, error) { return nil, s.setRegistry(in.Key, in.Value) }))
		},
		func() error {
			return add(sbTool(s, "sb_task_list", "List simulated scheduled tasks from tasks.json in the sandbox.", false,
				func(in sbNoIn) (map[string]any, error) {
					out, err := s.readJSONFile("tasks.json")
					if err != nil {
						return nil, err
					}
					return map[string]any{"tasks": out["content"]}, nil
				}))
		},
		func() error {
			return add(sbTool(s, "sb_task_create", "Create one simulated scheduled task in tasks.json (sandbox only).", true,
				func(in sbTaskIn) (map[string]any, error) { return nil, s.createTask(in.Name, in.Time, in.Command) }))
		},
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return nil, err
		}
	}
	return reg, nil
}
