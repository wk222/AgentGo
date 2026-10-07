package crushproto

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

func (s *Server) routes(mux *http.ServeMux) {
	// server level
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, nil) })
	mux.HandleFunc("GET /v1/version", s.fixtureHandler("version"))
	mux.HandleFunc("GET /v1/config", s.globalConfig)
	mux.HandleFunc("DELETE /v1/clients/{cid}", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, nil) })
	mux.HandleFunc("POST /v1/control", func(w http.ResponseWriter, r *http.Request) {
		// Refuse every control command ("shutdown_if_idle"...): the client keeps using us.
		writeErr(w, http.StatusConflict, "crushproto does not accept control commands")
	})

	// workspaces
	mux.HandleFunc("GET /v1/workspaces", s.listWorkspaces)
	mux.HandleFunc("POST /v1/workspaces", s.createWorkspace)
	mux.HandleFunc("GET /v1/workspaces/{id}", s.getWorkspace)
	mux.HandleFunc("DELETE /v1/workspaces/{id}", s.deleteWorkspace)
	mux.HandleFunc("POST /v1/workspaces/{id}/current-session", s.okForWorkspace)
	mux.HandleFunc("GET /v1/workspaces/{id}/config", s.workspaceConfig)
	mux.HandleFunc("GET /v1/workspaces/{id}/providers", s.workspaceProviders)
	mux.HandleFunc("GET /v1/workspaces/{id}/events", s.events)

	// sessions & messages
	mux.HandleFunc("GET /v1/workspaces/{id}/sessions", s.listSessions)
	mux.HandleFunc("POST /v1/workspaces/{id}/sessions", s.createSession)
	mux.HandleFunc("GET /v1/workspaces/{id}/sessions/{sid}", s.getSession)
	mux.HandleFunc("PUT /v1/workspaces/{id}/sessions/{sid}", s.saveSession)
	mux.HandleFunc("DELETE /v1/workspaces/{id}/sessions/{sid}", s.deleteSession)
	mux.HandleFunc("GET /v1/workspaces/{id}/sessions/{sid}/history", s.listHistory)
	mux.HandleFunc("GET /v1/workspaces/{id}/sessions/{sid}/messages", s.listMessages)
	mux.HandleFunc("GET /v1/workspaces/{id}/sessions/{sid}/messages/user", s.listUserMessages)
	mux.HandleFunc("GET /v1/workspaces/{id}/messages/user", s.listUserMessages)
	mux.HandleFunc("GET /v1/workspaces/{id}/sessions/{sid}/filetracker/files", s.emptyList)
	mux.HandleFunc("POST /v1/workspaces/{id}/filetracker/read", s.okForWorkspace)

	// agent
	mux.HandleFunc("GET /v1/workspaces/{id}/agent", s.agentStatus)
	mux.HandleFunc("POST /v1/workspaces/{id}/agent", s.agentRun)
	mux.HandleFunc("POST /v1/workspaces/{id}/agent/init", s.okForWorkspace)
	mux.HandleFunc("POST /v1/workspaces/{id}/agent/update", s.postWorkspaceAgentUpdate)
	mux.HandleFunc("POST /v1/workspaces/{id}/agent/main", s.okForWorkspace)
	mux.HandleFunc("GET /v1/workspaces/{id}/agent/sessions/{sid}", s.getSession)
	mux.HandleFunc("POST /v1/workspaces/{id}/agent/sessions/{sid}/cancel", s.agentCancel)
	mux.HandleFunc("GET /v1/workspaces/{id}/agent/sessions/{sid}/prompts/queued", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, 0) })
	mux.HandleFunc("GET /v1/workspaces/{id}/agent/sessions/{sid}/prompts/list", s.emptyList)
	mux.HandleFunc("POST /v1/workspaces/{id}/agent/sessions/{sid}/prompts/clear", s.okForWorkspace)
	mux.HandleFunc("POST /v1/workspaces/{id}/agent/sessions/{sid}/summarize", s.okForWorkspace)
	mux.HandleFunc("GET /v1/workspaces/{id}/agent/default-small-model", s.fixtureHandler("default_small_model"))

	// permissions & questions
	mux.HandleFunc("GET /v1/workspaces/{id}/permissions/skip", s.getSkip)
	mux.HandleFunc("POST /v1/workspaces/{id}/permissions/skip", s.setSkip)
	mux.HandleFunc("POST /v1/workspaces/{id}/permissions/grant", s.grant)
	mux.HandleFunc("POST /v1/workspaces/{id}/questions/answer", s.okForWorkspace)
	mux.HandleFunc("POST /v1/workspaces/{id}/questions/cancel", s.okForWorkspace)

	// config mutations
	mux.HandleFunc("POST /v1/workspaces/{id}/config/model", s.postWorkspaceConfigModel)
	for _, p := range []string{"set", "remove", "compact", "provider-key", "refresh-oauth"} {
		mux.HandleFunc("POST /v1/workspaces/{id}/config/"+p, s.okForWorkspace)
	}

	// project / git / skills / lsp / mcp (static stand-ins)
	// AgentGo owns project context (no AGENTS.md bootstrap dialog in the TUI).
	mux.HandleFunc("GET /v1/workspaces/{id}/project/needs-init", func(w http.ResponseWriter, r *http.Request) {
		if s.workspace(w, r) != nil {
			writeJSON(w, 200, map[string]bool{"needs_init": false})
		}
	})
	mux.HandleFunc("POST /v1/workspaces/{id}/project/init", s.okForWorkspace)
	mux.HandleFunc("GET /v1/workspaces/{id}/project/init-prompt", s.fixtureHandler("init_prompt"))
	mux.HandleFunc("GET /v1/workspaces/{id}/git/branch", s.fixtureHandler("git_branch"))
	mux.HandleFunc("GET /v1/workspaces/{id}/skills", s.fixtureHandler("skills"))
	mux.HandleFunc("GET /v1/workspaces/{id}/lsps", s.listLSPs)
	mux.HandleFunc("GET /v1/workspaces/{id}/lsps/{lsp}/diagnostics", s.getLSPDiagnostics)
	mux.HandleFunc("POST /v1/workspaces/{id}/lsps/start", s.startLSP)
	mux.HandleFunc("POST /v1/workspaces/{id}/lsps/stop", s.stopAllLSPs)
	mux.HandleFunc("GET /v1/workspaces/{id}/mcp/states", s.fixtureHandler("mcp_states"))
	mux.HandleFunc("GET /v1/workspaces/{id}/mcp/pending-auth", s.fixtureHandler("mcp_pending_auth"))
	mux.HandleFunc("GET /v1/workspaces/{id}/mcp/disabled", s.fixtureHandler("mcp_disabled"))
	mux.HandleFunc("GET /v1/workspaces/{id}/mcp/enabled", s.fixtureHandler("mcp_enabled"))
	mux.HandleFunc("GET /v1/workspaces/{id}/mcp/prompts", s.fixtureHandler("mcp_prompts"))
}

