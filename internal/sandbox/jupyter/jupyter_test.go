package jupyter

import (
	"strings"
	"testing"
)

func TestJupyterExecution(t *testing.T) {
	if err := checkJupyterGateway(); err != nil {
		t.Skip("Jupyter kernel gateway is not installed locally, skipping integration test.")
		return
	}

	// Start gateway on port 18888
	je, err := NewJupyterExecutor("python3", 18888)
	if err != nil {
		t.Fatalf("failed starting executor: %v", err)
	}
	defer je.Close()

	// First execution - declare variable a
	out1, err := je.Execute("a = 42\nprint('hello from jupyter')")
	if err != nil {
		t.Fatalf("first run error: %v", err)
	}
	if !strings.Contains(out1, "hello from jupyter") {
		t.Errorf("unexpected first run output: %q", out1)
	}

	// Second execution - verify state of variable a is preserved
	out2, err := je.Execute("print(a * 2)")
	if err != nil {
		t.Fatalf("second run error: %v", err)
	}
	if !strings.Contains(out2, "84") {
		t.Errorf("state preservation failed, expected 84, got %q", out2)
	}

	// Third execution - expect math division error
	_, err3 := je.Execute("1 / 0")
	if err3 == nil {
		t.Errorf("expected ZeroDivisionError, got nil error")
	} else {
		t.Logf("caught error successfully: %v", err3)
	}
}
