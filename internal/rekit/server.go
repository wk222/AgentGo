package rekit

import (
	"context"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Version is reported to MCP clients.
const Version = "0.1.0"

const instructions = `agentgo-re: evidence-first reverse-engineering kit.
Workflow: re_env -> re_triage / re_apk_inspect / re_har_summary (cheap facts) -> re_strings / re_xor_probe (targeted probes)
-> re_tool_run (ghidra/jadx/ilspycmd/rizin/idat via allowlist; output is paged + spilled to a file)
-> record EVERY conclusion with re_evidence_add (observation needs a source, derivation needs basis ids,
a 'high' inference needs a gate) -> re_report writes REPORT.md with the unknowns as next steps.
All answers are paged; pass offset/limit instead of asking for everything. Tokens/cookies in HAR are masked unless reveal=true.`

// Kit wires the toolkit into an MCP server.
type Kit struct {
	Home     string
	HomeFunc func() string
	Box      *ToolBox
}

func (k *Kit) homeDir() string {
	if k.HomeFunc != nil {
		if h := k.HomeFunc(); h != "" {
			return h
		}
	}
	return k.Home
}

// NewServer builds the MCP server with every re_* tool registered.
func NewServer(home string, allowDynamic bool) *server.MCPServer {
	return NewServerWithHome(func() string { return home }, allowDynamic)
}

// NewServerWithHome builds the MCP server using a dynamic home directory resolver
// (used by the in-process plugin so switching workspaces switches the default case root).
func NewServerWithHome(homeFn func() string, allowDynamic bool) *server.MCPServer {
	initial := ""
	if homeFn != nil {
		initial = homeFn()
	}
	box := NewToolBox(initial, allowDynamic)
	box.HomeFunc = homeFn
	k := &Kit{Home: initial, HomeFunc: homeFn, Box: box}
	s := server.NewMCPServer("agentgo-re", Version, server.WithToolCapabilities(false), server.WithInstructions(instructions))
	k.register(s)
	return s
}

type handler func(req mcp.CallToolRequest) (Reply, error)

func wrap(name string, h handler) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		r, err := h(req)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("%s: %v", name, err)), nil
		}
		r.Tool, r.OK = name, true
		return mcp.NewToolResultText(r.JSON()), nil
	}
}

func absPath(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("path is required")
	}
	return filepath.Abs(p)
}

