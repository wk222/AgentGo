"""A/B: the same coding tasks through two agentgo-cli builds, real model.

  python scripts/ab_codetools.py --a D:/tmp/agentgo-cli-crush.exe --b D:/tmp/agentgo-cli.exe --runs 2

Each run gets a fresh copy of a small Go fixture. A task passes when its checker
(on the files / answer text) says so; tool names and counts are recorded so
differences in *how* the model works show up too. Nothing here is a unit test:
it spends real tokens, so it is run by hand when the tool layer changes.
"""
import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path

GOPLS_DIR = os.path.expanduser("~/go/bin")

CALC = """package calc

// Add returns a+b.
func Add(a, b int) int { return a + b }

// Sum adds every element of xs.
func Sum(xs []int) int {
	total := 0
	for i := 0; i < len(xs)-1; i++ {
		total += xs[i]
	}
	return total
}
"""
CALC_TEST = """package calc

import "testing"

func TestSum(t *testing.T) {
	if got := Sum([]int{1, 2, 3}); got != 6 {
		t.Fatalf("Sum = %d, want 6", got)
	}
	if got := Add(2, 3); got != 5 {
		t.Fatalf("Add = %d, want 5", got)
	}
}
"""
MAIN = """package main

import (
	"fmt"

	"fixture/calc"
)

func main() {
	fmt.Println(calc.Add(1, 2))
	fmt.Println(calc.Add(3, 4))
	fmt.Println(calc.Sum([]int{1, 2, 3}))
}
"""
FILES = {
    "go.mod": "module fixture\n\ngo 1.21\n",
    "main.go": MAIN,
    "config.go": "package main\n\n// MaxItems caps the list.\nconst MaxItems = 10\n",
    "calc/calc.go": CALC,
    "calc/calc_test.go": CALC_TEST,
    "broken/broken.go": "package broken\n\nfunc Bad() int {\n\treturn undefinedThing\n}\n",
    "pkg/a/a.go": "package a\n\n// Foo greets.\nfunc Foo() string { return \"foo\" }\n",
    "pkg/b/b.go": 'package b\n\nimport "fixture/pkg/a"\n\nfunc Use() string { return a.Foo() + a.Foo() }\n',
    "notes/big.txt": "".join(
        f"line {i}: {'the secret word is pineapple' if i == 137 else 'nothing to see here'}\n" for i in range(1, 201)
    ),
}


def make_fixture(root: Path):
    for rel, body in FILES.items():
        p = root / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_bytes(body.encode())


def line_of(text: str, needle: str) -> int:
    return next(i for i, l in enumerate(text.splitlines(), 1) if needle in l)


def sh(root, *cmd):
    r = subprocess.run(cmd, cwd=root, capture_output=True, text=True, timeout=120)
    return r.returncode == 0, (r.stdout + r.stderr)


# --- tasks: (name, prompt, checker(root, answer, tools) -> bool) -----------------
def t_find_def(root, ans, tools):
    return "calc.go" in ans and str(line_of(CALC, "func Sum")) in ans


def t_edit_const(root, ans, tools):
    return "MaxItems = 20" in (root / "config.go").read_text()


def t_fix_bug(root, ans, tools):
    same = (root / "calc/calc_test.go").read_text() == CALC_TEST
    ok, _ = sh(root, "go", "test", "./calc")
    return same and ok


def t_lsp_diag(root, ans, tools):
    return "undefinedThing" in ans


def t_lsp_refs(root, ans, tools):
    return bool(re.search(r"\b(3|three)\b", ans))


def t_view_line(root, ans, tools):
    return "pineapple" in ans


def t_create(root, ans, tools):
    p = root / "hello/hello.go"
    if not p.exists():
        return False
    ok, _ = sh(root, "go", "build", "-o", os.devnull, "./hello")
    return ok and "package main" in p.read_text()


def t_rename(root, ans, tools):
    a = (root / "pkg/a/a.go").read_text()
    b = (root / "pkg/b/b.go").read_text()
    ok, _ = sh(root, "go", "build", "./pkg/...")
    return "Foo" not in a + b and "Bar" in a and "Bar" in b and ok


def t_list(root, ans, tools):
    return "a.go" in ans and "b.go" in ans


