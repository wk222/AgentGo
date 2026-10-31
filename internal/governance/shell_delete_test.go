package governance

import (
	"encoding/json"
	"strings"
	"testing"
)

func bashDecision(t *testing.T, command string) *ToolControlDecision {
	t.Helper()
	args, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	return BashPatternStage{}.Evaluate(ToolPolicyContext{ToolName: "execute_bash", Arguments: string(args)})
}

func TestRecursiveDeleteOfProtectedPlacesIsRefused(t *testing.T) {
	for _, cmd := range []string{
		`rd /s /q C:\`,
		`rmdir /s /q "C:\Users"`,
		`del /s /q C:\*`,
		`del /s /q D:\*.*`,
		`Remove-Item -Recurse -Force C:\Users\user`,
		`Remove-Item C:\ -Recurse`,
		`Remove-Item -Path 'D:\' -Rec -Fo`,
		`rm -rf ~`,
		`rm -rf $HOME`,
		`rm -rf /*`,
		`rm -fr /etc`,
		`rm -Rf /home/bob`,
		`rm -rf %USERPROFILE%`,
		`rd /s /q %SystemRoot%`,
		`rd /s /q C:\Windows\System32`,
		`rd /s /q "C:\Program Files (x86)"`,
		`cmd /c "rd /s /q D:\"`,
		`powershell -Command "Remove-Item -Recurse -Force $env:USERPROFILE"`,
		`bash -c 'rm -rf ~'`,
		`echo cleaning && sudo rm -rf ~`,
		`RM -RF C:\`,
		`find / -delete`,
		`xargs rm -rf $HOME`,
		`sudo -n rm -fr /usr`,
	} {
		d := bashDecision(t, cmd)
		if d == nil || d.Allowed {
			t.Errorf("not refused: %s -> %+v", cmd, d)
		}
	}
}

func TestOrdinaryDeletesAreAllowedButNamedForTheApprover(t *testing.T) {
	for _, cmd := range []string{
		`rm -rf build`,
		`rm -rf ./node_modules`,
		`rm -rf *`, // the current folder's contents, not the root
		`del /q dist\*.tmp`,
		`del report.bak`,
		`Remove-Item .\out -Recurse -Force`,
		`rd /s /q C:\work\tmp`,
		`rmdir /s /q build && mkdir build`,
		`find . -name "*.o" -delete`,
	} {
		d := bashDecision(t, cmd)
		if d == nil || !d.Allowed || !d.RequiresApproval || !strings.Contains(d.Reason, "删除") {
			t.Errorf("must be allowed but flagged for approval: %s -> %+v", cmd, d)
			continue
		}
		hasTag := false
		for _, tag := range d.ControlTags {
			hasTag = hasTag || tag == "shell-delete"
		}
		if !hasTag {
			t.Errorf("missing shell-delete tag: %s -> %v", cmd, d.ControlTags)
		}
	}
	d := bashDecision(t, `rm -rf build && rd /s /q out`)
	if d == nil || !strings.Contains(d.Reason, "build") || !strings.Contains(d.Reason, "out") {
		t.Errorf("the prompt must name every target: %+v", d)
	}
}

func TestCommandsThatOnlyMentionDeletionAreNotTreatedAsDeletes(t *testing.T) {
	for _, cmd := range []string{
		`dir C:\`,
		`git rm -r --cached build`,
		`git clean -n`,
		`npm run rm-all`,
		`echo remove me`,
		`type notes.txt`,
		`grep -r "rm -rf" docs`,
		`ls -la /home`,
		`cat /etc/hosts`,
		`Get-ChildItem -Recurse C:\work`,
		`copy a.txt C:\backup\`,
		``,
	} {
		if d := bashDecision(t, cmd); d != nil {
			t.Errorf("a command with no delete was flagged: %q -> %+v", cmd, d)
		}
	}
}

func TestProtectedTargetClassification(t *testing.T) {
	cases := []struct {
		in        string
		protected bool
	}{
		{`C:\`, true}, {`c:`, true}, {`C:/`, true}, {`C:\*`, true}, {`C:\*.*`, true}, {`d:\\`, true},
		{`C:\Users`, true}, {`C:\Users\user`, true}, {`C:\Users\user\`, true}, {`C:/Users/user`, true},
		{`C:\Windows`, true}, {`C:\Program Files`, true}, {`%userprofile%`, true}, {`$env:userprofile`, true},
		{`~`, true}, {`$HOME`, true}, {`/`, true}, {`/*`, true}, {`/home/bob`, true}, {`/etc`, true},
		{`C:\Users\user\Documents`, false}, {`C:\work`, false}, {`C:\Windows.old2`, false}, {`D:\data\old`, false},
		{`build`, false}, {`.`, false}, {`./*`, false}, {`*`, false}, {`..\x`, false}, {`/tmp/x`, false}, {`/home/bob/project`, false},
	}
	for _, c := range cases {
		if got, _ := protectedTarget(c.in); got != c.protected {
			t.Errorf("protectedTarget(%q) = %v, want %v", c.in, got, c.protected)
		}
	}
}

func TestRecursiveFlagDetection(t *testing.T) {
	yes := []string{"-r", "-R", "-rf", "-fr", "-Rf", "--recursive", "-Recurse", "-rec", "/s", "/S"}
	no := []string{"-f", "-force", "-Force", "/q", "-v", "-fo", "-i"}
	for _, f := range yes {
		if !isRecursiveFlag(f) {
			t.Errorf("%s should be recursive", f)
		}
	}
	for _, f := range no {
		if isRecursiveFlag(f) {
			t.Errorf("%s should not be recursive", f)
		}
	}
}

func TestNonRecursiveDeleteOfAProtectedFolderNeedsAWildcardToBeRefused(t *testing.T) {
	// "del C:\Users" on a folder only removes files directly in it and prompts;
	// the wildcard form removes them all.
	if d := bashDecision(t, `del C:\Users`); d == nil || !d.Allowed {
		t.Errorf("plain del of a folder name is not a recursive wipe: %+v", d)
	}
	if d := bashDecision(t, `del /q C:\*`); d == nil || d.Allowed {
		t.Errorf("wildcard delete at a drive root must be refused: %+v", d)
	}
}

func TestOtherToolsAreNotInspected(t *testing.T) {
	d := BashPatternStage{}.Evaluate(ToolPolicyContext{ToolName: "code_write", Arguments: `{"command":"rd /s /q C:\\"}`})
	if d != nil {
		t.Errorf("only the shell tool is read for deletes: %+v", d)
	}
}