func (s *Server) fixtureHandler(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "" && s.workspace(w, r) == nil {
			return
		}
		f, err := loadFixture(name)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.Status)
		_, _ = w.Write(f.Body)
	}
}

func (s *Server) okForWorkspace(w http.ResponseWriter, r *http.Request) {
	if s.workspace(w, r) != nil {
		writeJSON(w, 200, nil)
	}
}

func (s *Server) emptyList(w http.ResponseWriter, r *http.Request) {
	if s.workspace(w, r) != nil {
		writeJSON(w, 200, []any{})
	}
}

func (s *Server) listLSPs(w http.ResponseWriter, r *http.Request) {
	ws := s.workspace(w, r)
	if ws == nil {
		return
	}
	if s.opts.LSPProvider != nil {
		states := s.opts.LSPProvider.LSPStates(ws.path)
		if states != nil {
			writeJSON(w, 200, states)
			return
		}
	}
	s.fixtureHandler("lsps")(w, r)
}

func (s *Server) getLSPDiagnostics(w http.ResponseWriter, r *http.Request) {
	ws := s.workspace(w, r)
	if ws == nil {
		return
	}
	lspName := r.PathValue("lsp")
	if s.opts.LSPProvider != nil {
		diags := s.opts.LSPProvider.LSPDiagnostics(ws.path, lspName)
		if diags != nil {
			writeJSON(w, 200, diags)
			return
		}
	}
	writeJSON(w, 200, map[string]any{})
}

