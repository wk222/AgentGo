package bridge

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"agentgo/internal/applog"
	"agentgo/internal/buildinfo"
	"agentgo/internal/config"
)

// GetAppInfo says which build is running and where it keeps its data, so the version can be read out without
// making a bundle.
func (s *AppService) GetAppInfo() map[string]any {
	info := buildinfo.Current()
	out := map[string]any{
		"version":    info.Version,
		"go_version": info.GoVersion,
		"os":         info.OS,
		"os_version": info.OSVersion,
		"arch":       info.Arch,
		"tags":       info.Tags,
		"host":       hostKind,
	}
	if s.rt != nil {
		out["data_dir"] = s.rt.DataDir()
	}
	if crash, ok := applog.PreviousCrash(); ok {
		out["previous_crash"] = crash
	}
	return out
}

// ExportDiagnostics writes a diagnostics bundle next to the attachments, which both hosts already serve to the
// page: the answer carries a url the page offers as a download, and the path for anyone who prefers the file
// manager. The bundle holds no conversation, memory or workspace content and no API keys.
func (s *AppService) ExportDiagnostics() map[string]any {
	if s.rt == nil || s.rt.DataDir() == "" {
		return map[string]any{"success": false, "error": "数据目录尚未就绪"}
	}
	now := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	files := s.collectDiagnostics(ctx, now)
	name := diagnosticsName(now)
	path, size, err := writeDiagnosticsBundle(filepath.Join(s.rt.DataDir(), "attachments"), name, files, now)
	if err != nil {
		return map[string]any{"success": false, "error": "生成诊断包失败：" + err.Error()}
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Name)
	}
	return map[string]any{"success": true, "name": name, "path": path, "size": size, "url": "/attachments/" + name, "files": names}
}

// collectDiagnostics gathers every section. A section that cannot be read says so in its own file instead of
// stopping the others: a bundle from a half-broken install is the one that matters.
func (s *AppService) collectDiagnostics(ctx context.Context, now time.Time) []diagFile {
	var files []diagFile
	add := func(name string, v any, err error) {
		if err != nil {
			v = map[string]string{"error": err.Error()}
		}
		data, merr := json.MarshalIndent(v, "", "  ")
		if merr != nil {
			data = []byte(fmt.Sprintf("{\n  \"error\": %q\n}", merr.Error()))
		}
		files = append(files, diagFile{Name: name, Data: data})
	}

	info := buildinfo.Current()
	add("environment.json", s.environmentReport(now, info), nil)

	cfg := s.rt.LLMConfig()
	add("llm.json", map[string]any{
		"api_base":       sanitizeURL(cfg.APIBase),
		"api_key_set":    cfg.APIKey != "",
		"model":          cfg.Model,
		"fallback_model": cfg.FallbackModel,
	}, nil)
	add("features.json", s.GetFeatureFlags(), nil)
	add("config.json", config.Snapshot(), nil) // secrets are masked by config.Snapshot
	add("plugins.json", s.ListPlugins(), nil)
	tools := append([]string{}, s.ListRegisteredTools()...)
	sort.Strings(tools)
	add("tools.json", tools, nil)

	if runner := s.rt.AgentRunner(); runner != nil && runner.UsageTracer() != nil {
		add("usage.json", summarizeUsage(runner.UsageTracer().Range(14, now)), nil)
	}
	if s.rt.memStore != nil {
		rep, err := databaseReport(ctx, s.rt.memStore.DB(), filepath.Join(s.rt.DataDir(), "agentgo.db"))
		add("database.json", rep, err)
	}

	if p := applog.Path(); p != "" {
		if tail, err := tailFile(p, logTailBytes); err == nil {
			files = append(files, diagFile{Name: "logs/agentgo.log.tail.txt", Data: tail})
		} else {
			add("logs/error.json", nil, err)
		}
	}
	if crash, ok := applog.PreviousCrash(); ok {
		if data, err := tailFile(crash.File, logTailBytes); err == nil {
			files = append(files, diagFile{Name: "logs/crash.last.txt", Data: data})
		}
	}
	readme := fmt.Sprintf(`AgentGo 诊断包
生成时间：%s
版本：%s（%s/%s，%s）

把这个文件发给维护人员，用来排查打不开、模型连不上、记忆或工具异常等问题。

包含：版本与系统信息、模型连接（地址和模型名，不含密钥）、运行参数（密钥已隐藏）、
      功能开关、插件与工具清单、近 14 天模型用量、数据库表行数、日志末尾（密钥与用户名已隐藏）、
      上次异常退出的现场记录（如果有）。
不含：对话内容、记忆内容、工作区文件、API 密钥。
`, now.Format("2006-01-02 15:04:05"), info.Version, info.OS, info.Arch, hostKind)
	// The byte order mark lets the notepad of Windows 7 read the Chinese text as UTF-8.
	files = append([]diagFile{{Name: "README.txt", Data: []byte("\xEF\xBB\xBF" + readme)}}, files...)
	return files
}

