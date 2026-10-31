package rekit

import (
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"math"
	"os"
	"strings"
)

// Triage is the fact sheet for one file: identity, entropy and format specifics.
type Triage struct {
	Path      string         `json:"path"`
	Size      int64          `json:"size"`
	Hashed    int64          `json:"hashed_bytes"`
	MD5       string         `json:"md5"`
	SHA1      string         `json:"sha1"`
	SHA256    string         `json:"sha256"`
	Entropy   float64        `json:"entropy"`
	Kind      string         `json:"kind"`
	Details   map[string]any `json:"details,omitempty"`
	XORHits   []XORMatch     `json:"xor_masked_hypotheses,omitempty"`
	Hints     []string       `json:"hints,omitempty"`
	Limits    []string       `json:"-"`
	ZipInside *APKInfo       `json:"-"`
}

type histWriter struct{ h [256]uint64 }

func (w *histWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		w.h[b]++
	}
	return len(p), nil
}

func entropyOf(h *[256]uint64, n uint64) float64 {
	if n == 0 {
		return 0
	}
	var e float64
	for _, c := range h {
		if c == 0 {
			continue
		}
		p := float64(c) / float64(n)
		e -= p * math.Log2(p)
	}
	return e
}

func bufEntropy(b []byte) float64 {
	var h [256]uint64
	for _, c := range b {
		h[c]++
	}
	return entropyOf(&h, uint64(len(b)))
}

// sniff identifies a file kind from its first bytes (no XOR handling).
func sniff(h []byte) (kind string, hint string) {
	switch {
	case bytes.HasPrefix(h, []byte("MZ")):
		return "pe", ""
	case bytes.HasPrefix(h, []byte{0x7f, 'E', 'L', 'F'}):
		return "elf", ""
	case bytes.HasPrefix(h, []byte("PK\x03\x04")), bytes.HasPrefix(h, []byte("PK\x05\x06")):
		return "zip", ""
	case bytes.HasPrefix(h, []byte("dex\n")) && len(h) >= 8:
		return "dex", "dex version " + strings.TrimRight(string(h[4:7]), "\x00")
	case bytes.HasPrefix(h, []byte{0x1b, 'L', 'u', 'a'}) && len(h) > 4:
		return "lua-bytecode", fmt.Sprintf("Lua version byte 0x%02x (0x51=5.1 0x53=5.3 0x54=5.4); custom builds often reorder opcodes", h[4])
	case bytes.HasPrefix(h, []byte("UnityFS\x00")):
		return "unity-bundle", unityVersion(h)
	case bytes.HasPrefix(h, []byte("UnityWeb\x00")), bytes.HasPrefix(h, []byte("UnityRaw\x00")):
		return "unity-bundle-legacy", unityVersion(h)
	case len(h) >= 8 && le32(h) == 0xFAB11BAF:
		return "il2cpp-global-metadata", fmt.Sprintf("metadata version %d", le32(h[4:]))
	case bytes.HasPrefix(h, []byte("SQLite format 3\x00")):
		return "sqlite", ""
	case bytes.HasPrefix(h, []byte{0, 'a', 's', 'm'}):
		return "wasm", ""
	case bytes.HasPrefix(h, []byte{0x89, 'P', 'N', 'G'}):
		return "png", ""
	case bytes.HasPrefix(h, []byte("%PDF-")):
		return "pdf", ""
	case bytes.HasPrefix(h, []byte{0x1f, 0x8b}):
		return "gzip", ""
	case bytes.HasPrefix(h, []byte("7z\xbc\xaf\x27\x1c")):
		return "7z", ""
	case len(h) >= 4 && (le32(h) == 0xfeedface || le32(h) == 0xfeedfacf || be32(h) == 0xfeedface || be32(h) == 0xfeedfacf):
		return "macho", ""
	case bytes.HasPrefix(h, []byte("GGUF")):
		return "gguf", ""
	case bytes.HasPrefix(h, []byte{0xca, 0xfe, 0xba, 0xbe}):
		return "macho-fat-or-java-class", ""
	}
	return "unknown", ""
}

