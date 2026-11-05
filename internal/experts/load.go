package experts

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"agentgo/internal/textenc"
)

// Options names the folders user and workspace experts are read from. An empty
// name is skipped, and so is a folder that does not exist.
type Options struct {
	UserDir      string // <data folder>/experts
	WorkspaceDir string // <workspace>/.agentgo/experts
	// HasTool says whether the running program offers a tool. When it is set, an
	// official expert whose key tool is missing is left out; nil assumes every tool is there.
	HasTool func(name string) bool
}

// Catalog is every expert available now, plus what was wrong with the files that
// could not be used. A bad file never hides the others.
type Catalog struct {
	Experts  []Expert `json:"experts"`
	Warnings []string `json:"warnings,omitempty"`
}

// Load reads the experts inside the program, then the user's, then the
// workspace's; the same id in a later source replaces the earlier one. It reads
// the folders every time it is called, so an edited file is picked up at once.
func Load(opts Options) Catalog {
	var c Catalog
	byID := map[string]Expert{}
	put := func(e Expert) {
		if old, ok := byID[e.ID]; ok {
			e.Overrides = old.Source
		}
		byID[e.ID] = e
	}
	official, warns := officialExperts(opts.HasTool)
	c.Warnings = append(c.Warnings, warns...)
	for _, e := range official {
		put(e)
	}
	for _, d := range []struct {
		dir string
		src Source
	}{{opts.UserDir, SourceUser}, {opts.WorkspaceDir, SourceWorkspace}} {
		list, warns := loadDir(d.dir, d.src)
		c.Warnings = append(c.Warnings, warns...)
		for _, e := range list {
			put(e)
		}
	}
	for _, e := range byID {
		c.Experts = append(c.Experts, e)
	}
	sort.Slice(c.Experts, func(i, j int) bool { return less(c.Experts[i], c.Experts[j]) })
	return c
}

// categoryOrder is how the picker groups experts; other categories follow, by name.
var categoryOrder = []string{"office", "code", "research", "automation", "agentgo", "writing"}

func categoryRank(c string) int {
	for i, name := range categoryOrder {
		if c == name {
			return i
		}
	}
	return len(categoryOrder)
}

func less(a, b Expert) bool {
	if ra, rb := categoryRank(a.Category), categoryRank(b.Category); ra != rb {
		return ra < rb
	}
	if a.Category != b.Category {
		return a.Category < b.Category
	}
	if a.Order != b.Order {
		return a.Order < b.Order
	}
	return a.ID < b.ID
}

// loadDir reads the *.md files of one folder. Names that start with "." or "_"
// are skipped, which lets a draft sit in the folder unused.
func loadDir(dir string, src Source) ([]Expert, []string) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("读不了专家文件夹 %s：%v", dir, err)}
	}
	var out []Expert
	var warns []string
	seen := 0
	for _, de := range entries {
		name := de.Name()
		if de.IsDir() || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || !strings.EqualFold(filepath.Ext(name), ".md") {
			continue
		}
		if seen++; seen > maxFilesPerDir {
			warns = append(warns, fmt.Sprintf("%s 里的专家文件超过 %d 个，只读了前 %d 个", dir, maxFilesPerDir, maxFilesPerDir))
			break
		}
		path := filepath.Join(dir, name)
		data, err := readCapped(path)
		if err != nil {
			warns = append(warns, fmt.Sprintf("%s：%v", path, err))
			continue
		}
		text, _ := textenc.Decode(data)
		e, notes, err := Parse(strings.TrimSuffix(name, filepath.Ext(name)), text)
		for _, n := range notes {
			warns = append(warns, fmt.Sprintf("%s：%s", path, n))
		}
		if err != nil {
			warns = append(warns, fmt.Sprintf("%s：%v（这个专家没有加载）", path, err))
			continue
		}
		e.Source, e.Path = src, path
		out = append(out, e)
	}
	return out, warns
}

func readCapped(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFileBytes {
		return nil, fmt.Errorf("文件超过 %d KB，不像一份专家说明", maxFileBytes>>10)
	}
	return data, nil
}

// Get finds an expert by id, then by name (ignoring case), then by an id spelled
// with spaces or underscores ("Financial Modeler").
func (c Catalog) Get(key string) (Expert, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return Expert{}, false
	}
	for _, e := range c.Experts {
		if e.ID == key {
			return e, true
		}
	}
	for _, e := range c.Experts {
		if strings.EqualFold(e.Name, key) {
			return e, true
		}
	}
	slug := strings.ToLower(strings.NewReplacer(" ", "-", "_", "-").Replace(key))
	for _, e := range c.Experts {
		if e.ID == slug {
			return e, true
		}
	}
	return Expert{}, false
}

// Find is Get that says what exists when there is no match, so a caller (the
// model, through a tool) can correct itself.
func (c Catalog) Find(key string) (Expert, error) {
	if e, ok := c.Get(key); ok {
		return e, nil
	}
	if strings.TrimSpace(key) == "" {
		return Expert{}, errors.New("没有指定专家")
	}
	return Expert{}, fmt.Errorf("没有叫 %q 的专家。现有专家：%s", key, c.Brief())
}

// Brief lists "id（name）" for every expert, for messages.
func (c Catalog) Brief() string {
	if len(c.Experts) == 0 {
		return "（没有）"
	}
	parts := make([]string, 0, len(c.Experts))
	for _, e := range c.Experts {
		parts = append(parts, fmt.Sprintf("%s（%s）", e.ID, e.Name))
	}
	return strings.Join(parts, "、")
}