// environmentReport identifies the build and the machine it runs on. The executable's hash tells two builds
// with the same version name apart.
func (s *AppService) environmentReport(now time.Time, info buildinfo.Info) map[string]any {
	out := map[string]any{
		"generated_at": now.Format(time.RFC3339),
		"build":        info,
		"host":         hostKind,
		"num_cpu":      runtime.NumCPU(),
		"time_zone":    now.Location().String(),
		"log_file":     applog.Path(),
		"proxy_set":    proxyEnvPresence(),
	}
	if s.rt != nil {
		out["data_dir"] = s.rt.DataDir()
		out["workspace_root"] = s.rt.WorkspaceRoot()
	}
	if exe, err := os.Executable(); err == nil {
		ex := map[string]any{"path": exe}
		if st, err := os.Stat(exe); err == nil {
			ex["size"], ex["modified"] = st.Size(), st.ModTime().Format(time.RFC3339)
		}
		if sum, err := fileSHA256(exe); err == nil {
			ex["sha256"] = sum
		}
		out["executable"] = ex
	}
	return out
}

// proxyEnvPresence says which proxy variables are set, never their values: proxies are a usual cause of "cannot
// reach the model" on an intranet, and an address can carry a password.
func proxyEnvPresence() map[string]bool {
	out := map[string]bool{}
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY"} {
		out[k] = os.Getenv(k) != "" || os.Getenv(strings.ToLower(k)) != ""
	}
	return out
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// databaseReport counts rows per table (no content), the size of the database and its integrity check. It is
// bounded in time: a huge database must not hold the export up.
func databaseReport(ctx context.Context, db *sql.DB, path string) (map[string]any, error) {
	out := map[string]any{}
	if st, err := os.Stat(path); err == nil {
		out["size_bytes"] = st.Size()
		out["modified"] = st.ModTime().Format(time.RFC3339)
	}
	if st, err := os.Stat(path + "-wal"); err == nil {
		out["wal_size_bytes"] = st.Size()
	}
	if db == nil {
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return out, err
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err == nil && !strings.Contains(n, "_fts_") {
			names = append(names, n)
		}
	}
	rows.Close()
	counts := map[string]any{}
	for _, n := range names {
		var c int64
		q := `SELECT COUNT(*) FROM "` + strings.ReplaceAll(n, `"`, `""`) + `"`
		if err := db.QueryRowContext(ctx, q).Scan(&c); err != nil {
			counts[n] = "unreadable: " + err.Error()
			continue
		}
		counts[n] = c
	}
	out["table_rows"] = counts

	var check string
	if err := db.QueryRowContext(ctx, `PRAGMA quick_check(1)`).Scan(&check); err != nil {
		check = "not run: " + err.Error()
	}
	out["quick_check"] = check

	if mrows, err := db.QueryContext(ctx, `SELECT COALESCE(status,''), COALESCE(modality,''), COUNT(*) FROM memories GROUP BY 1, 2 ORDER BY 3 DESC LIMIT 40`); err == nil {
		type group struct {
			Status   string `json:"status"`
			Modality string `json:"modality"`
			Count    int64  `json:"count"`
		}
		var groups []group
		for mrows.Next() {
			var g group
			if mrows.Scan(&g.Status, &g.Modality, &g.Count) == nil {
				groups = append(groups, g)
			}
		}
		mrows.Close()
		out["memories_by_status"] = groups
	}
	return out, nil
}
