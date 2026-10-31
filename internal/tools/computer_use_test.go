package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agentgo/internal/computeruse"
	"agentgo/internal/config"
)

func TestComputerUseToolIsOptIn(t *testing.T) {
	config.ComputerUse.Set(false)
	r := NewRegistry()
	if err := RegisterComputerUseTool(r); err != nil {
		t.Fatal(err)
	}
	if _, ok := lookupToolInfo(t, r, "computer_use"); ok {
		t.Fatal("computer_use must not be registered unless AGENTGO_COMPUTER_USE is on")
	}
}

func TestComputerUseToolSchema(t *testing.T) {
	if !computeruse.Supported() {
		t.Skip("computer use needs Windows")
	}
	config.ComputerUse.Set(true)
	defer config.ComputerUse.Set(false)
	r := NewRegistry()
	if err := RegisterComputerUseTool(r); err != nil {
		t.Fatal(err)
	}
	info, ok := lookupToolInfo(t, r, "computer_use")
	if !ok {
		t.Fatal("computer_use should be registered when the flag is on")
	}
	js, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatalf("schema generation (recursive steps field): %v", err)
	}
	raw, err := json.Marshal(js)
	if err != nil {
		t.Fatalf("schema is not serializable: %v", err)
	}
	s := string(raw)
	for _, want := range []string{`"action"`, `"zoom_id"`, `"steps"`, `"allow_destructive"`, `"path"`} {
		if !strings.Contains(s, want) {
			t.Errorf("schema is missing %s: %s", want, s)
		}
	}
	// Only action is mandatory at the top level; what else is needed depends on it.
	if !strings.HasSuffix(s, `"required":["action"],"type":"object"}`) {
		t.Errorf("top level must require only action: ...%s", s[max(0, len(s)-120):])
	}
}

func TestComputerUseResultCarriesImageAndErrorsAsData(t *testing.T) {
	ok := computerUseToolResult(&computeruse.Result{Text: "hi", PNG: []byte("\x89PNG\r\n\x1a\nxx")}, nil)
	if len(ok.Parts) != 2 || ok.Parts[1].Image == nil || ok.Parts[1].Image.Base64Data == nil || ok.Parts[1].Image.MIMEType != "image/png" {
		t.Fatalf("a screenshot result must carry an image part: %#v", ok.Parts)
	}
	bad := computerUseToolResult(nil, computeruse.NewError(computeruse.CodeUserAborted, "stopped"))
	if len(bad.Parts) != 1 || !strings.Contains(bad.Parts[0].Text, `"escalation":"stop"`) {
		t.Fatalf("errors must reach the model as data with their escalation: %#v", bad.Parts)
	}
}

func lookupToolInfo(t *testing.T, r *Registry, name string) (*schemaToolInfo, bool) {
	t.Helper()
	for _, bt := range r.ToolsForADK() {
		info, err := bt.Info(context.Background())
		if err == nil && info != nil && info.Name == name {
			return info, true
		}
	}
	return nil, false
}