TASKS = [
    ("find_def", "In this repo, which file defines the function Sum and on what line? Answer with the file path and the line number.", t_find_def),
    ("edit_const", "Change MaxItems in config.go from 10 to 20.", t_edit_const),
    ("fix_bug", "`go test ./calc` currently fails. Find and fix the bug in calc/calc.go (do not touch the test). Verify by running the test.", t_fix_bug),
    ("lsp_diag", "Use the LSP diagnostics tool on broken/broken.go and tell me the name of the undefined identifier.", t_lsp_diag),
    ("lsp_refs", "Using LSP references, how many places call calc.Add (not counting its definition)? Answer with a number.", t_lsp_refs),
    ("view_line", "What does line 137 of notes/big.txt say? Quote it.", t_view_line),
    ("create_file", "Create hello/hello.go: a main package that prints hi.", t_create),
    ("rename", "Rename the function Foo to Bar in pkg/a/a.go and update every caller under pkg/.", t_rename),
    ("list_go", "List every .go file under pkg/ (use a glob or ls tool, not the shell).", t_list),
]


def run_once(exe, prompt, root, timeout):
    env = dict(os.environ)
    env["PATH"] = GOPLS_DIR + os.pathsep + env["PATH"]
    t0 = time.time()
    try:
        p = subprocess.run([exe, "run", "-yolo", "-json", "-w", str(root), prompt],
                           capture_output=True, text=True, timeout=timeout, env=env, encoding="utf-8", errors="replace",
                           cwd=root)  # Crush's grep runs from the process cwd; give it the workspace so A is fair
        out, rc = p.stdout, p.returncode
    except subprocess.TimeoutExpired as e:
        out, rc = (e.stdout or b"").decode("utf-8", "replace") if isinstance(e.stdout, bytes) else (e.stdout or ""), -1
    tools, deltas, final, err = [], [], [], ""
    for line in out.splitlines():
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            ev = json.loads(line)
        except ValueError:
            continue
        pl, ty = ev.get("payload") or {}, ev.get("type")
        if ty == "tool_call":
            tools.append(pl.get("name", "?"))
        elif ty == "token":
            deltas.append(pl.get("delta", ""))
        elif ty == "done":
            err = pl.get("error", "") or err
            for m in pl.get("messages") or []:
                if m.get("role") == "assistant" and m.get("type") in ("", "text", None):
                    final.append(m.get("content", ""))
    ans = "\n".join(final) or "".join(deltas)
    return ans, tools, err, rc, time.time() - t0


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--a", required=True, help="reference build (label A)")
    ap.add_argument("--b", required=True, help="build under test (label B)")
    ap.add_argument("--runs", type=int, default=2)
    ap.add_argument("--only", default="")
    ap.add_argument("--timeout", type=int, default=300)
    ap.add_argument("--tmp", default="D:/tmp/ab-fixture")
    ap.add_argument("--out", default="")
    a = ap.parse_args()

    results = []
    names = set(filter(None, a.only.split(",")))
    for name, prompt, check in TASKS:
        if names and name not in names:
            continue
        for run in range(a.runs):
            for label, exe in (("A", a.a), ("B", a.b)):
                root = Path(a.tmp) / f"{name}-{label}-{run}"
                shutil.rmtree(root, ignore_errors=True)
                make_fixture(root)
                ans, tools, err, rc, secs = run_once(exe, prompt, root, a.timeout)
                try:
                    ok = bool(check(root, ans, tools))
                except Exception as e:  # a checker bug must not hide as a pass
                    ok, err = False, f"checker: {e}"
                results.append(dict(task=name, run=run, build=label, ok=ok, tools=tools, secs=round(secs, 1), rc=rc, err=err, answer=ans[:300]))
                print(f"{name:12} #{run} {label} {'PASS' if ok else 'FAIL'} {secs:5.1f}s tools={len(tools)} {','.join(sorted(set(tools)))}"
                      + (f"  err={err[:120]}" if err else ""), flush=True)

    print("\n=== summary (pass / runs, mean tool calls, mean secs) ===")
    for name, _, _ in TASKS:
        row = [r for r in results if r["task"] == name]
        if not row:
            continue
        cells = []
        for label in "AB":
            rs = [r for r in row if r["build"] == label]
            cells.append(f"{label}: {sum(r['ok'] for r in rs)}/{len(rs)} tools={sum(len(r['tools']) for r in rs)/len(rs):.1f} {sum(r['secs'] for r in rs)/len(rs):.0f}s")
        print(f"{name:12} " + "   ".join(cells))
    for label in "AB":
        rs = [r for r in results if r["build"] == label]
        print(f"TOTAL {label}: {sum(r['ok'] for r in rs)}/{len(rs)}")
    if a.out:
        Path(a.out).write_text(json.dumps(results, ensure_ascii=False, indent=1), encoding="utf-8")


if __name__ == "__main__":
    sys.exit(main())
