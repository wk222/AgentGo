package capability

import (
	"fmt"
	"sync"
	"time"
)

// Grant represents a registered capability asset (tool/skill/agent/channel/workflow/app/memory).
type Grant struct {
	ID             string            `json:"id"`
	Kind           string            `json:"kind"` // tool, skill, agent, channel, workflow, app, memory
	Name           string            `json:"name"`
	Scope          string            `json:"scope"`
	Version        string            `json:"version"`
	Status         AssetStatus       `json:"status"`
	Owner          string            `json:"owner"`
	Source         string            `json:"source"`
	RiskLevel      string            `json:"risk_level"`
	Reusable       bool              `json:"reusable"`
	Recommended    bool              `json:"recommended"`
	SupersedesID   string            `json:"supersedes_id,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	LastVerifiedAt int64             `json:"last_verified_at,omitempty"`
	VerifyResult   string            `json:"verify_result,omitempty"` // pass, fail, skip
	CreatedAt      int64             `json:"created_at"`
	UpdatedAt      int64             `json:"updated_at"`
}

// Bus is a capability registry with optional SQLite persistence and event fan-out.
type Bus struct {
	mu          sync.RWMutex
	store       *Store
	grants      map[string]Grant
	subscribers []Subscriber
}

func NewBus() *Bus {
	return &Bus{grants: make(map[string]Grant)}
}

func NewBusWithStore(store *Store) *Bus {
	b := NewBus()
	b.store = store
	if store != nil {
		if rows, err := store.LoadAll(); err == nil {
			for _, g := range rows {
				b.grants[g.ID] = g
			}
		}
	}
	return b
}

func (b *Bus) Register(kind, name, scope string, meta map[string]string) Grant {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := kind + ":" + name + ":" + scope
	existing, exists := b.grants[id]
	version := "1.0.0"
	status := StatusPublished
	var owner, source, riskLevel string
	reusable := true
	recommended := false
	var supersedesID string
	var lastVerifiedAt int64
	var verifyResult string

	if exists {
		version = existing.Version
		status = existing.Status
		owner = existing.Owner
		source = existing.Source
		riskLevel = existing.RiskLevel
		reusable = existing.Reusable
		recommended = existing.Recommended
		supersedesID = existing.SupersedesID
		lastVerifiedAt = existing.LastVerifiedAt
		verifyResult = existing.VerifyResult
	} else {
		if kind == "tool" {
			riskLevel = "low"
			if name == "execute_bash" {
				riskLevel = "high"
			}
		}
		source = "system"
	}

	g := Grant{
		ID:             id,
		Kind:           kind,
		Name:           name,
		Scope:          scope,
		Version:        version,
		Status:         status,
		Owner:          owner,
		Source:         source,
		RiskLevel:      riskLevel,
		Reusable:       reusable,
		Recommended:    recommended,
		SupersedesID:   supersedesID,
		Metadata:       meta,
		LastVerifiedAt: lastVerifiedAt,
		VerifyResult:   verifyResult,
		CreatedAt:      time.Now().Unix(),
		UpdatedAt:      time.Now().Unix(),
	}
	if exists {
		g.CreatedAt = existing.CreatedAt
	}
	b.grants[id] = g
	if b.store != nil {
		_ = b.store.Upsert(g)
	}
	return g
}

func (b *Bus) List(kind string) []Grant {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var out []Grant
	for _, g := range b.grants {
		if kind == "" || g.Kind == kind {
			out = append(out, g)
		}
	}
	return out
}

func (b *Bus) RecordMetric(comp, name string, durationMs float64, tokens int) {
	if b.store != nil {
		// Mock cost calculation (e.g. 0.000001 per token)
		cost := float64(tokens) * 0.000001
		_ = b.store.RecordMetric(MetricRecord{
			Component: comp,
			Name:      name,
			Duration:  durationMs,
			Tokens:    tokens,
			Cost:      cost,
		})
	}
}

func (b *Bus) SeedDefaults() {
	b.Register("tool", "execute_bash", "agent", map[string]string{"risk": "high"})
	b.Register("tool", "mcp_filesystem", "agent", nil)
	b.Register("skill", "code_review", "global", nil)
	b.Register("channel", "wechat", "integration", map[string]string{"status": "disabled"})
	b.Register("channel", "wecom", "integration", map[string]string{"status": "disabled"})
}

// Verify updates verification timestamp and result.
func (b *Bus) Verify(id string, result string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	g, ok := b.grants[id]
	if !ok {
		return
	}
	g.LastVerifiedAt = time.Now().Unix()
	g.VerifyResult = result
	g.UpdatedAt = time.Now().Unix()
	b.grants[id] = g
	if b.store != nil {
		_ = b.store.Upsert(g)
	}
}

// Transition performs state transition validation and updates status.
func (b *Bus) Transition(id string, to AssetStatus) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	g, ok := b.grants[id]
	if !ok {
		return fmt.Errorf("asset %s not found", id)
	}
	if err := AssertTransition(g.Status, to); err != nil {
		return err
	}
	g.Status = to
	g.UpdatedAt = time.Now().Unix()
	b.grants[id] = g
	if b.store != nil {
		_ = b.store.Upsert(g)
	}
	return nil
}

// Deprecate marks an asset as deprecated and links it to a replacement asset.
func (b *Bus) Deprecate(id string, supersededBy string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	g, ok := b.grants[id]
	if !ok {
		return fmt.Errorf("asset %s not found", id)
	}
	if err := AssertTransition(g.Status, StatusDeprecated); err != nil {
		return err
	}
	g.Status = StatusDeprecated
	g.SupersedesID = supersededBy
	g.UpdatedAt = time.Now().Unix()
	b.grants[id] = g
	if b.store != nil {
		_ = b.store.Upsert(g)
	}
	return nil
}

// ListByStatus returns grants filtered by status.
func (b *Bus) ListByStatus(status AssetStatus) []Grant {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var out []Grant
	for _, g := range b.grants {
		if g.Status == status {
			out = append(out, g)
		}
	}
	return out
}

// ListRecommended returns grants flagged as recommended.
func (b *Bus) ListRecommended() []Grant {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var out []Grant
	for _, g := range b.grants {
		if g.Recommended {
			out = append(out, g)
		}
	}
	return out
}

// Get returns a single grant by ID.
func (b *Bus) Get(id string) (Grant, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	g, ok := b.grants[id]
	return g, ok
}

// Delete removes a grant from in-memory map and SQL store.
func (b *Bus) Delete(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.grants, id)
	if b.store != nil {
		return b.store.Delete(id)
	}
	return nil
}
