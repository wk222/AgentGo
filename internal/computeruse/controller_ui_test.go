package computeruse

import (
	"context"
	"strings"
	"testing"
)

type fakeUI struct {
	nodes   []UINode
	actErr  error
	lastSel UISelector
	lastAct UIAction
	lastVal string
	acted   UINode
}

func (f *fakeUI) Tree(t UITarget, d int) (string, []UINode, error) {
	return "Fake App", f.nodes, nil
}

func (f *fakeUI) Act(t UITarget, d int, sel UISelector, act UIAction, val string) (UINode, string, error) {
	f.lastSel, f.lastAct, f.lastVal = sel, act, val
	passes := []bool{false}
	if sel.needsTwoPasses() {
		passes = []bool{true, false} // exact first, like the real walker
	}
	for _, exact := range passes {
		for _, n := range f.nodes {
			if (sel.ID != nil && n.ID == *sel.ID) || (sel.ID == nil && matchNode(sel, n, exact)) {
				f.acted = n
				if f.actErr != nil {
					return n, "", f.actErr
				}
				return n, "invoked", nil
			}
		}
	}
	return UINode{}, "", NewError(CodeInvalidArguments, "no element")
}

func sampleNodes() []UINode {
	return []UINode{
		{ID: 0, Depth: 0, Role: "Window", Name: "Fake App", Enabled: true, Rect: UIRect{0, 0, 1920, 1080}},
		{ID: 1, Depth: 1, Role: "Button", Name: "Save", AutomationID: "root.toolbar.save", Enabled: true, Rect: UIRect{100, 50, 80, 40}},
		{ID: 2, Depth: 1, Role: "Edit", Name: "Title", Enabled: true, Rect: UIRect{300, 50, 400, 40}},
		{ID: 3, Depth: 1, Role: "Button", Name: "Delete", Enabled: false, Rect: UIRect{800, 50, 80, 40}},
	}
}

func uiCtl(sub Substrate, ui Accessibility) *Controller {
	return New(sub, WithSettle(0), WithAccessibility(ui))
}

func TestUITreeListsElementsAndNeedsNoScreenshot(t *testing.T) {
	c := uiCtl(newFake(1920, 1080), &fakeUI{nodes: sampleNodes()})
	res, err := c.Do(context.Background(), Request{Action: "ui_tree"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.PNG) != 0 {
		t.Fatal("ui_tree should be text only")
	}
	for _, want := range []string{`1 [Button] "Save"`, "id=root.toolbar.save", `3 [Button] "Delete" disabled`, "@screen("} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("missing %q in:\n%s", want, res.Text)
		}
	}
}

func TestUITreeUsesScreenshotPixelsWhenAFrameExists(t *testing.T) {
	c := uiCtl(newFake(2912, 1680), &fakeUI{nodes: []UINode{
		{ID: 0, Role: "Window", Name: "w", Enabled: true},
		{ID: 1, Depth: 1, Role: "Button", Name: "B", Enabled: true, Rect: UIRect{1000, 600, 200, 100}},
	}})
	if _, err := c.Do(context.Background(), Request{Action: "screenshot"}); err != nil {
		t.Fatal(err)
	}
	res, err := c.Do(context.Background(), Request{Action: "ui_tree"})
	if err != nil {
		t.Fatal(err)
	}
	// center (1100,650) on a 2x display -> (550,325) in screenshot pixels
	if !strings.Contains(res.Text, "@(550,325)") {
		t.Fatalf("expected screenshot-pixel center:\n%s", res.Text)
	}
}