func (s *Server) startLSP(w http.ResponseWriter, r *http.Request) {
	ws := s.workspace(w, r)
	if ws == nil {
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	_ = readJSON(r, &req)
	if s.opts.LSPProvider != nil && req.Path != "" {
		s.opts.LSPProvider.StartLSP(r.Context(), ws.path, req.Path)
	}
	writeJSON(w, 200, nil)
}

func (s *Server) stopAllLSPs(w http.ResponseWriter, r *http.Request) {
	ws := s.workspace(w, r)
	if ws == nil {
		return
	}
	if s.opts.LSPProvider != nil {
		s.opts.LSPProvider.StopAllLSPs(r.Context(), ws.path)
	}
	writeJSON(w, 200, nil)
}

// ---- workspaces ------------------------------------------------------------

func (s *Server) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path    string `json:"path"`
		DataDir string `json:"data_dir"`
		Version string `json:"version"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request: "+err.Error())
		return
	}
	// One workspace per project path, shared by every client attached to it
	// (e.g. a TUI and a `crush run`); it goes away with its last client.
	s.mu.Lock()
	for _, ex := range s.wss {
		if pathKey(ex.path) == pathKey(req.Path) {
			ex.refs++
			s.mu.Unlock()
			writeJSON(w, 200, s.workspaceBody(ex))
			return
		}
	}
	ws := newWorkspace(newID(), req.Path, req.DataDir)
	ws.refs = 1
	ws.skipAll = s.opts.SkipPermissions
	ws.store, ws.logf = s.opts.Store, s.opts.Logf
	ws.restore()
	s.wss[ws.id] = ws
	s.mu.Unlock()

	if s.opts.LSPProvider != nil {
		s.opts.LSPProvider.SetLSPEventListener(ws.path, func(evType, name string, state int, diagCount int) {
			ws.publish("lsp_event", "updated", map[string]any{
				"type":             evType,
				"name":             name,
				"state":            state,
				"diagnostic_count": diagCount,
			})
		})
	}

	writeJSON(w, 200, s.workspaceBody(ws))
}

// workspaceBody is the recorded workspace payload re-addressed to ws.
func (s *Server) workspaceBody(ws *workspace) map[string]any {
	m, err := fixtureMap("workspace")
	if err != nil {
		return map[string]any{"id": ws.id, "path": ws.path, "data_dir": ws.dataDir}
	}
	m["id"], m["path"], m["data_dir"] = ws.id, ws.path, ws.dataDir
	if cfg, ok := m["config"].(map[string]any); ok {
		s.overlayModels(cfg, ws.path) // the TUI reads its model picker from here
		if opts, ok := cfg["options"].(map[string]any); ok && ws.dataDir != "" {
			opts["data_directory"] = ws.dataDir
		}
	}
	return m
}

func (s *Server) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	// workspaceBody takes s.mu (via activeModel): never call it with s.mu held.
	// workspaceBody takes s.mu (via activeModel): never call it with s.mu held.
	// workspaceBody takes s.mu (via activeModel): never call it with s.mu held.
	s.mu.Lock()
	list := make([]*workspace, 0, len(s.wss))
	for _, ws := range s.wss {
		list = append(list, ws)
	}
	s.mu.Unlock()
	out := make([]any, 0, len(list))
	for _, ws := range list {
		out = append(out, s.workspaceBody(ws))
	}
	writeJSON(w, 200, out)
}

func (s *Server) getWorkspace(w http.ResponseWriter, r *http.Request) {
	if ws := s.workspace(w, r); ws != nil {
		writeJSON(w, 200, s.workspaceBody(ws))
	}
}

func (s *Server) deleteWorkspace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	ws := s.wss[id]
	if ws != nil {
		if ws.refs--; ws.refs > 0 { // other clients still attached
			s.mu.Unlock()
			ws.flush()
			writeJSON(w, 200, nil)
			return
		}
		delete(s.wss, id)
	}
	s.mu.Unlock()
	if ws == nil {
		writeErr(w, 404, "workspace not found")
		return
	}
	ws.mu.Lock()
	for _, c := range ws.cancels {
		c()
	}
	ws.mu.Unlock()
	ws.flush() // the project's sessions outlive the workspace object
	writeJSON(w, 200, nil)
}

// events streams every workspace event as Server-Sent Events.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	ws := s.workspace(w, r)
	if ws == nil {
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "streaming unsupported")
		return
	}
	ch, replay, cancel := ws.subscribe()
	defer cancel()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(200)
	for _, frame := range replay {
		fmt.Fprintf(w, "data: %s\n\n", frame)
	}
	fl.Flush()
	for {
		select {
		case frame := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", frame)
			fl.Flush()
		case <-r.Context().Done():
			return
		case <-s.base.Done():
			return
		}
	}
}

// ---- sessions & messages ---------------------------------------------------

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	if ws := s.workspace(w, r); ws != nil {
		writeJSON(w, 200, ws.listSessions())
	}
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	ws := s.workspace(w, r)
	if ws == nil {
		return
	}
	var req Session
	_ = readJSON(r, &req)
	sess := ws.createSession(req.Title)
	ws.publish("session", "created", sess)
	writeJSON(w, 200, sess)
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	ws := s.workspace(w, r)
	if ws == nil {
		return
	}
	sess, ok := ws.session(r.PathValue("sid"))
	if !ok {
		writeErr(w, 404, "session not found")
		return
	}
	writeJSON(w, 200, sess)
}

func (s *Server) saveSession(w http.ResponseWriter, r *http.Request) {
	ws := s.workspace(w, r)
	if ws == nil {
		return
	}
	var req Session
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	id := r.PathValue("sid")
	if _, ok := ws.session(id); !ok {
		writeErr(w, 404, "session not found")
		return
	}
	ws.updateSession(id, func(x *Session) { x.Title = req.Title })
	sess, _ := ws.session(id)
	writeJSON(w, 200, sess)
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	ws := s.workspace(w, r)
	if ws == nil {
		return
	}
	id := r.PathValue("sid")
	sess, _ := ws.session(id)
	if !ws.deleteSession(id) {
		writeErr(w, 404, "session not found")
		return
	}
	ws.publish("session", "deleted", sess)
	writeJSON(w, 200, nil)
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	if ws := s.workspace(w, r); ws != nil {
		writeJSON(w, 200, ws.messages(r.PathValue("sid")))
	}
}

func (s *Server) listUserMessages(w http.ResponseWriter, r *http.Request) {
	if ws := s.workspace(w, r); ws != nil {
		out := ws.userMessages(r.PathValue("sid"))
		if out == nil {
			out = []*Message{}
		}
		writeJSON(w, 200, out)
	}
}

// ---- agent -----------------------------------------------------------------

func (s *Server) activeModel(workDir string) (model, provider string) {
	s.mu.Lock()
	model, provider = s.model, s.provider
	s.mu.Unlock()
	if s.opts.ModelProvider != nil {
		info := s.opts.ModelProvider.CurrentModel(workDir)
		if info.Model != "" {
			model = info.Model
		}
		if info.Provider != "" {
			provider = info.Provider
		}
	}
	return model, provider
}

func (s *Server) agentStatus(w http.ResponseWriter, r *http.Request) {
	ws := s.workspace(w, r)
	if ws == nil {
		return
	}
	m, err := fixtureMap("agent")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	model, provider := s.activeModel(ws.path)
	if mod, ok := m["model"].(map[string]any); ok {
		mod["id"] = model
		mod["name"] = model
	}
	if mc, ok := m["model_cfg"].(map[string]any); ok {
		mc["model"] = model
		mc["provider"] = provider
	}
	ws.mu.Lock()
	m["is_busy"] = len(ws.cancels) > 0
	ws.mu.Unlock()
	writeJSON(w, 200, m)
}

func (s *Server) agentRun(w http.ResponseWriter, r *http.Request) {
	ws := s.workspace(w, r)
	if ws == nil {
		return
	}
	var req RunRequest
	if err := readJSON(r, &req); err != nil || req.SessionID == "" {
		writeErr(w, 400, "session_id and prompt are required")
		return
	}
	if _, ok := ws.session(req.SessionID); !ok {
		writeErr(w, 404, "session not found")
		return
	}
	ctx, cancel := context.WithCancel(s.base)
	ws.mu.Lock()
	if ws.cancels[req.SessionID] != nil {
		ws.mu.Unlock()
		cancel()
		writeErr(w, 409, "session is busy")
		return
	}
	ws.cancels[req.SessionID] = cancel
	ws.mu.Unlock()

	model, provider := s.activeModel(ws.path)
	em := &Emitter{ctx: ctx, ws: ws, sessionID: req.SessionID, model: model, provider: provider}
	w.WriteHeader(http.StatusAccepted)
	go func() {
		err := s.runner.Run(ctx, req, em)
		if err != nil && ctx.Err() == nil {
			s.opts.Logf("run %s failed: %v", req.RunID, err)
		}
		ws.mu.Lock()
		delete(ws.cancels, req.SessionID)
		ws.mu.Unlock()
		ws.updateSession(req.SessionID, func(*Session) {}) // publish is_busy=false
		cancel()
	}()
}

func (s *Server) agentCancel(w http.ResponseWriter, r *http.Request) {
	ws := s.workspace(w, r)
	if ws == nil {
		return
	}
	ws.mu.Lock()
	cancel := ws.cancels[r.PathValue("sid")]
	ws.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	writeJSON(w, 200, nil)
}

// ---- permissions -----------------------------------------------------------

func (s *Server) getSkip(w http.ResponseWriter, r *http.Request) {
	if ws := s.workspace(w, r); ws != nil {
		ws.mu.Lock()
		skip := ws.skipAll
		ws.mu.Unlock()
		writeJSON(w, 200, map[string]bool{"skip": skip})
	}
}

func (s *Server) setSkip(w http.ResponseWriter, r *http.Request) {
	ws := s.workspace(w, r)
	if ws == nil {
		return
	}
	var req struct {
		Skip bool `json:"skip"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ws.mu.Lock()
	ws.skipAll = req.Skip
	ws.mu.Unlock()
	writeJSON(w, 200, nil)
}

