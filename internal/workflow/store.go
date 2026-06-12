package workflow

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Definition is a Coze/PyFlow-like workflow JSON (nodes + edges).
type Definition struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Nodes       []Node         `json:"nodes"`
	Edges       []Edge         `json:"edges"`
	Meta        map[string]any `json:"meta,omitempty"`
	CreatedAt   int64          `json:"created_at"`
}

type Node struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"` // start, llm, tool, branch, end
	Title    string         `json:"title,omitempty"`
	Prompt   string         `json:"prompt,omitempty"`
	ToolName string         `json:"tool_name,omitempty"`
	ArgsJSON string         `json:"args_json,omitempty"`
	Config   map[string]any `json:"config,omitempty"`
}

type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	When string `json:"when,omitempty"` // optional branch label: contains:foo, equals:bar
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) (*Store, error) {
	s := &Store{db: db}
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS workflows (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT,
  body TEXT NOT NULL,
  created_at INTEGER NOT NULL
)`)
	if err != nil {
		return nil, err
	}
	_, _ = s.db.Exec(`
CREATE TABLE IF NOT EXISTS workflow_runs (
  id TEXT PRIMARY KEY,
  workflow_id TEXT NOT NULL,
  input TEXT,
  output TEXT,
  status TEXT NOT NULL,
  error TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
)`)
	_, _ = s.db.Exec(`
CREATE TABLE IF NOT EXISTS workflow_run_steps (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL,
  node_id TEXT NOT NULL,
  node_type TEXT NOT NULL,
  status TEXT NOT NULL,
  input TEXT,
  output TEXT,
  error TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
)`)
	_ = s.SeedDefaults()
	return s, nil
}

// SeedDefaults populates example workflows showcasing Eino Compose Graphs (Ch.08).
func (s *Store) SeedDefaults() error {
	list, err := s.List()
	if err != nil || len(list) > 0 {
		return nil
	}

	def1 := Definition{
		ID:          "wf_translate_summarize",
		Name:        "智能翻译与纪要",
		Description: "将输入的中文文本翻译为地道英文，然后再提取出英文纪要列表 (Eino Compose 串行链示例)",
		Nodes: []Node{
			{ID: "start", Type: "start", Title: "开始"},
			{ID: "translator", Type: "llm", Title: "翻译官", Prompt: "请将以下中文文本翻译为地道流利的英文：\n\n{{input}}"},
			{ID: "summarizer", Type: "llm", Title: "纪要专家", Prompt: "Please summarize the following English text into a concise, professional bullet-point summary:\n\n{{last}}"},
			{ID: "end", Type: "end", Title: "结束"},
		},
		Edges: []Edge{
			{From: "start", To: "translator"},
			{From: "translator", To: "summarizer"},
			{From: "summarizer", To: "end"},
		},
	}
	_ = s.Save(def1)

	def2 := Definition{
		ID:          "wf_weather_advisor",
		Name:        "天气与出行助手",
		Description: "先使用 get_current_time 工具获取当前时间，然后结合时间信息为用户提供纪律性出行建议 (Eino Compose 工具与大模型结合示例)",
		Nodes: []Node{
			{ID: "start", Type: "start", Title: "开始"},
			{ID: "get_time", Type: "tool", Title: "获取时间", ToolName: "get_current_time", ArgsJSON: "{}"},
			{ID: "advisor", Type: "llm", Title: "建议专家", Prompt: "当前的时间信息是：{{last}}。\n用户想了解出行地点或天气的信息，请根据时间制定贴心的出行、穿着与防晒/防雨指南，输入是：{{input}}"},
			{ID: "end", Type: "end", Title: "结束"},
		},
		Edges: []Edge{
			{From: "start", To: "get_time"},
			{From: "get_time", To: "advisor"},
			{From: "advisor", To: "end"},
		},
	}
	_ = s.Save(def2)

	return nil
}

func (s *Store) Save(def Definition) error {
	if def.ID == "" {
		def.ID = uuid.NewString()
	}
	if def.CreatedAt == 0 {
		def.CreatedAt = time.Now().Unix()
	}
	b, err := json.Marshal(def)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO workflows (id, name, description, body, created_at) VALUES (?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, description=excluded.description, body=excluded.body`,
		def.ID, def.Name, def.Description, string(b), def.CreatedAt)
	return err
}

func (s *Store) Get(id string) (Definition, error) {
	var body, name, desc string
	var created int64
	err := s.db.QueryRow(`SELECT name, description, body, created_at FROM workflows WHERE id=?`, id).
		Scan(&name, &desc, &body, &created)
	if err != nil {
		return Definition{}, err
	}
	var def Definition
	if err := json.Unmarshal([]byte(body), &def); err != nil {
		return Definition{}, err
	}
	def.ID = id
	return def, nil
}

