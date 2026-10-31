package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/agentsmd"
	"github.com/cloudwego/eino/schema"
)

func writeTestFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// relPaths lists the instruction paths relative to base, slash-separated; a
// path outside base stays as it is.
func relPaths(set instructionSet, base string) []string {
	var out []string
	for _, p := range set.Paths {
		if rel, err := filepath.Rel(base, p); err == nil && !strings.HasPrefix(rel, "..") {
			p = rel
		}
		out = append(out, filepath.ToSlash(p))
	}
	return out
}

func newRepo(t *testing.T) (base, repo string) {
	t.Helper()
	base = t.TempDir()
	repo = filepath.Join(base, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return base, repo
}

func TestInstructionsRunFromTheGitRootDownToTheWorkspace(t *testing.T) {
	base, repo := newRepo(t)
	writeTestFile(t, filepath.Join(base, "AGENTS.md"), "ABOVE THE REPOSITORY")
	writeTestFile(t, filepath.Join(repo, "AGENTS.md"), "ROOT")
	writeTestFile(t, filepath.Join(repo, "packages", "CLAUDE.md"), "PACKAGES, written for Claude Code")
	writeTestFile(t, filepath.Join(repo, "packages", "app", "AGENTS.md"), "APP")

	set := discoverInstructions(filepath.Join(repo, "packages", "app"), "")
	want := []string{"AGENTS.md", "packages/CLAUDE.md", "packages/app/AGENTS.md"}
	if got := relPaths(set, repo); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAgentsMDWinsOverClaudeMD(t *testing.T) {
	_, repo := newRepo(t)
	writeTestFile(t, filepath.Join(repo, "AGENTS.md"), "agents")
	writeTestFile(t, filepath.Join(repo, "CLAUDE.md"), "claude")
	if got := relPaths(discoverInstructions(repo, ""), repo); !reflect.DeepEqual(got, []string{"AGENTS.md"}) {
		t.Fatalf("got %v", got)
	}
}

// On a case-insensitive file system AGENTS.md and agents.md are one file; it
// must not be offered to the model twice.
func TestTheSameFileIsLoadedOnce(t *testing.T) {
	_, repo := newRepo(t)
	writeTestFile(t, filepath.Join(repo, "AGENTS.md"), "once")
	if got := discoverInstructions(repo, "").Paths; len(got) != 1 {
		t.Fatalf("got %v", got)
	}
}

func TestSoulAndTeamFilesStayWithTheWorkspaceRoot(t *testing.T) {
	_, repo := newRepo(t)
	writeTestFile(t, filepath.Join(repo, "AGENTS.md"), "a")
	writeTestFile(t, filepath.Join(repo, "SOUL.md"), "s")
	writeTestFile(t, filepath.Join(repo, "TEAM.md"), "t")
	writeTestFile(t, filepath.Join(repo, "sub", "SOUL.md"), "the workspace's own")
	// With the workspace in sub, the repository's SOUL.md and TEAM.md are not
	// instructions for it; only the AGENTS.md above it carries down.
	got := relPaths(discoverInstructions(filepath.Join(repo, "sub"), ""), repo)
	if !reflect.DeepEqual(got, []string{"AGENTS.md", "sub/SOUL.md"}) {
		t.Fatalf("got %v", got)
	}
	got = relPaths(discoverInstructions(repo, ""), repo)
	if !reflect.DeepEqual(got, []string{"AGENTS.md", "SOUL.md", "TEAM.md"}) {
		t.Fatalf("got %v", got)
	}
}

func TestUserLevelInstructionsComeFirst(t *testing.T) {
	_, repo := newRepo(t)
	data := t.TempDir()
	writeTestFile(t, filepath.Join(data, "AGENTS.md"), "USER")
	writeTestFile(t, filepath.Join(repo, "AGENTS.md"), "PROJECT")
	set := discoverInstructions(repo, data)
	if len(set.Paths) != 2 || set.Paths[0] != filepath.Join(data, "AGENTS.md") || len(set.Files) != 1 {
		t.Fatalf("paths %v, files %v", set.Paths, set.Files)
	}
	// Without a workspace the user's own instructions still apply.
	if got := discoverInstructions("", data).Paths; len(got) != 1 {
		t.Fatalf("no workspace: %v", got)
	}
}

func TestParentsSettingTurnsParentsAndUserLevelOff(t *testing.T) {
	t.Setenv("AGENTGO_AGENTSMD_PARENTS", "0")
	_, repo := newRepo(t)
	data := t.TempDir()
	writeTestFile(t, filepath.Join(data, "AGENTS.md"), "USER")
	writeTestFile(t, filepath.Join(repo, "AGENTS.md"), "ROOT")
	writeTestFile(t, filepath.Join(repo, "app", "AGENTS.md"), "APP")
	got := relPaths(discoverInstructions(filepath.Join(repo, "app"), data), repo)
	if !reflect.DeepEqual(got, []string{"app/AGENTS.md"}) {
		t.Fatalf("got %v", got)
	}
}

func TestWithoutAGitRootOnlyTheWorkspaceCounts(t *testing.T) {
	base := t.TempDir()
	writeTestFile(t, filepath.Join(base, "AGENTS.md"), "ABOVE")
	writeTestFile(t, filepath.Join(base, "work", "AGENTS.md"), "WORK")
	got := relPaths(discoverInstructions(filepath.Join(base, "work"), ""), base)
	if !reflect.DeepEqual(got, []string{"work/AGENTS.md"}) {
		t.Fatalf("got %v", got)
	}
}

func writeRules(t *testing.T, repo string) {
	t.Helper()
	rules := filepath.Join(repo, ".cursor", "rules")
	writeTestFile(t, filepath.Join(rules, "always.mdc"), "---\r\ndescription: house style\r\nglobs:\r\nalwaysApply: true\r\n---\r\nUse tabs.\r\n")
	writeTestFile(t, filepath.Join(rules, "plain.md"), "No front matter here.\n")
	writeTestFile(t, filepath.Join(rules, "api.mdc"), "---\ndescription: REST conventions\nglobs: internal/api/**/*.go, *.proto\nalwaysApply: false\n---\nUse verbs.\n")
	writeTestFile(t, filepath.Join(rules, "manual.mdc"), "---\nalwaysApply: false\n---\nOnly when asked.\n")
	writeTestFile(t, filepath.Join(rules, "ts.mdc"), "---\nglobs:\n  - \"**/*.ts\"\n  - '*.tsx'\n---\nTS rules\n")
}

func TestCursorRulesAreOfferedByTheirFrontMatter(t *testing.T) {
	_, repo := newRepo(t)
	writeRules(t, repo)
	set := discoverInstructions(repo, "")

	got := relPaths(set, repo)
	if len(got) != 3 || got[0] != ".cursor/rules/always.mdc" || got[1] != ".cursor/rules/plain.md" ||
		!strings.HasSuffix(got[2], "(rules to read when relevant)") {
		t.Fatalf("loaded: %v", got)
	}
	index := set.Virtual[filepath.ToSlash(set.Paths[2])]
	for _, want := range []string{".cursor/rules/api.mdc", "REST conventions", "internal/api/**/*.go, *.proto", ".cursor/rules/ts.mdc", "**/*.ts, *.tsx"} {
		if !strings.Contains(index, want) {
			t.Errorf("the index lacks %q:\n%s", want, index)
		}
	}
	for _, unwanted := range []string{"manual.mdc", "always.mdc", "plain.md"} {
		if strings.Contains(index, unwanted) {
			t.Errorf("the index must not list %q:\n%s", unwanted, index)
		}
	}
}

func TestRuleFilesAreLoadedWithoutTheirFrontMatter(t *testing.T) {
	_, repo := newRepo(t)
	writeRules(t, repo)
	set := discoverInstructions(repo, "")
	backend := newInstructionBackend(repo, set)
	got, err := backend.Read(context.Background(), &agentsmd.ReadRequest{FilePath: filepath.ToSlash(set.Paths[0])})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(got.Content) != "Use tabs." {
		t.Fatalf("content %q", got.Content)
	}
}

func TestParseRuleFile(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name, text, body string
		want             ruleMeta
	}{
		{"none", "just text\n", "just text\n", ruleMeta{}},
		{"unclosed fence is all body", "---\ndescription: x\nno end\n", "---\ndescription: x\nno end\n", ruleMeta{}},
		{"bom and crlf", "\ufeff---\r\ndescription: \"Quoted: yes\"\r\nalwaysApply: true\r\n---\r\nBody\r\n", "Body\r\n",
			ruleMeta{Present: true, Description: "Quoted: yes", AlwaysApply: &yes}},
		{"bare glob is not yaml", "---\nglobs: *.ts,*.tsx\nalwaysApply: false\n---\nB", "B",
			ruleMeta{Present: true, Globs: []string{"*.ts", "*.tsx"}, AlwaysApply: &no}},
		{"inline list", "---\nglobs: [\"a/**\", 'b']\n---\nB", "B", ruleMeta{Present: true, Globs: []string{"a/**", "b"}}},
		{"block list", "---\nglobs:\n  - a\n  - \"b c\"\ndescription: d\n---\nB", "B",
			ruleMeta{Present: true, Description: "d", Globs: []string{"a", "b c"}}},
		{"unknown keys and comments", "---\n# note\ntitle: x\n---\nB", "B", ruleMeta{Present: true}},
	}
	for _, c := range cases {
		meta, body := parseRuleFile(c.text)
		if !reflect.DeepEqual(meta, c.want) || body != c.body {
			t.Errorf("%s: got %+v %q, want %+v %q", c.name, meta, body, c.want, c.body)
		}
	}
}

func TestInstructionBackendOnlyReadsWhatIsAllowed(t *testing.T) {
	base, repo := newRepo(t)
	data := t.TempDir()
	writeTestFile(t, filepath.Join(data, "AGENTS.md"), "USER")
	writeTestFile(t, filepath.Join(data, "config.json"), `{"api_key":"SECRET"}`)
	writeTestFile(t, filepath.Join(repo, "AGENTS.md"), "ROOT")
	writeTestFile(t, filepath.Join(repo, "docs", "guide.md"), "GUIDE")
	writeTestFile(t, filepath.Join(base, "outside.md"), "OUTSIDE")
	backend := newInstructionBackend(repo, discoverInstructions(repo, data))
	read := func(p string) (string, error) {
		fc, err := backend.Read(context.Background(), &agentsmd.ReadRequest{FilePath: filepath.ToSlash(p)})
		if err != nil {
			return "", err
		}
		return fc.Content, nil
	}

	for path, want := range map[string]string{
		filepath.Join(repo, "AGENTS.md"):        "ROOT",
		filepath.Join(repo, "docs", "guide.md"): "GUIDE", // an @import within the project
		filepath.Join(data, "AGENTS.md"):        "USER",
		filepath.Join("docs", "guide.md"):       "GUIDE", // relative to the workspace
	} {
		if got, err := read(path); err != nil || got != want {
			t.Errorf("%s: %q, %v", path, got, err)
		}
	}
	for _, path := range []string{
		filepath.Join(base, "outside.md"),       // above the project
		filepath.Join(repo, "..", "outside.md"), // reached through ..
		filepath.Join(data, "config.json"),      // next to the user-level file, but not it
		filepath.Join(repo, "docs"),             // a directory
		filepath.Join(repo, "no-such-file.md"),
	} {
		if got, err := read(path); !errors.Is(err, os.ErrNotExist) || got != "" {
			t.Errorf("%s must read as missing, got %q, %v", path, got, err)
		}
	}

	// A link inside the project must not lead out of it.
	link := filepath.Join(repo, "link.md")
	if err := os.Symlink(filepath.Join(base, "outside.md"), link); err != nil {
		t.Logf("no symlink here (%v); skipping the link case", err)
		return
	}
	if got, err := read(link); !errors.Is(err, os.ErrNotExist) || got != "" {
		t.Fatalf("a link out of the project was followed: %q, %v", got, err)
	}
}

// The whole chain: what the model is given, in order, with an @import that
// tries to leave the project, and one that names a directory.
func TestAgentsMDMiddlewareInjectsEveryLayerInOrder(t *testing.T) {
	base, repo := newRepo(t)
	data := t.TempDir()
	writeTestFile(t, filepath.Join(base, "secret.md"), "THE SECRET")
	writeTestFile(t, filepath.Join(data, "AGENTS.md"), "USER-LEVEL")
	writeTestFile(t, filepath.Join(repo, "AGENTS.md"), "ROOT-LEVEL @docs/extra.md")
	writeTestFile(t, filepath.Join(repo, "docs", "extra.md"), "IMPORTED")
	writeTestFile(t, filepath.Join(repo, "app", "AGENTS.md"), "APP-LEVEL @../../secret.md and @docs/ for details")
	app := filepath.Join(repo, "app")
	writeRules(t, app)

	set := discoverInstructions(app, data)
	mw, err := agentsmd.NewTyped[*schema.Message](context.Background(), &agentsmd.Config{
		Backend:             newInstructionBackend(app, set),
		AgentsMDFiles:       set.Paths,
		AllAgentsMDMaxBytes: 120_000,
		PerAgentsMDMaxBytes: 32_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	state := &adk.TypedChatModelAgentState[*schema.Message]{Messages: []*schema.Message{schema.UserMessage("hello")}}
	_, out, err := mw.BeforeModelRewriteState(context.Background(), state, nil)
	if err != nil {
		t.Fatalf("an unreadable @import must not end the load: %v", err)
	}
	if len(out.Messages) != 2 {
		t.Fatalf("expected the instructions before the user message, got %d messages", len(out.Messages))
	}
	text := out.Messages[0].Content
	last := -1
	for _, want := range []string{"USER-LEVEL", "ROOT-LEVEL", "IMPORTED", "APP-LEVEL", "rules to read when relevant"} {
		at := strings.Index(text, want)
		if at < 0 || at < last {
			t.Fatalf("%q is missing or out of order (at %d, after %d):\n%s", want, at, last, text)
		}
		last = at
	}
	if strings.Contains(text, "THE SECRET") {
		t.Fatal("an @import left the project")
	}
}
