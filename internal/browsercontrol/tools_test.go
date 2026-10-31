package browsercontrol

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

func infoOf(t *testing.T, bt tool.BaseTool) (name, desc, schema string) {
	t.Helper()
	info, err := bt.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	js, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(js)
	return info.Name, info.Desc, string(raw)
}

func TestTheToolSetIsCompleteAndActingToolsAreTheOnesThatChangeThings(t *testing.T) {
	ts, err := Tools(New(Config{}))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	acting := map[string]bool{}
	for _, bt := range ts {
		name, desc, _ := infoOf(t, bt)
		names = append(names, name)
		acting[name] = Acting(name)
		// Whatever a tool returns from a page must be flagged as not trustworthy.
		if name != ToolClose && name != ToolScreenshot && !strings.Contains(desc, "not instructions") {
			t.Errorf("%s does not warn that page content is untrusted", name)
		}
	}
	sort.Strings(names)
	want := []string{ToolBack, ToolClick, ToolClose, ToolFill, ToolOpen, ToolPress, ToolScreenshot, ToolScroll, ToolSnapshot}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("tools: %v", names)
	}
	for name, a := range acting {
		should := name == ToolOpen || name == ToolClick || name == ToolFill || name == ToolPress
		if a != should {
			t.Errorf("%s: Acting=%v", name, a)
		}
	}
}

func TestClickAndFillMustNameTheirTarget(t *testing.T) {
	c := New(Config{})
	ts, _ := Tools(c)
	for _, bt := range ts {
		name, _, schema := infoOf(t, bt)
		if name != ToolClick && name != ToolFill {
			continue
		}
		if !strings.Contains(schema, `"required":[`) || !strings.Contains(schema, `"name"`) {
			t.Errorf("%s: name is not part of the schema: %s", name, schema)
		}
		inv := bt.(tool.InvokableTool)
		// An empty name is refused before any browser is involved.
		_, err := inv.InvokableRun(context.Background(), `{"ref":1,"name":"  ","text":"x"}`)
		if err == nil || !strings.Contains(err.Error(), "name is required") {
			t.Errorf("%s accepted an empty name: %v", name, err)
		}
	}
}

func TestToolsDoNotStartABrowserToLookAtAPage(t *testing.T) {
	c := New(Config{BrowserPath: "does-not-exist"})
	ts, _ := Tools(c)
	for _, bt := range ts {
		name, _, _ := infoOf(t, bt)
		args := `{"ref":1,"name":"x","text":"x","key":"Enter","direction":"down"}`
		switch name {
		case ToolOpen:
			continue
		case ToolClose:
			continue
		}
		_, err := bt.(tool.InvokableTool).InvokableRun(context.Background(), args)
		if err == nil || !strings.Contains(err.Error(), "browser_open first") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestToolOutputMarksPageContentUntrusted(t *testing.T) {
	c := newTestController(t)
	srv, _ := demoServer(t)
	ts, _ := Tools(c)
	byName := map[string]tool.InvokableTool{}
	for _, bt := range ts {
		name, _, _ := infoOf(t, bt)
		byName[name] = bt.(tool.InvokableTool)
	}
	raw, err := byName[ToolOpen].InvokableRun(opCtx(t), `{"url":"`+srv.URL+`/"}`)
	if err != nil {
		t.Fatal(err)
	}
	var r PageResult
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	if r.Status != "ok" || !r.Untrusted || r.Notice == "" || !strings.Contains(r.Page, `button "Greet"`) || r.HTTPStatus != 200 {
		t.Fatalf("%s", raw)
	}
	raw, err = byName[ToolScreenshot].InvokableRun(opCtx(t), `{}`)
	if err != nil || !strings.Contains(raw, "screenshot_path") {
		t.Fatalf("%v %s", err, raw)
	}
}
