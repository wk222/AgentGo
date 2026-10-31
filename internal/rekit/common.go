// Package rekit is AgentGo's own reverse-engineering toolkit, exposed as a stdio MCP
// server (cmd/agentgo-re).
//
// Design borrowed from the four reference projects (see docs/reverse-kit.md):
//   - rea:         every answer separates observation / derivation / inference / unknown,
//     is paged, and carries its limits; large output is spilled to a file.
//   - recurse:     few, backend-neutral tools; persistent per-case memory.
//   - reagent:     a finding is only "accepted" when a gate (objective check) agrees.
//   - open-reverselab: directory-is-convention (samples/exports/reports) + env probe.
package rekit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"agentgo/internal/config"
)

// Epistemic levels (rea's vocabulary). Tools return facts as "observation"; anything the
// agent concludes must be recorded as derivation / inference / unknown in the ledger.
const (
	KindObservation = "observation"
	KindDerivation  = "derivation"
	KindInference   = "inference"
	KindUnknown     = "unknown"
)

// Reply is the common envelope of every tool answer.
type Reply struct {
	Tool   string         `json:"tool"`
	OK     bool           `json:"ok"`
	Facts  any            `json:"facts,omitempty"`  // observations only
	Limits []string       `json:"limits,omitempty"` // what this answer does NOT prove
	Next   []string       `json:"next,omitempty"`   // suggested follow-up probes
	Paging *Paging        `json:"paging,omitempty"`
	Spill  string         `json:"spill_path,omitempty"`
	Extra  map[string]any `json:"extra,omitempty"`
}

// Paging describes a truncated list answer.
type Paging struct {
	Total      int  `json:"total"`
	Offset     int  `json:"offset"`
	Returned   int  `json:"returned"`
	HasMore    bool `json:"has_more"`
	NextOffset int  `json:"next_offset,omitempty"`
}

func page(total, offset, limit int) (lo, hi int, p *Paging) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 50
	}
	if offset > total {
		offset = total
	}
	hi = offset + limit
	if hi > total {
		hi = total
	}
	p = &Paging{Total: total, Offset: offset, Returned: hi - offset, HasMore: hi < total}
	if p.HasMore {
		p.NextOffset = hi
	}
	return offset, hi, p
}

// JSON renders a Reply compactly enough for an LLM (indent keeps it readable).
func (r Reply) JSON() string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // keep <outdir>, & and > readable for the model
	enc.SetIndent("", " ")
	if err := enc.Encode(r); err != nil {
		return fmt.Sprintf(`{"tool":%q,"ok":false,"error":%q}`, r.Tool, err.Error())
	}
	return strings.TrimRight(buf.String(), "\n")
}

// Home returns the directory that stores cases, ledgers and spill files.
// Priority: explicit flag value > AGENTGO_RE_HOME > ./.agentgo-re (never the C: profile).
func Home(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if v := config.ReHome.Get(); v != "" {
		return v
	}
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	return filepath.Join(wd, ".agentgo-re")
}

var caseNameRe = regexp.MustCompile(`^[A-Za-z0-9_.\-\p{Han}]{1,64}$`)

// ValidCase reports whether name is safe to use as a directory name.
func ValidCase(name string) bool {
	return caseNameRe.MatchString(name) && !strings.Contains(name, "..")
}

func caseDir(home, name string) (string, error) {
	if name == "" {
		name = "default"
	}
	if !ValidCase(name) {
		return "", fmt.Errorf("invalid case name %q (allowed: letters, digits, _ . - and Chinese, max 64)", name)
	}
	d := filepath.Join(home, "cases", name)
	return d, os.MkdirAll(d, 0o755)
}

func shaHex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func hexPreview(b []byte, n int) string {
	if len(b) > n {
		b = b[:n]
	}
	return hex.EncodeToString(b)
}

func le16(b []byte) uint16 { return uint16(b[0]) | uint16(b[1])<<8 }
func le32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}
func be32(b []byte) uint32 {
	return uint32(b[3]) | uint32(b[2])<<8 | uint32(b[1])<<16 | uint32(b[0])<<24
}

func readHeadAt(path string, off int64, n int) ([]byte, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	if off < 0 || off >= st.Size() {
		return nil, st.Size(), fmt.Errorf("offset %d outside file of %d bytes", off, st.Size())
	}
	buf := make([]byte, n)
	m, _ := f.ReadAt(buf, off)
	return buf[:m], st.Size(), nil
}

func readHead(path string, n int) ([]byte, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	if st.IsDir() {
		return nil, 0, fmt.Errorf("%s is a directory", path)
	}
	buf := make([]byte, n)
	m, _ := f.Read(buf)
	return buf[:m], st.Size(), nil
}