// grant resolves a pending permission. Both "allow" and "allow_session" grant;
// "deny" refuses. resolved=false means nobody was waiting (already decided).
func (s *Server) grant(w http.ResponseWriter, r *http.Request) {
	ws := s.workspace(w, r)
	if ws == nil {
		return
	}
	var req struct {
		Action     string            `json:"action"`
		Permission PermissionRequest `json:"permission"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ws.mu.Lock()
	ch := ws.pending[req.Permission.ID]
	delete(ws.pending, req.Permission.ID)
	ws.mu.Unlock()
	if ch == nil {
		writeJSON(w, 200, map[string]bool{"resolved": false})
		return
	}
	ch <- req.Action == "allow" || req.Action == "allow_session"
	writeJSON(w, 200, map[string]bool{"resolved": true})
}

// ---- config & models -------------------------------------------------------

// overlayModels rewrites a Crush config object so the active models and the
// provider list reflect the runtime instead of the recorded fixture.
func (s *Server) overlayModels(cfg map[string]any, workDir string) {
	model, provider := s.activeModel(workDir)
	models, _ := cfg["models"].(map[string]any)
	if models == nil {
		models = map[string]any{}
		cfg["models"] = models
	}
	for _, kind := range []string{"large", "small"} {
		sel, _ := models[kind].(map[string]any)
		if sel == nil {
			sel = map[string]any{}
			models[kind] = sel
		}
		sel["model"], sel["provider"] = model, provider
	}
	// The picker lists cfg.Providers first: replace the recorded Azure copy
	// with what the runtime really offers.
	if s.opts.ModelProvider == nil {
		return
	}
	byID := map[string]any{}
	for _, p := range s.opts.ModelProvider.AvailableProviders(workDir) {
		if pm, ok := p.(map[string]any); ok {
			if id, _ := pm["id"].(string); id != "" {
				byID[id] = pm
			}
		}
	}
	if len(byID) > 0 {
		cfg["providers"] = byID
	}
}

func (s *Server) globalConfig(w http.ResponseWriter, r *http.Request) {
	m, err := fixtureMap("global_config")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.overlayModels(m, "")
	writeJSON(w, 200, m)
}

func (s *Server) workspaceConfig(w http.ResponseWriter, r *http.Request) {
	ws := s.workspace(w, r)
	if ws == nil {
		return
	}
	m, err := fixtureMap("ws_config")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.overlayModels(m, ws.path)
	writeJSON(w, 200, m)
}
func (s *Server) workspaceProviders(w http.ResponseWriter, r *http.Request) {
	ws := s.workspace(w, r)
	if ws == nil {
		return
	}
	if s.opts.ModelProvider != nil {
		if list := s.opts.ModelProvider.AvailableProviders(ws.path); len(list) > 0 {
			writeJSON(w, 200, list)
			return
		}
	}
	s.fixtureHandler("providers")(w, r)
}

type configModelReq struct {
	Scope     int    `json:"scope"`
	ModelType string `json:"model_type"`
	Model     struct {
		Model    string `json:"model"`
		Provider string `json:"provider"`
	} `json:"model"`
}

func (s *Server) postWorkspaceConfigModel(w http.ResponseWriter, r *http.Request) {
	ws := s.workspace(w, r)
	if ws == nil {
		return
	}
	var req configModelReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "invalid config model request: "+err.Error())
		return
	}
	model := strings.TrimSpace(req.Model.Model)
	provider := strings.TrimSpace(req.Model.Provider)
	if model == "" {
		writeJSON(w, 200, nil)
		return
	}
	if s.opts.ModelProvider != nil {
		if err := s.opts.ModelProvider.SetModel(r.Context(), ws.path, model, provider); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	} else {
		s.mu.Lock()
		s.model = model
		if provider != "" {
			s.provider = provider
		}
		s.mu.Unlock()
	}
	ws.publish("config_changed", "updated", map[string]any{"workspace_id": ws.id})
	writeJSON(w, 200, nil)
}

func (s *Server) postWorkspaceAgentUpdate(w http.ResponseWriter, r *http.Request) {
	ws := s.workspace(w, r)
	if ws == nil {
		return
	}
	ws.publish("config_changed", "updated", map[string]any{"workspace_id": ws.id})
	writeJSON(w, 200, nil)
}
