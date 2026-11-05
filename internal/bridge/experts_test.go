package bridge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// expertRuntime boots a full runtime and returns its data folder. The office, code
// and document tools are contributed when the app service starts, so it starts
// one: the experts built around those tools are on offer only once they exist.
func expertRuntime(t *testing.T) (*Runtime, string) {
	t.Helper()
	dir := t.TempDir()
	rt := bootIn(t, dir, nil)
	app := NewAppService(rt)
	t.Cleanup(app.Close)
	return rt, dir
}

func writeExpertFile(t *testing.T, dir, id, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "---\nname: " + name + "\nsummary: 测试用的专家\ncategory: research\n---\n" +
		"每次回答先给出结论，再给出依据，最后列出还没有核实的地方，不要省略这三部分。\n"
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func rowByID(t *testing.T, list any, id string) map[string]any {
	t.Helper()
	rows, ok := list.([]map[string]any)
	if !ok {
		t.Fatalf("unexpected list type %T", list)
	}
	for _, r := range rows {
		if r["id"] == id {
			return r
		}
	}
	return nil
}

func TestOfficialExpertsAreOfferedOnlyWhileTheirToolsExist(t *testing.T) {
	rt, _ := expertRuntime(t)
	if _, ok := rt.expertLibrary().Get("financial-modeler"); !ok {
		t.Fatal("financial-modeler must be on offer while the office tools exist")
	}
	rt.toolReg.RemoveTool("edit_xlsx") // what a build or a switch without the office tools looks like
	lib := rt.expertLibrary()
	for _, id := range []string{"financial-modeler", "dashboard-builder"} {
		if _, ok := lib.Get(id); ok {
			t.Errorf("%s is offered although its key tool is gone", id)
		}
	}
	if _, ok := lib.Get("office-assistant"); !ok {
		t.Error("an expert whose own key tool is still there must stay")
	}
	if _, ok := lib.Get("translator"); !ok {
		t.Error("an expert that needs no optional tool must stay")
	}
}

func TestConversationCreateOpensAnExpertConversation(t *testing.T) {
	rt, _ := expertRuntime(t)
	ctx := context.Background()
	want, ok := rt.expertLibrary().Get("financial-modeler")
	if !ok {
		t.Fatal("the official financial-modeler expert is missing")
	}

	res, err := rt.CreateConversation(ctx, "sess_from", "季度预算", "financial-modeler")
	if err != nil {
		t.Fatal(err)
	}
	row := res.(map[string]any)
	id, _ := row["id"].(string)
	if id == "" || row["title"] != "季度预算" {
		t.Fatalf("result: %v", row)
	}
	got, err := rt.Sessions().Expert(ctx, id)
	if err != nil || got != "financial-modeler" {
		t.Fatalf("bound expert %q err=%v", got, err)
	}
	// What the agent will carry into every run of that conversation.
	block := rt.AgentRunner().ExpertBlock(id)
	for _, need := range []string{"[Expert: " + want.Name, "financial-modeler", "never replace them", "skill:office-financial-model"} {
		if !strings.Contains(block, need) {
			t.Errorf("the expert block lacks %q:\n%s", need, block)
		}
	}
	// By name works as well as by id, and a plain conversation has no block.
	if _, err := rt.CreateConversation(ctx, "sess_from", "按名字开", want.Name); err != nil {
		t.Fatalf("by name: %v", err)
	}
	plain, err := rt.CreateConversation(ctx, "sess_from", "普通对话", "")
	if err != nil {
		t.Fatal(err)
	}
	pid := plain.(map[string]any)["id"].(string)
	if b := rt.AgentRunner().ExpertBlock(pid); b != "" {
		t.Fatalf("an ordinary conversation must have no expert block: %q", b)
	}
	// It only creates: the new conversation is empty.
	msgs, err := rt.Sessions().GetMessages(ctx, id, 10)
	if err != nil || len(msgs) != 0 {
		t.Fatalf("a new conversation must be empty: %v messages=%d", err, len(msgs))
	}
}

func TestConversationCreateRefusals(t *testing.T) {
	rt, _ := expertRuntime(t)
	ctx := context.Background()
	before, _ := rt.Sessions().List(ctx, 100)

	_, err := rt.CreateConversation(ctx, "s", "x", "no-such-expert")
	if err == nil || !strings.Contains(err.Error(), "software-engineer") {
		t.Fatalf("an unknown expert must be refused with the list of experts: %v", err)
	}
	if _, err := rt.CreateConversation(ctx, "s", "   ", ""); err == nil {
		t.Fatal("an empty name must be refused")
	}
	after, _ := rt.Sessions().List(ctx, 100)
	if len(after) != len(before) {
		t.Fatalf("a refused call must not leave a conversation behind: %d -> %d", len(before), len(after))
	}
}

func TestConversationCreateIsRateLimitedPerSender(t *testing.T) {
	rt, _ := expertRuntime(t)
	ctx := context.Background()
	for i := 0; i < convCreateSenderLimit; i++ {
		if _, err := rt.CreateConversation(ctx, "loop", "第几个", ""); err != nil {
			t.Fatalf("conversation %d: %v", i, err)
		}
	}
	before, _ := rt.Sessions().List(ctx, 100)
	if _, err := rt.CreateConversation(ctx, "loop", "多了", ""); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("a runaway loop must be cut off: %v", err)
	}
	after, _ := rt.Sessions().List(ctx, 100)
	if len(after) != len(before) {
		t.Fatalf("a refused call must create nothing: %d -> %d", len(before), len(after))
	}
	// Another conversation has its own allowance.
	if _, err := rt.CreateConversation(ctx, "someone-else", "别人的", ""); err != nil {
		t.Fatalf("another sender must not be affected: %v", err)
	}
}

