package rekit

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

func fakePE() []byte {
	b := make([]byte, 512)
	copy(b, "MZ")
	binary.LittleEndian.PutUint32(b[0x3c:], 0x80)
	copy(b[0x80:], "PE\x00\x00")
	return b
}

func TestXORProbeFindsNOT(t *testing.T) {
	masked := xorBytes(fakePE(), 0xFF)
	if masked[0] != 0xB2 || masked[1] != 0xA5 {
		t.Fatalf("setup: want B2 A5, got %x %x", masked[0], masked[1])
	}
	hits := ProbeSingleByteXOR(masked)
	if len(hits) != 1 || hits[0].Key != "0xFF" || hits[0].Format != "pe/MZ" {
		t.Fatalf("hits = %+v", hits)
	}
	if h := ProbeSingleByteXOR(fakePE()); len(h) != 0 {
		t.Fatalf("plain PE must not look masked: %+v", h)
	}
}

func TestKnownPlaintextPeriod(t *testing.T) {
	key := []byte{0x13, 0x37}
	plain := []byte{'M', 'Z', 0x90, 0x00, 0x03, 0x00, 0x00, 0x00}
	enc := make([]byte, len(plain))
	for i := range plain {
		enc[i] = plain[i] ^ key[i%2]
	}
	kp, err := KnownPlaintextKey(enc, "4d5a900003000000")
	if err != nil || kp.Period != 2 || kp.KeyHex != "1337" {
		t.Fatalf("kp=%+v err=%v", kp, err)
	}
}

func TestDecodeNeverOverwritesSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.bin")
	_ = os.WriteFile(src, xorBytes(fakePE(), 0xFF), 0o644)
	if _, err := DecodeXORFile(src, src, []byte{0xFF}); err == nil {
		t.Fatal("decoding onto the source must be refused")
	}
	dst := filepath.Join(dir, "out", "a.dll")
	if n, err := DecodeXORFile(src, dst, []byte{0xFF}); err != nil || n != 512 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	got, _ := os.ReadFile(dst)
	if !bytes.Equal(got, fakePE()) {
		t.Fatal("decoded bytes differ from the original PE")
	}
	tr, err := TriageFile(src, 0)
	if err != nil || !strings.HasPrefix(tr.Kind, "xor-masked:pe") {
		t.Fatalf("triage of masked file: %+v err=%v", tr, err)
	}
}

// buildAXML hand-assembles a tiny binary manifest to test the parser without an APK.
func buildAXML(strs []string, elems func(idx func(string) uint32) [][]byte) []byte {
	var pool bytes.Buffer
	offs := make([]uint32, len(strs))
	for i, s := range strs {
		offs[i] = uint32(pool.Len())
		u := utf16.Encode([]rune(s))
		_ = binary.Write(&pool, binary.LittleEndian, uint16(len(u)))
		for _, c := range u {
			_ = binary.Write(&pool, binary.LittleEndian, c)
		}
		_ = binary.Write(&pool, binary.LittleEndian, uint16(0))
	}
	for pool.Len()%4 != 0 {
		pool.WriteByte(0)
	}
	hdr := 28 + 4*len(strs)
	sp := new(bytes.Buffer)
	w32 := func(b *bytes.Buffer, v uint32) { _ = binary.Write(b, binary.LittleEndian, v) }
	w16 := func(b *bytes.Buffer, v uint16) { _ = binary.Write(b, binary.LittleEndian, v) }
	w16(sp, 1)
	w16(sp, 28)
	w32(sp, uint32(hdr+pool.Len()))
	w32(sp, uint32(len(strs)))
	w32(sp, 0)
	w32(sp, 0)
	w32(sp, uint32(hdr))
	w32(sp, 0)
	for _, o := range offs {
		w32(sp, o)
	}
	sp.Write(pool.Bytes())
	idx := func(s string) uint32 {
		for i, x := range strs {
			if x == s {
				return uint32(i)
			}
		}
		panic("missing string " + s)
	}
	body := new(bytes.Buffer)
	body.Write(sp.Bytes())
	for _, e := range elems(idx) {
		body.Write(e)
	}
	out := new(bytes.Buffer)
	w16(out, 3)
	w16(out, 8)
	w32(out, uint32(8+body.Len()))
	out.Write(body.Bytes())
	return out.Bytes()
}

type attr struct {
	name  uint32
	dtype byte
	raw   uint32
	data  uint32
}