func unityVersion(h []byte) string {
	// signature\0, u32 BE format, unity version\0, engine revision\0
	i := bytes.IndexByte(h, 0)
	if i < 0 || i+5 >= len(h) {
		return ""
	}
	rest := h[i+5:]
	j := bytes.IndexByte(rest, 0)
	if j <= 0 {
		return ""
	}
	return fmt.Sprintf("bundle format %d, player %s", be32(h[i+1:]), rest[:j])
}

// TriageFile hashes (up to maxHash bytes), measures entropy and decodes format details.
func TriageFile(path string, maxHash int64) (*Triage, error) {
	head, size, err := readHead(path, 4096)
	if err != nil {
		return nil, err
	}
	t := &Triage{Path: path, Size: size, Details: map[string]any{}}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if maxHash <= 0 || maxHash > size {
		maxHash = size
	}
	hw := &histWriter{}
	hs := []hash.Hash{md5.New(), sha1.New(), sha256.New()}
	w := io.MultiWriter(hs[0], hs[1], hs[2], hw)
	n, err := io.CopyN(w, f, maxHash)
	if err != nil && err != io.EOF {
		return nil, err
	}
	t.Hashed = n
	t.MD5, t.SHA1, t.SHA256 = hex.EncodeToString(hs[0].Sum(nil)), hex.EncodeToString(hs[1].Sum(nil)), hex.EncodeToString(hs[2].Sum(nil))
	t.Entropy = round2(entropyOf(&hw.h, uint64(n)))
	if n < size {
		t.Limits = append(t.Limits, fmt.Sprintf("hashes/entropy cover only the first %d of %d bytes; they are NOT whole-file identities", n, size))
	}
	if t.Entropy > 7.5 {
		t.Hints = append(t.Hints, "entropy > 7.5: packed, encrypted or compressed payload")
	}

	kind, hint := sniff(head)
	t.Kind = kind
	if hint != "" {
		t.Hints = append(t.Hints, hint)
	}
	switch kind {
	case "pe":
		describePE(path, t)
	case "elf":
		describeELF(path, t)
	case "macho":
		describeMachO(path, t)
	case "zip":
		if info, err := InspectAPK(path, 0); err == nil {
			t.ZipInside = info
			t.Details["zip"] = info.Summary()
			t.Kind = info.Container
		}
	case "unknown":
		if len(head) >= 64 {
			if hits := ProbeSingleByteXOR(head[:min(len(head), 512)]); len(hits) > 0 {
				t.XORHits = hits
				t.Kind = "xor-masked:" + hits[0].Format
				t.Hints = append(t.Hints, "head matches a known signature after single-byte XOR; confirm with re_xor_probe before decoding")
			}
		}
	}
	return t, nil
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

var packerSections = []string{"upx0", "upx1", ".vmp0", ".vmp1", ".themida", ".winlice", ".enigma1", ".aspack", ".adata", ".petite", ".mpress1", ".nsp0", ".boot"}

func describePE(path string, t *Triage) {
	f, err := pe.Open(path)
	if err != nil {
		t.Limits = append(t.Limits, "debug/pe could not parse headers: "+err.Error())
		return
	}
	defer f.Close()
	d := t.Details
	d["machine"] = fmt.Sprintf("0x%x", f.Machine)
	d["timestamp"] = f.TimeDateStamp
	d["characteristics"] = fmt.Sprintf("0x%x", f.Characteristics)
	d["is_dll"] = f.Characteristics&pe.IMAGE_FILE_DLL != 0
	var entry, imgBase uint64
	var dirs []pe.DataDirectory
	switch oh := f.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		entry, imgBase, dirs = uint64(oh.AddressOfEntryPoint), oh.ImageBase, oh.DataDirectory[:]
		d["bits"] = 64
	case *pe.OptionalHeader32:
		entry, imgBase, dirs = uint64(oh.AddressOfEntryPoint), uint64(oh.ImageBase), oh.DataDirectory[:]
		d["bits"] = 32
	}
	d["entry_rva"] = fmt.Sprintf("0x%x", entry)
	d["image_base"] = fmt.Sprintf("0x%x", imgBase)
	if len(dirs) > 14 && dirs[14].VirtualAddress != 0 {
		t.Kind = "pe-dotnet"
		t.Hints = append(t.Hints, ".NET CLR header present: decompile with ilspycmd/dnSpy instead of native tools")
	}
	if len(dirs) > 4 && dirs[4].Size != 0 {
		d["authenticode_bytes"] = dirs[4].Size
	}
	var secs []map[string]any
	var maxEnd uint32
	for _, s := range f.Sections {
		name := strings.ToLower(strings.TrimRight(s.Name, "\x00"))
		ent := 0.0
		if s.Size > 0 && s.Size <= 64<<20 {
			if data, err := s.Data(); err == nil {
				ent = round2(bufEntropy(data))
			}
		}
		secs = append(secs, map[string]any{"name": s.Name, "raw_offset": s.Offset, "raw_size": s.Size, "virtual_size": s.VirtualSize, "entropy": ent})
		if end := s.Offset + s.Size; end > maxEnd {
			maxEnd = end
		}
		for _, p := range packerSections {
			if name == p {
				t.Hints = append(t.Hints, "packer-like section name: "+s.Name)
			}
		}
		if ent > 7.2 && name != ".rsrc" {
			t.Hints = append(t.Hints, fmt.Sprintf("section %s entropy %.2f: likely packed/encrypted", s.Name, ent))
		}
	}
	d["sections"] = secs
	if t.Size > int64(maxEnd) && maxEnd > 0 {
		d["overlay_bytes"] = t.Size - int64(maxEnd)
	}
	if libs, err := f.ImportedLibraries(); err == nil && len(libs) > 0 {
		if len(libs) > 40 {
			libs = libs[:40]
		}
		d["imported_libraries"] = libs
	}
}