func TestListExpertsForTheModelNamesOnlySkillsThatExist(t *testing.T) {
	rt, dir := expertRuntime(t)
	writeExpertFile(t, filepath.Join(dir, "experts"), "mine", "我的专家")
	list, err := rt.ListExperts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	fin := rowByID(t, list, "financial-modeler")
	if fin == nil {
		t.Fatal("financial-modeler is not listed")
	}
	skills, _ := fin["skills"].([]string)
	if len(skills) == 0 {
		t.Fatalf("financial-modeler must name its skills: %v", fin)
	}
	for _, s := range skills {
		if !rt.skillExists(s) {
			t.Errorf("listed skill %q does not exist", s)
		}
	}
	mine := rowByID(t, list, "mine")
	if mine == nil || mine["source"] != "user" {
		t.Fatalf("a file in <data>/experts must be listed as the user's: %v", mine)
	}
}

func TestExpertIPCRoundTrip(t *testing.T) {
	rt, _ := expertRuntime(t)
	s := &AppService{rt: rt}

	r := s.NewSessionWithExpert("", "bug-hunter")
	if r["success"] != true {
		t.Fatalf("NewSessionWithExpert: %v", r)
	}
	id := r["id"].(string)
	if sess := r["session"]; sess == nil {
		t.Fatalf("the answer must carry the session like NewSession does: %v", r)
	}

	g := s.GetSessionExpert(id)
	if g["success"] != true || g["expert_id"] != "bug-hunter" || g["missing"] != false || g["expert"] == nil {
		t.Fatalf("GetSessionExpert: %v", g)
	}
	foundRow := false
	for _, row := range s.ListSessions() {
		if row["id"] == id {
			foundRow = true
			if row["expert_id"] != "bug-hunter" {
				t.Fatalf("the sidebar row must say which expert: %v", row)
			}
		}
	}
	if !foundRow {
		t.Fatal("the new conversation is not in the list")
	}

	// A wrong name changes nothing; a right one switches; empty clears.
	if bad := s.SetSessionExpert(id, "no-such-expert"); bad["success"] != false {
		t.Fatalf("an unknown expert must be refused: %v", bad)
	}
	if now := s.GetSessionExpert(id); now["expert_id"] != "bug-hunter" {
		t.Fatalf("a refused change must keep the binding: %v", now)
	}
	if sw := s.SetSessionExpert(id, "code-reviewer"); sw["success"] != true || sw["expert_id"] != "code-reviewer" {
		t.Fatalf("switch: %v", sw)
	}
	if cl := s.SetSessionExpert(id, ""); cl["success"] != true || cl["expert_id"] != "" {
		t.Fatalf("clear: %v", cl)
	}
	if now := s.GetSessionExpert(id); now["expert_id"] != "" || now["expert"] != nil || now["missing"] != false {
		t.Fatalf("after clearing: %v", now)
	}
	if gone := s.SetSessionExpert("sess_nope", "bug-hunter"); gone["success"] != false {
		t.Fatalf("a conversation that does not exist must be refused: %v", gone)
	}
	if bad := s.NewSessionWithExpert("x", "no-such-expert"); bad["success"] != false {
		t.Fatalf("opening as an unknown expert must be refused: %v", bad)
	}
}

func TestExpertFilesAreReadFreshAndAGoneFileMeansNoExpert(t *testing.T) {
	rt, dir := expertRuntime(t)
	ws := t.TempDir()
	rt.mu.Lock()
	rt.workspace = ws
	rt.mu.Unlock()
	s := &AppService{rt: rt}
	userDir := filepath.Join(dir, "experts")
	wsDir := filepath.Join(ws, ".agentgo", "experts")
	writeExpertFile(t, userDir, "mine", "我的专家")
	writeExpertFile(t, wsDir, "teamlead", "组内专家")

	list := s.ListExperts()
	rows := list["experts"].([]map[string]any)
	sources := map[string]any{}
	for _, r := range rows {
		sources[r["id"].(string)] = r["source"]
	}
	if sources["mine"] != "user" || sources["teamlead"] != "workspace" {
		t.Fatalf("sources: mine=%v teamlead=%v", sources["mine"], sources["teamlead"])
	}
	if list["user_dir"] != userDir || list["workspace_dir"] != wsDir {
		t.Fatalf("the page must be told where to put one's own: %v %v", list["user_dir"], list["workspace_dir"])
	}

	r := s.NewSessionWithExpert("用我的", "mine")
	if r["success"] != true {
		t.Fatalf("%v", r)
	}
	id := r["id"].(string)
	if block := rt.AgentRunner().ExpertBlock(id); !strings.Contains(block, "我的专家") {
		t.Fatalf("the user's expert must reach the prompt: %q", block)
	}

	// Edit the file: the next run sees the new rules without a restart.
	text := "---\nname: 我的专家\nsummary: 改过了\n---\n改过之后的规则：每次先复述需求，再动手，复述必须不超过两句话。\n"
	if err := os.WriteFile(filepath.Join(userDir, "mine.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	if block := rt.AgentRunner().ExpertBlock(id); !strings.Contains(block, "改过之后的规则") {
		t.Fatalf("an edited expert must apply to the next run: %q", block)
	}

	// Delete the file: the conversation still runs, without an expert, and says so.
	if err := os.Remove(filepath.Join(userDir, "mine.md")); err != nil {
		t.Fatal(err)
	}
	if block := rt.AgentRunner().ExpertBlock(id); block != "" {
		t.Fatalf("a gone expert must mean no block: %q", block)
	}
	g := s.GetSessionExpert(id)
	if g["missing"] != true || g["expert"] != nil || g["expert_id"] != "mine" {
		t.Fatalf("the page must be told the expert is gone: %v", g)
	}
}
