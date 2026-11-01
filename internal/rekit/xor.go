package rekit

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// magic is a well-known file signature used to detect single-byte XOR/NOT masking
// (e.g. a Unity Mono Assembly-CSharp.dll whose "MZ" became B2 A5 = MZ ^ 0xFF).
type magic struct {
	Name string
	Sig  []byte
}

var knownMagics = []magic{
	{"elf", []byte{0x7f, 'E', 'L', 'F'}},
	{"zip/apk/jar", []byte{'P', 'K', 3, 4}},
	{"dex", []byte("dex\n")},
	{"lua-bytecode", []byte{0x1b, 'L', 'u', 'a'}},
	{"unityfs", []byte("UnityFS")},
	{"wasm", []byte{0, 'a', 's', 'm'}},
	{"png", []byte{0x89, 'P', 'N', 'G'}},
	{"sqlite", []byte("SQLite format 3")},
	{"pdf", []byte("%PDF-")},
	{"gzip", []byte{0x1f, 0x8b, 8}},
	{"il2cpp-metadata", []byte{0xAF, 0x1B, 0xB1, 0xFA}},
	{"macho64", []byte{0xcf, 0xfa, 0xed, 0xfe}},
	{"7z", []byte{'7', 'z', 0xbc, 0xaf, 0x27, 0x1c}},
}

// XORMatch is one (key, format) hypothesis for a masked head.
type XORMatch struct {
	Key    string `json:"key"`
	Format string `json:"format"`
	Note   string `json:"note,omitempty"`
}

func xorBytes(b []byte, key byte) []byte {
	out := make([]byte, len(b))
	for i := range b {
		out[i] = b[i] ^ key
	}
	return out
}

// isMZ validates a decoded head as a real PE (MZ + e_lfanew -> "PE\0\0"); a bare "MZ"
// would match far too many keys.
func isMZ(d []byte) bool {
	if len(d) < 0x40 || d[0] != 'M' || d[1] != 'Z' {
		return false
	}
	off := int(le32(d[0x3c:]))
	return off > 0 && off+4 <= len(d) && bytes.Equal(d[off:off+4], []byte("PE\x00\x00"))
}

// ProbeSingleByteXOR tests all 255 non-zero keys against the known signatures.
// head should be >= 256 bytes so the PE check can follow e_lfanew.
func ProbeSingleByteXOR(head []byte) []XORMatch {
	var out []XORMatch
	for k := 1; k < 256; k++ {
		d := xorBytes(head, byte(k))
		if isMZ(d) {
			note := ""
			if k == 0xFF {
				note = "bitwise NOT (^0xFF), same scheme as Unity Mono Assembly-CSharp.dll in libmono.so"
			}
			out = append(out, XORMatch{Key: fmt.Sprintf("0x%02X", k), Format: "pe/MZ", Note: note})
			continue
		}
		for _, m := range knownMagics {
			if len(d) >= len(m.Sig) && bytes.Equal(d[:len(m.Sig)], m.Sig) {
				out = append(out, XORMatch{Key: fmt.Sprintf("0x%02X", k), Format: m.Name})
				break
			}
		}
	}
	return out
}

// KnownPlaintextKey derives the XOR key from head ^ expected and detects its period.
type KPKey struct {
	KeyHex string `json:"key_hex"`
	Period int    `json:"period"`
	Note   string `json:"note"`
}

func KnownPlaintextKey(head []byte, expectedHex string) (*KPKey, error) {
	exp, err := hex.DecodeString(strings.ReplaceAll(strings.TrimPrefix(expectedHex, "0x"), " ", ""))
	if err != nil || len(exp) == 0 {
		return nil, fmt.Errorf("expected_hex must be hex bytes, e.g. 4d5a9000: %v", err)
	}
	if len(exp) > len(head) {
		exp = exp[:len(head)]
	}
	key := make([]byte, len(exp))
	for i := range exp {
		key[i] = head[i] ^ exp[i]
	}
	period := len(key)
	for p := 1; p <= len(key)/2; p++ {
		ok := true
		for i := range key {
			if key[i] != key[i%p] {
				ok = false
				break
			}
		}
		if ok {
			period = p
			break
		}
	}
	note := "no repetition seen within the known-plaintext window: key may be longer than it, or not XOR"
	if period <= len(key)/2 {
		note = fmt.Sprintf("key repeats with period %d inside %d known bytes (a hypothesis until it decodes a whole file)", period, len(key))
	}
	return &KPKey{KeyHex: hex.EncodeToString(key[:period]), Period: period, Note: note}, nil
}

// DecodeXORFile streams src -> dst with a repeating key. It never writes over src.
func DecodeXORFile(src, dst string, key []byte) (int64, error) {
	if len(key) == 0 {
		return 0, fmt.Errorf("empty key")
	}
	sa, _ := filepath.Abs(src)
	da, _ := filepath.Abs(dst)
	if strings.EqualFold(sa, da) {
		return 0, fmt.Errorf("dst must differ from src (originals are never modified)")
	}
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(da), 0o755); err != nil {
		return 0, err
	}
	out, err := os.Create(da)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	buf := make([]byte, 1<<20)
	var total int64
	for {
		n, rerr := in.Read(buf)
		for i := 0; i < n; i++ {
			buf[i] ^= key[int((total+int64(i))%int64(len(key)))]
		}
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return total, werr
			}
			total += int64(n)
		}
		if rerr == io.EOF {
			return total, nil
		}
		if rerr != nil {
			return total, rerr
		}
	}
}
