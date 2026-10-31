// Package pathgrant records which folders outside a workspace the user has let
// the agent use. A tool call that names a path outside the workspace is held for
// approval (see governance.PathPolicyStage); approving it grants that call's
// folder for the rest of the program's run, and the tools' path jails then accept
// paths inside it. Grants live in memory only: a restart asks again.
//
// The package depends on the standard library alone so that governance and the
// tool packages can both use it.
package pathgrant

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Registry holds the granted folders (or single files) per workspace root.
type Registry struct {
	mu        sync.RWMutex
	grants    map[string][]string
	protected []string
}

// New returns an empty registry.
func New() *Registry { return &Registry{grants: map[string][]string{}} }

// Default is the process-wide registry the application uses.
var Default = New()

// Resolve returns the one spelling of p that every comparison in this package
// uses: absolute, cleaned, with symbolic links, junctions and Windows 8.3 short
// names (RUNNER~1) expanded. A path that does not exist yet keeps its missing
// tail and has its nearest existing ancestor expanded, so a file about to be
// created is judged by where it will really land. When nothing can be resolved
// the cleaned absolute path is returned.
//
// Grants and the jails that consult them must agree on this spelling: the
// model may name a folder one way, the application's workspace another, and
// the file system treats them as the same place.
func Resolve(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	abs = filepath.Clean(abs)

	cursor, tail := abs, ""
	for {
		if _, err := os.Lstat(cursor); err == nil {
			break
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			return abs // nothing on this path exists
		}
		tail = filepath.Join(filepath.Base(cursor), tail)
		cursor = parent
	}
	real, err := filepath.EvalSymlinks(cursor)
	if err != nil {
		return abs
	}
	if tail == "" {
		return filepath.Clean(real)
	}
	return filepath.Join(real, tail)
}

// fold drops the letter case where the file system ignores it.
func fold(p string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(p)
	}
	return p
}

func norm(p string) string { return fold(Resolve(p)) }

// Under reports whether path is dir itself or inside it.
func Under(path, dir string) bool {
	return underNorm(norm(path), norm(dir))
}

func underNorm(p, d string) bool {
	if p == d {
		return true
	}
	rel, err := filepath.Rel(d, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// Grant lets the agent use path (a folder, or a single file) while working in
// the workspace at root. Forbidden locations are refused.
func (r *Registry) Grant(root, path string) bool {
	if r == nil || strings.TrimSpace(root) == "" || strings.TrimSpace(path) == "" || r.Forbidden(path) != "" {
		return false
	}
	key := norm(root)
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, g := range r.grants[key] {
		if Under(path, g) {
			return true
		}
	}
	r.grants[key] = append(r.grants[key], Resolve(path))
	return true
}

// Allowed reports whether path lies inside a folder granted for the workspace.
func (r *Registry) Allowed(root, path string) bool {
	if r == nil || strings.TrimSpace(root) == "" || strings.TrimSpace(path) == "" {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, g := range r.grants[norm(root)] {
		if Under(path, g) {
			return true
		}
	}
	return false
}

// List returns the folders granted for the workspace.
func (r *Registry) List(root string) []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.grants[norm(root)]...)
}

// Revoke drops every grant of the workspace.
func (r *Registry) Revoke(root string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.grants, norm(root))
}

// Protect marks a folder that can never be granted, such as the application's
// own data folder (it holds the model API key).
func (r *Registry) Protect(dir string) {
	if r == nil || strings.TrimSpace(dir) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.protected = append(r.protected, dir)
}

// Forbidden returns why path may not be granted, or "" when it may. Drive
// roots, operating-system folders, credential folders and Protect()ed folders
// are refused whatever the user answers.
func (r *Registry) Forbidden(path string) string {
	if _, err := filepath.Abs(path); err != nil {
		return "路径无法解析"
	}
	// Judge the real location: a short name, a junction or a symlink that leads
	// into a closed folder is that folder.
	abs := Resolve(path)
	if filepath.Dir(abs) == abs {
		return "磁盘根目录"
	}
	p := fold(abs)
	for _, d := range systemDirs() {
		if underNorm(p, norm(d)) {
			return "系统目录 " + d
		}
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		for _, sub := range []string{".ssh", ".aws", ".gnupg", ".kube", ".docker", ".config/gcloud", "AppData/Roaming/Microsoft/Credentials", "AppData/Local/Microsoft/Credentials"} {
			if d := filepath.Join(home, filepath.FromSlash(sub)); underNorm(p, norm(d)) {
				return "凭据目录 " + d
			}
		}
	}
	if r != nil {
		r.mu.RLock()
		defer r.mu.RUnlock()
		for _, d := range r.protected {
			if underNorm(p, norm(d)) {
				return "AgentGo 自身的数据目录"
			}
		}
	}
	return ""
}

func systemDirs() []string {
	if runtime.GOOS == "windows" {
		var out []string
		for _, env := range []string{"SystemRoot", "windir", "ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"} {
			if v := strings.TrimSpace(os.Getenv(env)); v != "" {
				out = append(out, v)
			}
		}
		return out
	}
	return []string{"/etc", "/boot", "/sys", "/proc", "/dev", "/bin", "/sbin", "/lib", "/usr/bin", "/usr/sbin", "/usr/lib"}
}

// DirFor returns what to grant for a path the model named: the folder itself
// when path is one, otherwise the folder that holds the file. A file that sits
// directly in a drive root is granted by itself, never the whole drive.
func DirFor(path string, isDirKey bool) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if isDirKey {
		return abs
	}
	if fi, err := os.Stat(abs); err == nil && fi.IsDir() {
		return abs
	}
	parent := filepath.Dir(abs)
	if filepath.Dir(parent) == parent {
		return abs
	}
	return parent
}
