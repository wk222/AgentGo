package tools

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// maxUntrackedFiles is how many new files are shown with their content.
	maxUntrackedFiles = 20
	// maxUntrackedFileBytes bounds one file's content; a longer file is cut at a line.
	maxUntrackedFileBytes = 24 * 1024
	// maxUntrackedTotalBytes bounds all the new files shown together.
	maxUntrackedTotalBytes = 48 * 1024
	// untrackedSniffBytes is how much of a file is checked for a NUL byte, which text never holds.
	untrackedSniffBytes = 8 * 1024
	// maxUntrackedListed bounds the names listed as "not shown".
	maxUntrackedListed = 20
)

// untrackedContent renders the new files git does not track yet as numbered
// lines. git diff has nothing to say about them, and a reviewer that has to
// fetch each one separately tends to skip some. Line numbers are given so that
// findings can name exact lines. Files that cannot be shown in full (too long,
// binary, named like a secret) say so, and the model reads them itself.
func untrackedContent(root string, names []string) string {
	var b strings.Builder
	var skipped []string
	shown, total := 0, 0
	for _, name := range names {
		if name == "" || strings.HasSuffix(name, "/") { // a nested repository is listed as a directory
			continue
		}
		if shown >= maxUntrackedFiles || total >= maxUntrackedTotalBytes {
			skipped = append(skipped, name)
			continue
		}
		block := renderUntracked(root, name)
		b.WriteString(block)
		total += len(block)
		shown++
	}
	if len(skipped) > 0 {
		listed := skipped
		if len(listed) > maxUntrackedListed {
			listed = listed[:maxUntrackedListed]
		}
		more := ""
		if len(skipped) > len(listed) {
			more = ", …"
		}
		fmt.Fprintf(&b, "[%d more new files not shown: %s%s — read them with the file tools]\n",
			len(skipped), strings.Join(listed, ", "), more)
	}
	return b.String()
}

// renderUntracked is one new file: a header line and its numbered lines, or a
// header line that says why the content is not shown.
func renderUntracked(root, name string) string {
	skip := func(reason string) string {
		return fmt.Sprintf("=== new file: %s — not shown (%s) ===\n", name, reason)
	}
	full := filepath.Join(root, filepath.FromSlash(name))
	info, err := os.Lstat(full)
	switch {
	case err != nil:
		return skip("cannot be read")
	case info.Mode()&os.ModeSymlink != 0:
		return skip("symbolic link")
	case !info.Mode().IsRegular():
		return skip("not a regular file")
	case looksSecret(name):
		return skip("the name looks like a secret or key file; read it only if the review needs it")
	}
	f, err := os.Open(full)
	if err != nil {
		return skip("cannot be read")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxUntrackedFileBytes+1))
	if err != nil {
		return skip("cannot be read")
	}
	if bytes.IndexByte(data[:min(len(data), untrackedSniffBytes)], 0) >= 0 {
		return skip(fmt.Sprintf("binary file, %d bytes", info.Size()))
	}

	text := string(data)
	cut := len(data) > maxUntrackedFileBytes
	if cut {
		text = cutPrefix(text, maxUntrackedFileBytes)
	}
	text = strings.ToValidUTF8(text, "\uFFFD") // sources saved in GBK are common on Chinese systems
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}

	var b strings.Builder
	if cut {
		fmt.Fprintf(&b, "=== new file: %s (first %d lines of %d bytes) ===\n", name, len(lines), info.Size())
	} else {
		fmt.Fprintf(&b, "=== new file: %s (%d lines) ===\n", name, len(lines))
	}
	width := len(strconv.Itoa(len(lines)))
	for i, line := range lines {
		fmt.Fprintf(&b, "%*d| %s\n", width, i+1, line)
	}
	if cut {
		b.WriteString("… cut here; read the rest with the file tools …\n")
	}
	return b.String()
}

// looksSecret reports whether a file name suggests credentials. Such a file is
// not pasted into the model's context on the tool's own initiative.
func looksSecret(name string) bool {
	base := strings.ToLower(path.Base(name))
	switch {
	case strings.HasSuffix(base, ".example"), strings.HasSuffix(base, ".sample"), strings.HasSuffix(base, ".template"), strings.HasSuffix(base, ".pub"):
		return false
	case base == ".env", strings.HasPrefix(base, ".env."), base == ".npmrc", base == ".netrc", base == ".pypirc":
		return true
	case strings.HasPrefix(base, "id_rsa"), strings.HasPrefix(base, "id_ed25519"), strings.HasPrefix(base, "id_ecdsa"):
		return true
	case strings.HasPrefix(base, "credentials"), strings.HasPrefix(base, "secrets."), base == "secrets":
		return true
	}
	for _, ext := range []string{".pem", ".key", ".pfx", ".p12", ".kdbx", ".jks", ".keystore"} {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	return false
}