func TestUIPressByNameReturnsScreenshotAndRoutesSelector(t *testing.T) {
	ui := &fakeUI{nodes: sampleNodes()}
	c := uiCtl(newFake(1920, 1080), ui)
	res, err := c.Do(context.Background(), Request{Action: "ui_press", Role: "Button", Name: "save"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.PNG) == 0 || !strings.Contains(res.Text, "invoked") {
		t.Fatalf("want a screenshot and the note, got %q png=%d", res.Text, len(res.PNG))
	}
	if ui.lastAct != UIPress || ui.acted.ID != 1 {
		t.Fatalf("wrong action routing: %v %+v", ui.lastAct, ui.acted)
	}
}

func TestUIActionNeedsASelector(t *testing.T) {
	c := uiCtl(newFake(1920, 1080), &fakeUI{nodes: sampleNodes()})
	_, err := c.Do(context.Background(), Request{Action: "ui_press"})
	if codeOf(t, err) != CodeInvalidArguments {
		t.Fatalf("got %v", err)
	}
}

func TestUIPressFallsBackToClickingTheCenter(t *testing.T) {
	sub := newFake(1920, 1080)
	c := uiCtl(sub, &fakeUI{nodes: sampleNodes(), actErr: ErrNoPattern})
	res, err := c.Do(context.Background(), Request{Action: "ui_press", Name: "Save"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "clicked its center") {
		t.Fatalf("got %q", res.Text)
	}
	if !strings.Contains(strings.Join(sub.calls, "|"), "140,70") {
		t.Fatalf("expected a click at the element center (140,70); calls=%v", sub.calls)
	}
}

func TestUIPressFallbackRefusesOffscreenElement(t *testing.T) {
	nodes := []UINode{{ID: 0, Role: "Button", Name: "Far", Enabled: true, Rect: UIRect{5000, 5000, 10, 10}}}
	c := uiCtl(newFake(1920, 1080), &fakeUI{nodes: nodes, actErr: ErrNoPattern})
	_, err := c.Do(context.Background(), Request{Action: "ui_press", Name: "Far"})
	if codeOf(t, err) != CodeInvalidArguments {
		t.Fatalf("got %v", err)
	}
}

func TestUIReadIsTextOnly(t *testing.T) {
	c := uiCtl(newFake(1920, 1080), &fakeUI{nodes: sampleNodes()})
	res, err := c.Do(context.Background(), Request{Action: "ui_read", Role: "Edit"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.PNG) != 0 || !strings.Contains(res.Text, "value:") {
		t.Fatalf("got %q png=%d", res.Text, len(res.PNG))
	}
}

func TestUIActionsHonourStopAndPreflight(t *testing.T) {
	sub := newFake(1920, 1080)
	c := uiCtl(sub, &fakeUI{nodes: sampleNodes()})
	sub.preflight = NewError(CodeSecureDesktop, "uac")
	if _, err := c.Do(context.Background(), Request{Action: "ui_press", Name: "Save"}); codeOf(t, err) != CodeSecureDesktop {
		t.Fatalf("preflight must gate ui_press: %v", err)
	}
	sub.preflight = nil
	c.Stop()
	if _, err := c.Do(context.Background(), Request{Action: "ui_press", Name: "Save"}); codeOf(t, err) != CodeUserAborted {
		t.Fatalf("Stop must gate ui_press: %v", err)
	}
}

func TestUIUnsupportedWithoutAccessibility(t *testing.T) {
	c := New(newFake(1920, 1080), WithSettle(0))
	_, err := c.Do(context.Background(), Request{Action: "ui_tree"})
	if codeOf(t, err) != CodeUnsupported {
		t.Fatalf("got %v", err)
	}
}

func TestUIEmptyTreeExplainsPixelFallback(t *testing.T) {
	c := uiCtl(newFake(1920, 1080), &fakeUI{nodes: []UINode{{ID: 0, Role: "Window", Name: "Game"}}})
	res, err := c.Do(context.Background(), Request{Action: "ui_tree"})
	if err != nil || !strings.Contains(res.Text, "exposes no accessibility tree") {
		t.Fatalf("got %v %v", res, err)
	}
}

func TestMatchNodePrefersExactThenSubstring(t *testing.T) {
	n := UINode{Role: "Button", Name: "Save As...", AutomationID: "a.b.save"}
	if !matchNode(UISelector{Name: "save"}, n, false) {
		t.Fatal("substring should match case-insensitively")
	}
	if matchNode(UISelector{Name: "save"}, n, true) {
		t.Fatal("exact pass must not substring-match")
	}
	if !matchNode(UISelector{AutomationID: "save"}, n, false) {
		t.Fatal("automation id should match on the dotted tail")
	}
	if matchNode(UISelector{Role: "Edit"}, n, false) {
		t.Fatal("role mismatch")
	}
}
