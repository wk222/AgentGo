package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func changesOf(t *testing.T, dir, path string) getWorkspaceChangesOutput {
	t.Helper()
	out, err := getWorkspaceChanges(context.Background(), dir, getWorkspaceChangesInput{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestUntrackedFilesComeWithNumberedLines(t *testing.T) {
	dir := gitRepo(t)
	writeFile(t, dir, "new.go", "package main\r\n\r\nfunc main() {}\r\n") // CRLF, as written on Windows
	writeFile(t, dir, "说明.txt", "第一行\n第二行\n")

	out := changesOf(t, dir, "")
	for _, want := range []string{
		"=== new file: new.go (3 lines) ===",
		"1| package main",
		"3| func main() {}",
		"=== new file: 说明.txt (2 lines) ===",
		"2| 第二行",
	} {
		if !strings.Contains(out.Untracked, want) {
			t.Errorf("untracked lacks %q:\n%s", want, out.Untracked)
		}
	}
	if strings.Contains(out.Untracked, "\r") {
		t.Error("carriage returns must not reach the model")
	}
	if !strings.Contains(out.Status, "?? new.go") {
		t.Errorf("status still names the new file:\n%s", out.Status)
	}
	if strings.Contains(out.Diff, "package main") {
		t.Errorf("new files stay out of the diff:\n%s", out.Diff)
	}
}

func TestUntrackedIsEmptyWhenThereIsNothingNew(t *testing.T) {
	dir := gitRepo(t)
	writeFile(t, dir, "a.txt", "one\ntwo\nthree\n")
	if got := changesOf(t, dir, "").Untracked; got != "" {
		t.Errorf("no new files, but untracked = %q", got)
	}
}

func TestUntrackedHonoursGitignoreAndThePathFilter(t *testing.T) {
	dir := gitRepo(t)
	writeFile(t, dir, ".gitignore", "build.log\n")
	writeFile(t, dir, "build.log", "noise\n")
	writeFile(t, dir, "x.txt", "wanted\n")
	writeFile(t, dir, "y.txt", "other\n")

	all := changesOf(t, dir, "").Untracked
	// .gitignore is itself a new file and names build.log, so look for the file's own header
	if strings.Contains(all, "noise") || strings.Contains(all, "=== new file: build.log") {
		t.Errorf("an ignored file was shown:\n%s", all)
	}
	if !strings.Contains(all, "wanted") || !strings.Contains(all, "other") {
		t.Errorf("both new files should be shown:\n%s", all)
	}
	one := changesOf(t, dir, "x.txt").Untracked
	if !strings.Contains(one, "wanted") || strings.Contains(one, "other") {
		t.Errorf("the path filter must narrow the new files too:\n%s", one)
	}
}

func TestUntrackedBinarySecretAndOversizedFilesSayWhy(t *testing.T) {
	dir := gitRepo(t)
	writeFile(t, dir, "logo.dat", "GIF89a\x00\x01\x02binary")
	writeFile(t, dir, ".env", "API_KEY=do-not-send-me\n")
	writeFile(t, dir, "server.pem", "-----BEGIN PRIVATE KEY-----\nabc\n")
	writeFile(t, dir, ".env.example", "API_KEY=changeme\n")
	var big strings.Builder
	for i := 1; big.Len() < 3*maxUntrackedFileBytes; i++ {
		fmt.Fprintf(&big, "这是第%d行，用来把文件撑过单个文件的上限。\n", i)
	}
	writeFile(t, dir, "big.txt", big.String())

	got := changesOf(t, dir, "").Untracked
	for _, want := range []string{
		"=== new file: logo.dat — not shown (binary file,",
		"=== new file: .env — not shown (the name looks like a secret",
		"=== new file: server.pem — not shown (the name looks like a secret",
		"API_KEY=changeme", // a template is not a secret
		"=== new file: big.txt (first ",
		"… cut here; read the rest with the file tools …",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("untracked lacks %q:\n%.600s", want, got)
		}
	}
	for _, leaked := range []string{"do-not-send-me", "BEGIN PRIVATE KEY", "GIF89a"} {
		if strings.Contains(got, leaked) {
			t.Errorf("%q must not be shown", leaked)
		}
	}
	if !utf8.ValidString(got) {
		t.Error("cutting a file split a UTF-8 character")
	}
	// the cut file ends on a whole line: the last numbered line is complete
	const marker = "… cut here; read the rest with the file tools …"
	if i := strings.Index(got, marker); i >= 0 {
		before := strings.TrimSpace(got[:i])
		if !strings.HasSuffix(before, "上限。") {
			t.Errorf("the cut should fall at the end of a line, got …%q", before[max(0, len(before)-40):])
		}
	}
}

func TestUntrackedStopsAtTheFileAndByteBudgets(t *testing.T) {
	dir := gitRepo(t)
	for i := 0; i < maxUntrackedFiles+5; i++ {
		writeFile(t, dir, fmt.Sprintf("f%02d.txt", i), "x\n")
	}
	got := changesOf(t, dir, "").Untracked
	if n := strings.Count(got, "=== new file:"); n != maxUntrackedFiles {
		t.Errorf("shown %d files, want %d", n, maxUntrackedFiles)
	}
	if !strings.Contains(got, "[5 more new files not shown: f20.txt, f21.txt") {
		t.Errorf("the rest should be named:\n%s", got[len(got)-300:])
	}

	dir = gitRepo(t)
	chunk := strings.Repeat("0123456789abcdef", 1024) + "\n" // about 16 KiB a file
	for i := 0; i < 8; i++ {
		writeFile(t, dir, fmt.Sprintf("c%d.txt", i), chunk)
	}
	got = changesOf(t, dir, "").Untracked
	if len(got) > maxUntrackedTotalBytes+len(chunk)+400 {
		t.Errorf("untracked is %d bytes, far over the %d budget", len(got), maxUntrackedTotalBytes)
	}
	if !strings.Contains(got, "more new files not shown: c") {
		t.Errorf("files past the byte budget should be listed:\n%.300s", got[len(got)-300:])
	}
}

func TestLooksSecret(t *testing.T) {
	for name, want := range map[string]bool{
		".env": true, "app/.env.local": true, ".env.example": false, "id_rsa": true, "id_rsa.pub": false,
		"deploy/key.PEM": true, "credentials.json": true, "secrets.yaml": true, "secrets": true, "secrets_test.go": false,
		"main.go": false, "notes/keys.md": false, "cert.p12": true, ".npmrc": true,
	} {
		if got := looksSecret(name); got != want {
			t.Errorf("looksSecret(%q) = %v, want %v", name, got, want)
		}
	}
}
