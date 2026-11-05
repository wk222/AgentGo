package bridge

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"agentgo/internal/agent"
	"agentgo/internal/experts"
	"agentgo/internal/sessions"
)

// Experts: roles a conversation can run as (internal/experts). This file is the
// runtime side: where the files are, how a conversation's expert reaches the
// agent, and what the conversation_create / list_experts tools do.

// Opening conversations is cheap for a person and a runaway for a loop, so one
// conversation may open only a few in a while, and the whole runtime not many more.
const (
	convCreateWindow      = 30 * time.Minute
	convCreateSenderLimit = 5
	convCreateAllLimit    = 30
)

// expertOptions says where the user's and the workspace's experts live.
func (r *Runtime) expertOptions() experts.Options {
	r.mu.RLock()
	data, ws := r.dataDir, r.workspace
	r.mu.RUnlock()
	o := experts.Options{HasTool: r.hasTool}
	if strings.TrimSpace(data) != "" {
		o.UserDir = filepath.Join(data, "experts")
	}
	if strings.TrimSpace(ws) != "" {
		o.WorkspaceDir = filepath.Join(ws, ".agentgo", "experts")
	}
	return o
}

// hasTool says whether this runtime offers a tool. The switch for a tool plugin is
// not proof it is there (no browser installed, no workspace chosen), so official
// experts built around such tools are offered only when the tool exists.
func (r *Runtime) hasTool(name string) bool {
	if r.toolReg == nil {
		return false
	}
	_, ok := r.toolReg.Get(name)
	return ok
}

// expertLibrary reads the experts available now. It reads the folders each
// time, so an edited file applies to the next message.
func (r *Runtime) expertLibrary() experts.Catalog { return experts.Load(r.expertOptions()) }

// skillExists says whether a skill can be loaded now; an expert names only those.
func (r *Runtime) skillExists(name string) bool {
	r.mu.RLock()
	l := r.skillLoader
	r.mu.RUnlock()
	if l == nil {
		return false
	}
	_, ok := l.Get("skill:" + name)
	return ok
}

// expertResolver is what the runner asks at the start of every run: the
// system-prompt block of the expert this conversation is bound to. Anything that
// goes wrong (no binding, a file that is gone, a store error) means "no expert":
// a conversation must never fail to run because of its persona.
func (r *Runtime) expertResolver(store *sessions.Store) agent.ExpertResolver {
	return func(sessionID string) string {
		if store == nil {
			return ""
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		id, err := store.Expert(ctx, sessionID)
		if err != nil || id == "" {
			return ""
		}
		e, ok := r.expertLibrary().Get(id)
		if !ok {
			return ""
		}
		return e.Prompt(r.skillExists)
	}
}

// expertRow is how an expert is shown to the page. Rules are included only when asked for.
func (r *Runtime) expertRow(e experts.Expert, withRules bool) map[string]any {
	skills := make([]map[string]any, 0, len(e.Skills))
	for _, s := range e.Skills {
		skills = append(skills, map[string]any{"id": s, "available": r.skillExists(s)})
	}
	starters := e.Starters
	if starters == nil {
		starters = []string{}
	}
	row := map[string]any{
		"id": e.ID, "name": e.Name, "summary": e.Summary, "category": e.Category,
		"source": string(e.Source), "order": e.Order, "skills": skills, "starters": starters,
	}
	if e.Overrides != "" {
		row["overrides"] = string(e.Overrides)
	}
	if e.Path != "" {
		row["path"] = e.Path
	}
	if withRules {
		row["rules"] = e.Rules
	}
	return row
}

// ListExperts implements tools.ConversationHandler: a compact list for the model.
func (r *Runtime) ListExperts(_ context.Context) (any, error) {
	cat := r.expertLibrary()
	out := make([]map[string]any, 0, len(cat.Experts))
	for _, e := range cat.Experts {
		var skills []string
		for _, s := range e.Skills {
			if r.skillExists(s) {
				skills = append(skills, s)
			}
		}
		out = append(out, map[string]any{
			"id": e.ID, "name": e.Name, "summary": e.Summary, "category": e.Category, "source": string(e.Source), "skills": skills,
		})
	}
	return out, nil
}

// CreateConversation implements tools.ConversationHandler. It only creates: no
// message is sent, and nothing makes the user's screen follow.
func (r *Runtime) CreateConversation(ctx context.Context, fromID, name, expertKey string) (any, error) {
	st, err := r.sessionStore()
	if err != nil {
		return nil, err
	}
	title := sessions.CleanTitle(name)
	if title == "" {
		return nil, fmt.Errorf("name is required")
	}
	var chosen *experts.Expert
	if key := strings.TrimSpace(expertKey); key != "" {
		e, err := r.expertLibrary().Find(key)
		if err != nil {
			return nil, err
		}
		chosen = &e
	}
	// After the checks above, so a misspelled expert does not use up the allowance.
	if !r.convCreateRate.allow(time.Now(), convCreateWindow,
		rateRule{key: "from:" + fromID, limit: convCreateSenderLimit},
		rateRule{key: "all", limit: convCreateAllLimit}) {
		return nil, fmt.Errorf("too many conversations opened recently (limit %d per conversation per %d minutes); use one of the ones that exist (session_message action=list)",
			convCreateSenderLimit, int(convCreateWindow/time.Minute))
	}
	sess, err := st.Create(ctx, title)
	if err != nil {
		return nil, err
	}
	res := map[string]any{"id": sess.ID, "title": sess.Title, "expert": nil}
	if chosen != nil {
		if err := st.SetExpert(ctx, sess.ID, chosen.ID); err != nil {
			_ = st.Delete(ctx, sess.ID) // no half-made conversation
			return nil, fmt.Errorf("could not set the expert: %w", err)
		}
		res["expert"] = map[string]any{"id": chosen.ID, "name": chosen.Name}
	}
	return res, nil
}