func describeELF(path string, t *Triage) {
	f, err := elf.Open(path)
	if err != nil {
		t.Limits = append(t.Limits, "debug/elf could not parse headers: "+err.Error())
		return
	}
	defer f.Close()
	d := t.Details
	d["class"], d["machine"], d["type"] = f.Class.String(), f.Machine.String(), f.Type.String()
	d["entry"] = fmt.Sprintf("0x%x", f.Entry)
	if libs, err := f.ImportedLibraries(); err == nil {
		d["needed"] = libs
	}
	var names []string
	for _, s := range f.Sections {
		names = append(names, s.Name)
		switch s.Name {
		case ".go.buildinfo", ".note.go.buildid":
			t.Hints = append(t.Hints, "Go binary (section "+s.Name+")")
		case ".gnu_debugdata":
			t.Hints = append(t.Hints, "has MiniDebugInfo (.gnu_debugdata)")
		}
	}
	if len(names) > 60 {
		names = names[:60]
	}
	d["sections"] = names
	if syms, err := f.DynamicSymbols(); err == nil {
		d["dynamic_symbol_count"] = len(syms)
		for _, s := range syms {
			if s.Name == "il2cpp_init" || s.Name == "il2cpp_runtime_invoke" {
				t.Hints = append(t.Hints, "exports il2cpp_* API: pair with global-metadata.dat for Il2CppDumper-style recovery")
				break
			}
			if s.Name == "mono_image_open_from_data_with_name" {
				t.Hints = append(t.Hints, "Mono runtime: managed DLLs may be loaded through mono_image_open_from_data (custom loaders can mask assemblies)")
				break
			}
		}
	}
	if len(f.Sections) == 0 || f.SectionByType(elf.SHT_SYMTAB) == nil {
		t.Hints = append(t.Hints, "stripped (no .symtab)")
	}
}

func describeMachO(path string, t *Triage) {
	f, err := macho.Open(path)
	if err != nil {
		t.Limits = append(t.Limits, "debug/macho could not parse headers: "+err.Error())
		return
	}
	defer f.Close()
	t.Details["cpu"], t.Details["type"], t.Details["load_commands"] = f.Cpu.String(), f.Type.String(), len(f.Loads)
}