func (s *Store) List() ([]Definition, error) {
	rows, err := s.db.Query(`SELECT id, name, description, body, created_at FROM workflows ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Definition
	for rows.Next() {
		var id, name, desc, body string
		var created int64
		if err := rows.Scan(&id, &name, &desc, &body, &created); err != nil {
			return nil, err
		}
		var def Definition
		_ = json.Unmarshal([]byte(body), &def)
		def.ID = id
		def.Name = name
		def.Description = desc
		def.CreatedAt = created
		out = append(out, def)
	}
	return out, rows.Err()
}

func (s *Store) GetFlowgram(id string) (FlowgramDocument, error) {
	def, err := s.Get(id)
	if err != nil {
		return FlowgramDocument{}, err
	}
	return FromDefinition(def), nil
}

func (s *Store) SaveFlowgram(id string, doc FlowgramDocument) error {
	name, desc := id, ""
	if existing, err := s.Get(id); err == nil {
		name, desc = existing.Name, existing.Description
	}
	def := doc.ToDefinition(name, desc)
	def.ID = id
	b, _ := json.Marshal(doc)
	if def.Meta == nil {
		def.Meta = map[string]any{}
	}
	def.Meta["flowgram_json"] = string(b)
	return s.Save(def)
}

func (s *Store) SaveFromRegister(name, description, nodesJSON string) (Definition, error) {
	var nodes []Node
	if nodesJSON != "" {
		_ = json.Unmarshal([]byte(nodesJSON), &nodes)
	}
	if len(nodes) == 0 {
		nodes = []Node{
			{ID: "start", Type: "start", Title: "开始"},
			{ID: "llm1", Type: "llm", Title: "LLM", Prompt: "{{input}}"},
			{ID: "end", Type: "end", Title: "结束"},
		}
	}
	def := Definition{
		Name: name, Description: description,
		Nodes: nodes,
		Edges: []Edge{{From: "start", To: "llm1"}, {From: "llm1", To: "end"}},
	}
	if err := s.Save(def); err != nil {
		return Definition{}, err
	}
	return def, nil
}

type RunRecord struct {
	ID         string `json:"id"`
	WorkflowID string `json:"workflow_id"`
	Input      string `json:"input"`
	Output     string `json:"output"`
	Status     string `json:"status"` // running, paused, completed, failed
	Error      string `json:"error,omitempty"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

type StepRecord struct {
	ID        string `json:"id"`
	RunID     string `json:"run_id"`
	NodeID    string `json:"node_id"`
	NodeType  string `json:"node_type"`
	Status    string `json:"status"` // running, completed, failed
	Input     string `json:"input"`
	Output    string `json:"output"`
	Error     string `json:"error,omitempty"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

func (s *Store) SaveRun(r RunRecord) error {
	if r.CreatedAt == 0 {
		r.CreatedAt = time.Now().Unix()
	}
	if r.UpdatedAt == 0 {
		r.UpdatedAt = r.CreatedAt
	}
	_, err := s.db.Exec(`INSERT INTO workflow_runs (id, workflow_id, input, output, status, error, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET output=excluded.output, status=excluded.status, error=excluded.error, updated_at=excluded.updated_at`,
		r.ID, r.WorkflowID, r.Input, r.Output, r.Status, r.Error, r.CreatedAt, r.UpdatedAt)
	return err
}

func (s *Store) SaveStep(sr StepRecord) error {
	if sr.CreatedAt == 0 {
		sr.CreatedAt = time.Now().Unix()
	}
	if sr.UpdatedAt == 0 {
		sr.UpdatedAt = sr.CreatedAt
	}
	_, err := s.db.Exec(`INSERT INTO workflow_run_steps (id, run_id, node_id, node_type, status, input, output, error, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET status=excluded.status, output=excluded.output, error=excluded.error, updated_at=excluded.updated_at`,
		sr.ID, sr.RunID, sr.NodeID, sr.NodeType, sr.Status, sr.Input, sr.Output, sr.Error, sr.CreatedAt, sr.UpdatedAt)
	return err
}

func (s *Store) GetRun(runID string) (RunRecord, error) {
	var r RunRecord
	err := s.db.QueryRow(`SELECT id, workflow_id, input, output, status, error, created_at, updated_at FROM workflow_runs WHERE id=?`, runID).
		Scan(&r.ID, &r.WorkflowID, &r.Input, &r.Output, &r.Status, &r.Error, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

func (s *Store) ListRuns(workflowID string, limit int) ([]RunRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	var rows *sql.Rows
	var err error
	if workflowID != "" {
		rows, err = s.db.Query(`SELECT id, workflow_id, input, output, status, error, created_at, updated_at FROM workflow_runs WHERE workflow_id=? ORDER BY created_at DESC LIMIT ?`, workflowID, limit)
	} else {
		rows, err = s.db.Query(`SELECT id, workflow_id, input, output, status, error, created_at, updated_at FROM workflow_runs ORDER BY created_at DESC LIMIT ?`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunRecord
	for rows.Next() {
		var r RunRecord
		if err := rows.Scan(&r.ID, &r.WorkflowID, &r.Input, &r.Output, &r.Status, &r.Error, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *Store) GetSteps(runID string) ([]StepRecord, error) {
	rows, err := s.db.Query(`SELECT id, run_id, node_id, node_type, status, input, output, error, created_at, updated_at FROM workflow_run_steps WHERE run_id=? ORDER BY created_at ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StepRecord
	for rows.Next() {
		var sr StepRecord
		if err := rows.Scan(&sr.ID, &sr.RunID, &sr.NodeID, &sr.NodeType, &sr.Status, &sr.Input, &sr.Output, &sr.Error, &sr.CreatedAt, &sr.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, sr)
	}
	return out, nil
}