func startEl(name uint32, attrs []attr) []byte {
	b := new(bytes.Buffer)
	w16 := func(v uint16) { _ = binary.Write(b, binary.LittleEndian, v) }
	w32 := func(v uint32) { _ = binary.Write(b, binary.LittleEndian, v) }
	w16(0x0102)
	w16(16)
	w32(uint32(16 + 20 + 20*len(attrs)))
	w32(1)
	w32(0xffffffff)
	w32(0xffffffff)
	w32(name)
	w16(20)
	w16(20)
	w16(uint16(len(attrs)))
	w16(0)
	w16(0)
	w16(0)
	for _, a := range attrs {
		w32(0xffffffff)
		w32(a.name)
		w32(a.raw)
		w16(8)
		b.WriteByte(0)
		b.WriteByte(a.dtype)
		w32(a.data)
	}
	return b.Bytes()
}

func endEl(name uint32) []byte {
	out := new(bytes.Buffer)
	_ = binary.Write(out, binary.LittleEndian, uint16(0x0103))
	_ = binary.Write(out, binary.LittleEndian, uint16(16))
	_ = binary.Write(out, binary.LittleEndian, uint32(24))
	_ = binary.Write(out, binary.LittleEndian, uint32(1))
	_ = binary.Write(out, binary.LittleEndian, uint32(0xffffffff))
	_ = binary.Write(out, binary.LittleEndian, uint32(0xffffffff))
	_ = binary.Write(out, binary.LittleEndian, name)
	return out.Bytes()
}

func TestParseAXML(t *testing.T) {
	strs := []string{"manifest", "package", "versionCode", "versionName", "m.Sgame.yzr", "1.17.14", "uses-permission", "name",
		"android.permission.INTERNET", "application", "activity", "com.x.Main", "intent-filter", "action", "category",
		"android.intent.action.MAIN", "android.intent.category.LAUNCHER", "uses-sdk", "minSdkVersion", "targetSdkVersion"}
	data := buildAXML(strs, func(i func(string) uint32) [][]byte {
		return [][]byte{
			startEl(i("manifest"), []attr{{i("package"), 0x03, i("m.Sgame.yzr"), 0}, {i("versionCode"), 0x10, 0xffffffff, 38}, {i("versionName"), 0x03, i("1.17.14"), 0}}),
			startEl(i("uses-sdk"), []attr{{i("minSdkVersion"), 0x10, 0xffffffff, 21}, {i("targetSdkVersion"), 0x10, 0xffffffff, 28}}), endEl(i("uses-sdk")),
			startEl(i("uses-permission"), []attr{{i("name"), 0x03, i("android.permission.INTERNET"), 0}}), endEl(i("uses-permission")),
			startEl(i("application"), nil),
			startEl(i("activity"), []attr{{i("name"), 0x03, i("com.x.Main"), 0}}),
			startEl(i("intent-filter"), nil),
			startEl(i("action"), []attr{{i("name"), 0x03, i("android.intent.action.MAIN"), 0}}), endEl(i("action")),
			startEl(i("category"), []attr{{i("name"), 0x03, i("android.intent.category.LAUNCHER"), 0}}), endEl(i("category")),
			endEl(i("intent-filter")), endEl(i("activity")), endEl(i("application")), endEl(i("manifest")),
		}
	})
	m, err := ParseAXML(data)
	if err != nil {
		t.Fatal(err)
	}
	if m.Package != "m.Sgame.yzr" || m.VersionCode != "38" || m.VersionName != "1.17.14" || m.MinSDK != "21" || m.TargetSDK != "28" {
		t.Fatalf("manifest = %+v", m)
	}
	if len(m.Permissions) != 1 || len(m.LauncherActs) != 1 || m.LauncherActs[0] != "com.x.Main" {
		t.Fatalf("perms/launcher = %+v %+v", m.Permissions, m.LauncherActs)
	}
}