func (k *Kit) register(s *server.MCPServer) {
	ro := mcp.WithReadOnlyHintAnnotation(true)

	s.AddTool(mcp.NewTool("re_env", ro,
		mcp.WithDescription("List external RE tools the kit can launch (ghidra headless, jadx, ilspycmd, rizin, idat, adb, frida...) with resolved paths. Set versions=true to run each --version."),
		mcp.WithBoolean("versions", mcp.Description("also query each tool's version (slower)"))),
		wrap("re_env", func(req mcp.CallToolRequest) (Reply, error) {
			tools := k.Box.Probe(false)
			type row struct {
				Name    string `json:"name"`
				Class   string `json:"class"`
				Path    string `json:"path,omitempty"`
				Version string `json:"version,omitempty"`
				Purpose string `json:"purpose"`
				Found   bool   `json:"found"`
			}
			var rows []row
			var missing []string
			for _, t := range tools {
				v := ""
				if req.GetBool("versions", false) && t.Resolved != "" {
					v = k.Box.Version(t)
				}
				rows = append(rows, row{t.Name, t.Class, t.Resolved, v, t.Purpose, t.Resolved != ""})
				if t.Resolved == "" {
					missing = append(missing, t.Name)
				}
			}
			h := k.homeDir()
			return Reply{
				Facts:  map[string]any{"home": h, "allow_dynamic": k.Box.AllowDynamic, "tools": rows},
				Limits: []string{"'found' only means an executable exists; it was not run unless versions=true"},
				Next:   []string{fmt.Sprintf("missing: %s. Point to them with env RE_TOOL_<NAME> or %s", strings.Join(missing, ", "), filepath.Join(h, "tools.json"))},
			}, nil
		}))

	s.AddTool(mcp.NewTool("re_triage", ro,
		mcp.WithDescription("First look at any file: size, md5/sha1/sha256, entropy, format (PE/ELF/Mach-O/zip/APK/dex/Unity bundle/IL2CPP metadata/Lua/...), sections, imports, .NET/packer/XOR-mask hints."),
		mcp.WithString("path", mcp.Required(), mcp.Description("file path")),
		mcp.WithNumber("max_hash_mb", mcp.Description("hash at most this many MiB from the start (default: whole file)"))),
		wrap("re_triage", func(req mcp.CallToolRequest) (Reply, error) {
			p, err := absPath(req.GetString("path", ""))
			if err != nil {
				return Reply{}, err
			}
			t, err := TriageFile(p, int64(req.GetFloat("max_hash_mb", 0)*(1<<20)))
			if err != nil {
				return Reply{}, err
			}
			next := nextForKind(t)
			return Reply{Facts: t, Limits: append(t.Limits, "format hints are heuristics from headers/names, not proof of behavior"), Next: next}, nil
		}))

	s.AddTool(mcp.NewTool("re_apk_inspect", ro,
		mcp.WithDescription("Static APK/XAPK/zip inspection from the central directory: parsed AndroidManifest (package, versionCode, SDKs, permissions, launcher), dex list, ABIs, engine (Unity/IL2CPP/Mono/Flutter/RN/Cocos/Lua), packer markers (Bangcle, 360, Tencent, Baidu...), signature files, XOR-masked Managed DLLs."),
		mcp.WithString("path", mcp.Required(), mcp.Description("apk/xapk/zip path")),
		mcp.WithNumber("list_limit", mcp.Description("max native libs listed per ABI (default 40)"))),
		wrap("re_apk_inspect", func(req mcp.CallToolRequest) (Reply, error) {
			p, err := absPath(req.GetString("path", ""))
			if err != nil {
				return Reply{}, err
			}
			info, err := InspectAPK(p, req.GetInt("list_limit", 40))
			if err != nil {
				return Reply{}, err
			}
			var next []string
			if len(info.MaskedManaged) > 0 {
				next = append(next, "masked Managed DLLs: decode with re_xor_probe decode_to=..., then re_tool_run ilspycmd")
			}
			if len(info.Packers) > 0 {
				next = append(next, "packer markers present: static dex is likely a shell; prefer live dump/Frida (see skill android-bangcle-vmp-unidbg)")
			}
			if contains(info.Engines, "Unity IL2CPP") {
				next = append(next, "IL2CPP: extract libil2cpp.so + global-metadata.dat, re_triage the metadata for its version")
			}
			if info.Manifest == nil {
				next = append(next, "no parsable AndroidManifest.xml: for XAPK, inspect the inner base APK")
			}
			return Reply{Facts: info,
				Limits: []string{"no code was executed; engine/packer names come from file names inside the archive", "XOR hypotheses are single-byte only"},
				Next:   next}, nil
		}))

	s.AddTool(mcp.NewTool("re_strings", ro,
		mcp.WithDescription("Extract ASCII (+UTF-16LE with wide=true) strings with file offsets from a window of a file; regexp filter; paged. Window max 64 MiB per call: continue with next_scan_offset."),
		mcp.WithString("path", mcp.Required()),
		mcp.WithNumber("offset", mcp.Description("scan window start (default 0)")),
		mcp.WithNumber("length", mcp.Description("scan window bytes (default/max 64MiB)")),
		mcp.WithNumber("min_len", mcp.Description("minimum string length (default 4)")),
		mcp.WithBoolean("wide", mcp.Description("also scan 2-byte-aligned UTF-16LE")),
		mcp.WithString("filter", mcp.Description("Go regexp applied to each string")),
		mcp.WithNumber("limit", mcp.Description("page size (default 50)")),
		mcp.WithNumber("page_offset", mcp.Description("skip this many matches"))),
		wrap("re_strings", func(req mcp.CallToolRequest) (Reply, error) {
			p, err := absPath(req.GetString("path", ""))
			if err != nil {
				return Reply{}, err
			}
			off := int64(req.GetFloat("offset", 0))
			hits, scanned, eof, err := ExtractStrings(p, off, int64(req.GetFloat("length", 0)), req.GetInt("min_len", 4), req.GetBool("wide", false), req.GetString("filter", ""))
			if err != nil {
				return Reply{}, err
			}
			lo, hi, pg := page(len(hits), req.GetInt("page_offset", 0), req.GetInt("limit", 50))
			extra := map[string]any{"scanned_bytes": scanned, "window_start": off, "reached_eof": eof}
			if !eof {
				extra["next_scan_offset"] = off + scanned
			}
			return Reply{Facts: hits[lo:hi], Paging: pg, Extra: extra,
				Limits: []string{"wide scan only sees 2-byte-aligned UTF-16LE; strings split across window edges can be cut", "offsets are file offsets, not RVAs/VAs"}}, nil
		}))

	s.AddTool(mcp.NewTool("re_xor_probe",
		mcp.WithDescription("Detect/decode XOR-masked files. (1) default: try all single-byte keys on the head against known signatures (MZ+PE, ELF, zip, dex, Lua, UnityFS, IL2CPP metadata...). (2) expected_hex: derive key = head^expected and detect its period. (3) decode_to + key_hex: write a decoded COPY (never overwrites the source)."),
		mcp.WithString("path", mcp.Required()),
		mcp.WithNumber("offset", mcp.Description("head offset (default 0)")),
		mcp.WithString("expected_hex", mcp.Description("known plaintext at offset, e.g. 4d5a9000")),
		mcp.WithString("key_hex", mcp.Description("repeating XOR key bytes in hex, e.g. ff")),
		mcp.WithString("decode_to", mcp.Description("output path; relative paths go under <home>/cases/<case>/exports")),
		mcp.WithString("case", mcp.Description("case name (default 'default')"))),
		wrap("re_xor_probe", func(req mcp.CallToolRequest) (Reply, error) {
			p, err := absPath(req.GetString("path", ""))
			if err != nil {
				return Reply{}, err
			}
			off := int64(req.GetFloat("offset", 0))
			head, _, err := readHeadAt(p, off, 512)
			if err != nil {
				return Reply{}, err
			}
			facts := map[string]any{"head_hex": hexPreview(head, 16)}
			var next []string
			if hits := ProbeSingleByteXOR(head); len(hits) > 0 {
				facts["single_byte_hypotheses"] = hits
				next = append(next, "decode a copy with key_hex + decode_to, then re_triage the copy to confirm the format parses")
			} else {
				facts["single_byte_hypotheses"] = []XORMatch{}
			}
			if eh := req.GetString("expected_hex", ""); eh != "" {
				kp, err := KnownPlaintextKey(head, eh)
				if err != nil {
					return Reply{}, err
				}
				facts["known_plaintext_key"] = kp
			}
			if to, kh := req.GetString("decode_to", ""), req.GetString("key_hex", ""); to != "" || kh != "" {
				key, err := hex.DecodeString(kh)
				if err != nil || len(key) == 0 {
					return Reply{}, fmt.Errorf("decode needs key_hex (hex bytes) and decode_to")
				}
				if to == "" {
					return Reply{}, fmt.Errorf("decode_to is required with key_hex")
				}
				if !filepath.IsAbs(to) {
					d, err := caseDir(k.homeDir(), req.GetString("case", ""))
					if err != nil {
						return Reply{}, err
					}
					to = filepath.Join(d, "exports", to)
				}
				n, err := DecodeXORFile(p, to, key)
				if err != nil {
					return Reply{}, err
				}
				facts["decoded"] = map[string]any{"to": to, "bytes": n, "key_hex": kh}
			}
			return Reply{Facts: facts, Next: next,
				Limits: []string{"a signature match after XOR is a hypothesis; only a successful parse of the decoded file confirms it", "multi-byte keys need expected_hex (known plaintext)"}}, nil
		}))

	harArgs := []mcp.ToolOption{ro, mcp.WithString("path", mcp.Required(), mcp.Description("HAR file"))}
	s.AddTool(mcp.NewTool("re_har_summary", append(harArgs,
		mcp.WithDescription("HAR-first API reverse: group requests into endpoints (METHOD host/path with ids normalised) with statuses, query keys, request/response body JSON keys and auth/sign-like header names. Use re_har_entry for one full exchange."),
		mcp.WithString("host", mcp.Description("host substring filter")),
		mcp.WithString("path_regex", mcp.Description("regexp on URL path")),
		mcp.WithString("method", mcp.Description("GET/POST/...")),
		mcp.WithNumber("status", mcp.Description("exact HTTP status")),
		mcp.WithBoolean("json_only", mcp.Description("only JSON responses")),
		mcp.WithNumber("limit", mcp.Description("page size (default 30)")),
		mcp.WithNumber("offset", mcp.Description("page offset")))...),
		wrap("re_har_summary", func(req mcp.CallToolRequest) (Reply, error) {
			p, err := absPath(req.GetString("path", ""))
			if err != nil {
				return Reply{}, err
			}
			h, err := LoadHAR(p)
			if err != nil {
				return Reply{}, err
			}
			eps := h.Summarize(HARFilter{req.GetString("host", ""), req.GetString("path_regex", ""), req.GetString("method", ""), req.GetInt("status", 0), req.GetBool("json_only", false)})
			lo, hi, pg := page(len(eps), req.GetInt("offset", 0), req.GetInt("limit", 30))
			return Reply{Facts: eps[lo:hi], Paging: pg, Extra: map[string]any{"total_entries": len(h.Entries)},
				Limits: []string{"body keys come from at most depth 2 of JSON bodies; encrypted/base64 bodies show no keys", "path normalisation replaces long numeric/hex segments with {id}"},
				Next:   []string{"re_har_entry index=<entry_indexes[0]> to read one exchange", "when a sign check fails, treat the server's error code as an oracle for which field is wrong"}}, nil
		}))

	s.AddTool(mcp.NewTool("re_har_entry", append(harArgs,
		mcp.WithDescription("One HAR exchange in full. Authorization/Cookie-class header values are masked unless reveal=true."),
		mcp.WithNumber("index", mcp.Required(), mcp.Description("entry index from re_har_summary")),
		mcp.WithNumber("max_body", mcp.Description("max chars per body (default 4000)")),
		mcp.WithBoolean("reveal", mcp.Description("show credential headers unmasked")))...),
		wrap("re_har_entry", func(req mcp.CallToolRequest) (Reply, error) {
			p, err := absPath(req.GetString("path", ""))
			if err != nil {
				return Reply{}, err
			}
			h, err := LoadHAR(p)
			if err != nil {
				return Reply{}, err
			}
			d, err := h.Detail(req.GetInt("index", -1), req.GetInt("max_body", 4000), req.GetBool("reveal", false))
			if err != nil {
				return Reply{}, err
			}
			return Reply{Facts: d, Limits: []string{"HAR may omit bodies, redact headers or reorder fields; byte-exact replay needs the live app"}}, nil
		}))

	s.AddTool(mcp.NewTool("re_evidence_add",
		mcp.WithDescription("Record a claim in the case ledger. kind=observation needs source; derivation needs basis ids; inference with confidence=high needs a gate (objective check, e.g. 'diff_exec 3205/3205'); unknown should carry next_step."),
		mcp.WithString("case", mcp.Description("case name (default 'default')")),
		mcp.WithString("kind", mcp.Required(), mcp.Enum(KindObservation, KindDerivation, KindInference, KindUnknown)),
		mcp.WithString("claim", mcp.Required()),
		mcp.WithString("source", mcp.Description("tool call / file / offset / URL backing the claim")),
		mcp.WithArray("basis", mcp.Description("evidence ids this derives from"), mcp.WithStringItems()),
		mcp.WithString("confidence", mcp.Enum("low", "medium", "high")),
		mcp.WithString("gate", mcp.Description("objective check that agreed with the claim")),
		mcp.WithArray("tags", mcp.WithStringItems()),
		mcp.WithString("supersedes", mcp.Description("id of the record this replaces")),
		mcp.WithString("next_step", mcp.Description("what would resolve it"))),
		wrap("re_evidence_add", func(req mcp.CallToolRequest) (Reply, error) {
			e, err := AddEvidence(k.homeDir(), req.GetString("case", ""), Evidence{
				Kind: req.GetString("kind", ""), Claim: req.GetString("claim", ""), Source: req.GetString("source", ""),
				Basis: req.GetStringSlice("basis", nil), Confidence: req.GetString("confidence", ""), Gate: req.GetString("gate", ""),
				Tags: req.GetStringSlice("tags", nil), Supersedes: req.GetString("supersedes", ""), NextStep: req.GetString("next_step", ""),
			})
			if err != nil {
				return Reply{}, err
			}
			return Reply{Facts: e}, nil
		}))

	s.AddTool(mcp.NewTool("re_evidence_list", ro,
		mcp.WithDescription("Query the case ledger (resume a job after a context reset). Superseded records are hidden unless include_superseded."),
		mcp.WithString("case"), mcp.WithString("kind", mcp.Enum(KindObservation, KindDerivation, KindInference, KindUnknown)),
		mcp.WithString("tag"), mcp.WithString("text", mcp.Description("substring over claim/source/next_step")),
		mcp.WithBoolean("include_superseded"), mcp.WithNumber("limit"), mcp.WithNumber("offset")),
		wrap("re_evidence_list", func(req mcp.CallToolRequest) (Reply, error) {
			ev, err := ListEvidence(k.homeDir(), req.GetString("case", ""), EvidenceQuery{req.GetString("kind", ""), req.GetString("tag", ""), req.GetString("text", ""), req.GetBool("include_superseded", false)})
			if err != nil {
				return Reply{}, err
			}
			lo, hi, pg := page(len(ev), req.GetInt("offset", 0), req.GetInt("limit", 50))
			return Reply{Facts: ev[lo:hi], Paging: pg}, nil
		}))

	s.AddTool(mcp.NewTool("re_report",
		mcp.WithDescription("Render <home>/cases/<case>/REPORT.md from the ledger: observations, derivations, inferences, unknowns (= next steps)."),
		mcp.WithString("case")),
		wrap("re_report", func(req mcp.CallToolRequest) (Reply, error) {
			path, md, err := RenderReport(k.homeDir(), req.GetString("case", ""))
			if err != nil {
				return Reply{}, err
			}
			return Reply{Facts: map[string]any{"report_path": path, "markdown": md}}, nil
		}))

	s.AddTool(mcp.NewTool("re_tool_run",
		mcp.WithDescription("Run an allowlisted analysis tool with an argv array (no shell). Output is captured to a spill file; a head+tail excerpt is returned. Dynamic tools (adb, frida) need --allow-dynamic. Names: see re_env."),
		mcp.WithString("tool", mcp.Required(), mcp.Description("e.g. ilspycmd, jadx, ghidra-headless, rizin, idat, 7z")),
		mcp.WithArray("args", mcp.Description("argv after the executable"), mcp.WithStringItems()),
		mcp.WithString("cwd"), mcp.WithString("case"),
		mcp.WithNumber("timeout_s", mcp.Description("default 120, max 1800")),
		mcp.WithNumber("max_chars", mcp.Description("excerpt size (default 8000)"))),
		wrap("re_tool_run", func(req mcp.CallToolRequest) (Reply, error) {
			res, err := k.Box.Run(req.GetString("case", ""), req.GetString("tool", ""), req.GetStringSlice("args", nil), req.GetString("cwd", ""),
				time.Duration(req.GetFloat("timeout_s", 120))*time.Second, req.GetInt("max_chars", 8000))
			if err != nil {
				return Reply{}, err
			}
			next := []string{}
			if res.ExitCode != 0 || res.TimedOut {
				next = append(next, "non-zero exit or timeout: an empty output is NOT evidence of absence; read spill_path")
			}
			return Reply{Facts: res, Spill: res.SpillPath, Next: next,
				Limits: []string{"tool output is an observation of that tool, not ground truth; record conclusions with re_evidence_add"}}, nil
		}))

	s.AddTool(mcp.NewTool("re_ghidra_export", ro,
		mcp.WithDescription("Lazy + persistent binary evidence pack (functions, decompiled C, imports, exports, strings, call xrefs). Builds <case>/ghidra/<sha16>/index.json once via ghidra-headless (or rizin / pure-Go PE .pdata+exports+E8-calls / ELF symtab fallback) and serves paged views without dumping the whole decompilation into context."),
		mcp.WithString("path", mcp.Required(), mcp.Description("binary file path")),
		mcp.WithString("case", mcp.Description("case name (default 'default')")),
		mcp.WithString("view", mcp.Enum("overview", "functions", "function_detail", "imports", "exports", "strings", "xrefs"), mcp.Description("lazy query view (default 'overview')")),
		mcp.WithString("target", mcp.Description("function name or address for view=function_detail")),
		mcp.WithString("filter", mcp.Description("case-insensitive regexp filter for list views")),
		mcp.WithNumber("offset", mcp.Description("page offset")),
		mcp.WithNumber("limit", mcp.Description("page size (default 40)")),
		mcp.WithString("backend", mcp.Enum("auto", "ghidra", "rizin", "native"), mcp.Description("indexer backend (default 'auto')")),
		mcp.WithString("import_json", mcp.Description("optional external Ghidra/IDA JSON file to ingest into persistent cache")),
		mcp.WithBoolean("force", mcp.Description("rebuild index even if cached")),
		mcp.WithNumber("decompile_limit", mcp.Description("max functions to decompile in headless pass (default 60)")),
		mcp.WithNumber("timeout_s", mcp.Description("headless timeout seconds (default 300)"))),
		wrap("re_ghidra_export", func(req mcp.CallToolRequest) (Reply, error) {
			return k.ExportOrQueryBinary(GhidraExportRequest{
				Case:           req.GetString("case", ""),
				Path:           req.GetString("path", ""),
				Backend:        req.GetString("backend", "auto"),
				ImportJSON:     req.GetString("import_json", ""),
				Force:          req.GetBool("force", false),
				DecompileLimit: req.GetInt("decompile_limit", 60),
				TimeoutS:       req.GetInt("timeout_s", 300),
				View:           req.GetString("view", "overview"),
				Target:         req.GetString("target", ""),
				Filter:         req.GetString("filter", ""),
				Offset:         req.GetInt("offset", 0),
				Limit:          req.GetInt("limit", 40),
			})
		}))

	s.AddTool(mcp.NewTool("re_gate",
		mcp.WithDescription("Execute an objective verification gate (hash_eq, format_valid, byte_parity, json_golden) and record a PASS/FAIL GateReceipt (e.g. gate-0001) in <case>/gates.jsonl. High-confidence inferences in re_evidence_add can reference this gate id for cryptographic/structural proof."),
		mcp.WithString("case", mcp.Description("case name (default 'default')")),
		mcp.WithString("kind", mcp.Required(), mcp.Enum(GateHashEq, GateFormatValid, GateByteParity, GateJSONGolden)),
		mcp.WithString("path", mcp.Required(), mcp.Description("primary artifact path to verify")),
		mcp.WithString("other_path", mcp.Description("reference/golden file path (for hash_eq, byte_parity, json_golden)")),
		mcp.WithString("expected", mcp.Description("expected hash hex (hash_eq) or expected format kind (format_valid, e.g. pe-dotnet, elf, apk, har)")),
		mcp.WithString("algo", mcp.Enum("sha256", "sha1", "md5"), mcp.Description("hash algorithm for hash_eq (default sha256)")),
		mcp.WithNumber("max_diff_bytes", mcp.Description("allowed differing bytes for byte_parity (default 0 = byte-exact)")),
		mcp.WithNumber("abs_tol", mcp.Description("numeric absolute tolerance for json_golden (default 1e-6)")),
		mcp.WithString("note", mcp.Description("optional note describing what this gate proves"))),
		wrap("re_gate", func(req mcp.CallToolRequest) (Reply, error) {
			rec, err := RunGate(k.homeDir(), req.GetString("case", ""), GateRequest{
				Case:         req.GetString("case", ""),
				Kind:         req.GetString("kind", ""),
				Path:         req.GetString("path", ""),
				OtherPath:    req.GetString("other_path", ""),
				Expected:     req.GetString("expected", ""),
				Algo:         req.GetString("algo", "sha256"),
				MaxDiffBytes: int64(req.GetFloat("max_diff_bytes", 0)),
				AbsTol:       req.GetFloat("abs_tol", 1e-6),
				Note:         req.GetString("note", ""),
			})
			if err != nil {
				return Reply{}, err
			}
			var next []string
			if rec.Status == GatePass {
				next = append(next, fmt.Sprintf("re_evidence_add kind=inference confidence=high gate=%q claim=...", rec.ID+": "+rec.Summary))
			} else {
				next = append(next, "gate status is FAIL: do NOT record high-confidence inference; revise hypothesis or record as unknown")
			}
			return Reply{Facts: rec, Next: next}, nil
		}))
}

func nextForKind(t *Triage) []string {
	var n []string
	switch {
	case t.Kind == "pe-dotnet":
		n = append(n, "re_tool_run tool=ilspycmd args=[-p,-o,<outdir>,<path>] for a C# project")
	case t.Kind == "pe" || t.Kind == "elf" || t.Kind == "macho":
		n = append(n, "re_strings filter=... for quick anchors; re_tool_run tool=ghidra-headless/rizin/idat for functions")
	case strings.HasPrefix(t.Kind, "apk") || t.Kind == "xapk-or-apks-bundle":
		n = append(n, "re_apk_inspect for manifest/engine/packer detail")
	case strings.HasPrefix(t.Kind, "xor-masked"):
		n = append(n, "re_xor_probe path=... to confirm the key, then decode a copy")
	case t.Kind == "il2cpp-global-metadata":
		n = append(n, "pair with libil2cpp.so of the SAME build; metadata version decides which dumper release works")
	case t.Kind == "unknown" && t.Entropy > 7.5:
		n = append(n, "high entropy + no signature: look for a loader that decrypts it (re_strings on the host binary, XOR/AES constants)")
	}
	return n
}
