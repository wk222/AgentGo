package bridge

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeUploadNameKeepsOnlyAPlainFileName(t *testing.T) {
	for in, want := range map[string]string{
		"全光提速实时通报2610(1)(1).xlsx": "全光提速实时通报2610(1)(1).xlsx",
		`C:\fakepath\月报.xlsx`:     "月报.xlsx",
		"../../etc/passwd":        "passwd",
		`a<b>c:d"e|f?g*h.txt`:     "a_b_c_d_e_f_g_h.txt",
		"  报告 .  ":                "报告",
		"":                        "file",
		"..":                      "file",
		"CON.txt":                 "_CON.txt",
	} {
		if got := safeUploadName(in); got != want {
			t.Errorf("safeUploadName(%q) = %q, want %q", in, got, want)
		}
	}
	long := safeUploadName(strings.Repeat("长", 300) + ".xlsx")
	if len([]rune(long)) > 120 || !strings.HasSuffix(long, ".xlsx") {
		t.Errorf("a very long name must be shortened but keep its extension: %d runes %q", len([]rune(long)), long[len(long)-8:])
	}
}

func TestStoreImportedFileKeepsDifferentContentApart(t *testing.T) {
	root := t.TempDir()
	rel, reused, err := storeImportedFile(root, "通报.xlsx", []byte("v1"))
	if err != nil || reused || rel != "uploads/通报.xlsx" {
		t.Fatalf("first copy: %q reused=%v err=%v", rel, reused, err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))); string(got) != "v1" {
		t.Fatalf("content = %q", got)
	}
	// The same file dropped again is not duplicated.
	if rel2, reused, _ := storeImportedFile(root, "通报.xlsx", []byte("v1")); rel2 != rel || !reused {
		t.Errorf("same bytes must reuse the copy: %q reused=%v", rel2, reused)
	}
	// A different file with the same name never overwrites the first one.
	rel3, reused, err := storeImportedFile(root, "通报.xlsx", []byte("v2"))
	if err != nil || reused || rel3 != "uploads/通报 (2).xlsx" {
		t.Fatalf("second version: %q reused=%v err=%v", rel3, reused, err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "uploads", "通报.xlsx")); string(got) != "v1" {
		t.Error("the first copy must stay untouched")
	}
	// A name that tries to climb out of uploads stays inside it.
	rel4, _, err := storeImportedFile(root, `..\..\evil.txt`, []byte("x"))
	if err != nil || rel4 != "uploads/evil.txt" {
		t.Errorf("traversal name: %q err=%v", rel4, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "evil.txt")); err == nil {
		t.Error("nothing may be written outside uploads")
	}
}

func TestImportChatFileNeedsAWorkspaceAndABoundedFile(t *testing.T) {
	root := t.TempDir()
	rt, err := newRuntimeAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	svc := &AppService{rt: rt}
	data := base64.StdEncoding.EncodeToString([]byte("hello"))

	// A fresh runtime already has a default workspace (the working directory), so
	// always move to a temp one before importing: never write into the repo.
	if _, err := rt.SetWorkspaceRoot(root); err != nil {
		t.Fatal(err)
	}
	res := svc.ImportChatFile("a.txt", data)
	if res["success"] != true || res["path"] != "uploads/a.txt" {
		t.Fatalf("import: %v", res)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "uploads", "a.txt")); string(got) != "hello" {
		t.Errorf("content = %q", got)
	}
	if res := svc.ImportChatFile("a.txt", "###"); res["success"] != false {
		t.Errorf("bad base64 must fail: %v", res)
	}
	big := base64.StdEncoding.EncodeToString(make([]byte, maxImportedFileBytes+1))
	if res := svc.ImportChatFile("big.bin", big); res["success"] != false || !strings.Contains(res["error"].(string), "MB") {
		t.Errorf("an oversized file must be refused with a way out: %v", res)
	}
}