func TestLedgerRules(t *testing.T) {
	home := t.TempDir()
	if _, err := AddEvidence(home, "t", Evidence{Kind: KindObservation, Claim: "x"}); err == nil {
		t.Fatal("observation without source must fail")
	}
	if _, err := AddEvidence(home, "t", Evidence{Kind: KindDerivation, Claim: "x"}); err == nil {
		t.Fatal("derivation without basis must fail")
	}
	if _, err := AddEvidence(home, "t", Evidence{Kind: KindInference, Claim: "x", Confidence: "high"}); err == nil {
		t.Fatal("high-confidence inference without gate must fail")
	}
	o, err := AddEvidence(home, "t", Evidence{Kind: KindObservation, Claim: "head is B2A5", Source: "re_xor_probe a.dll"})
	if err != nil || o.ID != "ev-0001" {
		t.Fatalf("o=%+v err=%v", o, err)
	}
	d, err := AddEvidence(home, "t", Evidence{Kind: KindDerivation, Claim: "key is 0xFF", Basis: []string{o.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AddEvidence(home, "t", Evidence{Kind: KindDerivation, Claim: "bad", Basis: []string{"ev-0099"}}); err == nil {
		t.Fatal("dangling basis must fail")
	}
	if _, err := AddEvidence(home, "t", Evidence{Kind: KindInference, Claim: "key is 0xFF", Supersedes: d.ID, Confidence: "high", Gate: "decoded file parses with debug/pe"}); err != nil {
		t.Fatal(err)
	}
	_, _ = AddEvidence(home, "t", Evidence{Kind: KindUnknown, Claim: "who loads it?", NextStep: "xrefs to mono_image_open_from_data"})
	list, _ := ListEvidence(home, "t", EvidenceQuery{})
	if len(list) != 3 {
		t.Fatalf("superseded record must be hidden, got %d", len(list))
	}
	_, md, err := RenderReport(home, "t")
	if err != nil || !strings.Contains(md, "四、未知 / 下一步") || !strings.Contains(md, "xrefs to mono_image_open_from_data") {
		t.Fatalf("report: %v\n%s", err, md)
	}
	if _, err := AddEvidence(home, "../evil", Evidence{Kind: KindUnknown, Claim: "x"}); err == nil {
		t.Fatal("path traversal in case name must fail")
	}
}

const sampleHAR = `{"log":{"entries":[
{"startedDateTime":"t0","request":{"method":"POST","url":"https://a.example/api/v1/user/12345678/save?ver=1","headers":[{"name":"Authorization","value":"Bearer abcdefghijklmnop"},{"name":"X-Sign","value":"s"}],"postData":{"mimeType":"application/json","text":"{\"studyRecord\":\"zz\",\"limitId\":3}"}},"response":{"status":200,"headers":[],"content":{"size":20,"mimeType":"application/json","text":"{\"code\":1,\"data\":{\"t\":1}}"}}},
{"startedDateTime":"t1","request":{"method":"POST","url":"https://a.example/api/v1/user/99999999/save?ver=1","headers":[],"postData":{"mimeType":"application/json","text":"{\"studyRecord\":\"yy\"}"}},"response":{"status":403,"headers":[],"content":{"size":10,"mimeType":"application/json","text":"{\"code\":1001}"}}}
]}}`

func TestHARGroupsAndMasks(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.har")
	_ = os.WriteFile(p, append([]byte("\xef\xbb\xbf"), sampleHAR...), 0o644) // BOM seen in real Fiddler exports
	h, err := LoadHAR(p)
	if err != nil {
		t.Fatal(err)
	}
	eps := h.Summarize(HARFilter{})
	if len(eps) != 1 || eps[0].Count != 2 || eps[0].Key != "POST a.example/api/v1/user/{id}/save" {
		t.Fatalf("eps = %+v", eps)
	}
	if got := strings.Join(eps[0].ReqBodyKeys, ","); got != "limitId,studyRecord" {
		t.Fatalf("req keys %s", got)
	}
	if eps[0].Statuses[403] != 1 || eps[0].Statuses[200] != 1 {
		t.Fatalf("statuses %+v", eps[0].Statuses)
	}
	d, _ := h.Detail(0, 100, false)
	if strings.Contains(d.ReqHeaders["Authorization"], "abcdefghijklmnop") || len(d.MaskedHeader) != 1 {
		t.Fatalf("authorization must be masked: %+v", d)
	}
	if r, _ := h.Detail(0, 100, true); !strings.Contains(r.ReqHeaders["Authorization"], "abcdefghijklmnop") {
		t.Fatal("reveal=true must show the header")
	}
}

func TestStringsOffsets(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.bin")
	var b bytes.Buffer
	b.Write([]byte{0, 0, 0})
	b.WriteString("hello_world")
	b.Write([]byte{0, 1})
	for _, c := range utf16.Encode([]rune("wide!")) {
		_ = binary.Write(&b, binary.LittleEndian, c)
	}
	b.Write([]byte{0, 0, 0, 0})
	_ = os.WriteFile(p, b.Bytes(), 0o644)
	hits, _, eof, err := ExtractStrings(p, 0, 0, 4, true, "")
	if err != nil || !eof {
		t.Fatal(err, eof)
	}
	var sawA, sawW bool
	for _, h := range hits {
		if h.Text == "hello_world" && h.Offset == 3 && h.Enc == "ascii" {
			sawA = true
		}
		if h.Text == "wide!" && h.Enc == "utf16le" {
			sawW = true
		}
	}
	if !sawA || !sawW {
		t.Fatalf("hits=%+v", hits)
	}
}

func TestMCPEndToEnd(t *testing.T) {
	home := t.TempDir()
	srv := NewServer(home, false)
	c, err := client.NewInProcessClient(srv)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	lt, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"re_env": false, "re_triage": false, "re_apk_inspect": false, "re_strings": false, "re_xor_probe": false,
		"re_har_summary": false, "re_har_entry": false, "re_evidence_add": false, "re_evidence_list": false, "re_report": false,
		"re_tool_run": false, "re_ghidra_export": false, "re_gate": false}
	for _, tl := range lt.Tools {
		want[tl.Name] = true
	}
	for n, ok := range want {
		if !ok {
			t.Errorf("tool %s not registered", n)
		}
	}
	src := filepath.Join(home, "m.dll")
	_ = os.WriteFile(src, xorBytes(fakePE(), 0xFF), 0o644)
	call := func(name string, args map[string]any) (string, bool) {
		var r mcp.CallToolRequest
		r.Params.Name, r.Params.Arguments = name, args
		res, err := c.CallTool(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		return res.Content[0].(mcp.TextContent).Text, res.IsError
	}
	txt, isErr := call("re_triage", map[string]any{"path": src})
	if isErr || !strings.Contains(txt, "xor-masked:pe") {
		t.Fatalf("triage: err=%v %s", isErr, txt)
	}
	txt, isErr = call("re_xor_probe", map[string]any{"path": src, "key_hex": "ff", "decode_to": "m.orig.dll", "case": "c1"})
	if isErr || !strings.Contains(txt, "decoded") {
		t.Fatalf("xor decode: %v %s", isErr, txt)
	}
	if _, err := os.Stat(filepath.Join(home, "cases", "c1", "exports", "m.orig.dll")); err != nil {
		t.Fatal("decoded copy should land under the case exports dir")
	}
	txt, isErr = call("re_tool_run", map[string]any{"tool": "adb", "args": []any{"devices"}})
	if !isErr || !strings.Contains(txt, "dynamic") {
		t.Fatalf("dynamic tool must be refused by default: %v %s", isErr, txt)
	}
	txt, isErr = call("re_tool_run", map[string]any{"tool": "bash", "args": []any{"-c", "id"}})
	if !isErr || !strings.Contains(txt, "allowlist") {
		t.Fatalf("non-allowlisted tool must be refused: %v %s", isErr, txt)
	}
	var env Reply
	txt, _ = call("re_env", nil)
	if err := json.Unmarshal([]byte(txt), &env); err != nil || !env.OK {
		t.Fatalf("env reply: %v %s", err, txt)
	}
}

func TestExecutableGatesAndLedgerEnforcement(t *testing.T) {
	home := t.TempDir()
	refFile := filepath.Join(home, "ref.bin")
	decFile := filepath.Join(home, "dec.bin")
	badFile := filepath.Join(home, "bad.bin")
	_ = os.WriteFile(refFile, fakePE(), 0o644)
	_ = os.WriteFile(decFile, fakePE(), 0o644)
	_ = os.WriteFile(badFile, xorBytes(fakePE(), 0x55), 0o644)

	// 1. PASS hash_eq gate (gate-0001)
	g1, err := RunGate(home, "gcase", GateRequest{
		Kind:      GateHashEq,
		Path:      decFile,
		OtherPath: refFile,
		Note:      "decoded matches reference PE",
	})
	if err != nil || g1.Status != GatePass || g1.ID != "gate-0001" {
		t.Fatalf("g1=%+v err=%v", g1, err)
	}

	// 2. FAIL byte_parity gate (gate-0002)
	g2, err := RunGate(home, "gcase", GateRequest{
		Kind:      GateByteParity,
		Path:      badFile,
		OtherPath: refFile,
	})
	if err != nil || g2.Status != GateFail || g2.ID != "gate-0002" {
		t.Fatalf("g2=%+v err=%v", g2, err)
	}

	// 3. json_golden gate with numeric tolerance (gate-0003 PASS, gate-0004 FAIL)
	j1 := filepath.Join(home, "actual.json")
	j2 := filepath.Join(home, "golden.json")
	_ = os.WriteFile(j1, []byte(`{"frame":1,"matrix":{"tx":10.0000004,"ty":-5.5},"tags":["a","b"]}`), 0o644)
	_ = os.WriteFile(j2, []byte(`{"frame":1,"matrix":{"tx":10.0,"ty":-5.5},"tags":["a","b"]}`), 0o644)
	g3, err := RunGate(home, "gcase", GateRequest{
		Kind:      GateJSONGolden,
		Path:      j1,
		OtherPath: j2,
		AbsTol:    1e-5,
	})
	if err != nil || g3.Status != GatePass {
		t.Fatalf("g3=%+v err=%v", g3, err)
	}
	g4, err := RunGate(home, "gcase", GateRequest{
		Kind:      GateJSONGolden,
		Path:      j1,
		OtherPath: j2,
		AbsTol:    1e-9,
	})
	if err != nil || g4.Status != GateFail {
		t.Fatalf("g4=%+v err=%v", g4, err)
	}

	// 4. Ledger rejects high-confidence inference referencing FAIL or non-existent gate.
	if _, err := AddEvidence(home, "gcase", Evidence{
		Kind: KindInference, Confidence: "high", Claim: "bad parity", Gate: "gate-0002",
	}); err == nil {
		t.Fatal("high-confidence inference referencing FAIL gate-0002 must be rejected")
	}
	if _, err := AddEvidence(home, "gcase", Evidence{
		Kind: KindInference, Confidence: "high", Claim: "missing gate", Gate: "gate-9999",
	}); err == nil {
		t.Fatal("high-confidence inference referencing unknown gate-9999 must be rejected")
	}

	// 5. Ledger accepts high-confidence inference referencing PASS gate-0001 & gate-0003.
	ev, err := AddEvidence(home, "gcase", Evidence{
		Kind: KindInference, Confidence: "high", Claim: "XOR key 0xFF + golden parity verified", Gate: "gate-0001 and gate-0003",
	})
	if err != nil || ev.ID != "ev-0001" {
		t.Fatalf("ev=%+v err=%v", ev, err)
	}
	_, md, err := RenderReport(home, "gcase")
	if err != nil || !strings.Contains(md, "五、客观门控核验记录") || !strings.Contains(md, "gate-0001") {
		t.Fatalf("report missing gates section: err=%v\n%s", err, md)
	}
}

func TestGhidraExportLazyIndexAndPECallGraph(t *testing.T) {
	home := t.TempDir()
	k := &Kit{Home: home, Box: NewToolBox(home, false)}

	// Use our own compiled test binary (a real PE/ELF executable) to exercise native-static indexing.
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	rep, err := k.ExportOrQueryBinary(GhidraExportRequest{
		Case:    "bintest",
		Path:    exe,
		Backend: "native",
		View:    "overview",
	})
	if err != nil || !rep.OK && rep.Facts == nil {
		t.Fatalf("overview err=%v rep=%+v", err, rep)
	}
	facts, ok := rep.Facts.(map[string]any)
	if !ok {
		t.Fatalf("unexpected facts type %T", rep.Facts)
	}
	if facts["cached"] != false {
		t.Fatalf("first run must not be cached: %+v", facts)
	}
	scriptPath, _ := facts["ghidra_script_path"].(string)
	if _, err := os.Stat(scriptPath); err != nil {
		t.Fatalf("ghidra_script_path not written: %v", err)
	}

	// Second query hits persistent cache and pages functions.
	rep2, err := k.ExportOrQueryBinary(GhidraExportRequest{
		Case:  "bintest",
		Path:  exe,
		View:  "functions",
		Limit: 5,
	})
	if err != nil || rep2.Paging == nil || rep2.Paging.Total == 0 {
		t.Fatalf("functions view failed: err=%v paging=%+v", err, rep2.Paging)
	}
	if rep2.Extra["cached"] != true {
		t.Fatalf("second run must hit persistent cache: %+v", rep2.Extra)
	}
	funcs := rep2.Facts.([]FuncRecord)
	if len(funcs) == 0 {
		t.Fatal("expected at least 1 indexed function")
	}

	// Query function_detail for the first function.
	det, err := k.ExportOrQueryBinary(GhidraExportRequest{
		Case:   "bintest",
		Path:   exe,
		View:   "function_detail",
		Target: funcs[0].Address,
	})
	if err != nil {
		t.Fatalf("function_detail failed: %v", err)
	}
	detFacts := det.Facts.(map[string]any)
	if detFacts["function"] == nil {
		t.Fatalf("missing function in detail: %+v", detFacts)
	}
}
