package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// MinHeartbeatInterval is the minimum allowable interval in seconds (60s).
	MinHeartbeatInterval = 60
	// DefaultHeartbeatInterval is 1800s (30 minutes).
	DefaultHeartbeatInterval = 1800
	// EmptyGoalMessage is injected when GOAL.md is empty.
	EmptyGoalMessage = "当前没有待办事项"
)

// HeartbeatConfig represents heartbeat configuration stored in heartbeat.json.
type HeartbeatConfig struct {
	Interval int  `json:"interval"`
	Active   bool `json:"active"`
}

// HeartbeatManager provides proactive idle-driven background inspection and goal tracking,
// ported and enhanced from PurrCat's HeartbeatManager.
type HeartbeatManager struct {
	mu           sync.RWMutex
	cfg          HeartbeatConfig
	cfgPath      string
	goalPath     string
	lastMtime    time.Time
	isIdle       bool
	idleSince    time.Time
	stopCh       chan struct{}
	running      bool
	onHeartbeat  func(goalText string)
}

// NewHeartbeatManager creates a new HeartbeatManager with configuration in dir.
func NewHeartbeatManager(dir string, onHeartbeat func(goalText string)) *HeartbeatManager {
	cfgPath := filepath.Join(dir, "heartbeat.json")
	goalPath := filepath.Join(dir, "GOAL.md")

	hm := &HeartbeatManager{
		cfg:         HeartbeatConfig{Interval: DefaultHeartbeatInterval, Active: false},
		cfgPath:     cfgPath,
		goalPath:    goalPath,
		isIdle:      true,
		idleSince:   time.Now(),
		stopCh:      make(chan struct{}),
		onHeartbeat: onHeartbeat,
	}
	hm.ensureConfigFile()
	return hm
}

func (h *HeartbeatManager) ensureConfigFile() {
	if _, err := os.Stat(h.cfgPath); os.IsNotExist(err) {
		h.writeConfig(HeartbeatConfig{Interval: DefaultHeartbeatInterval, Active: false})
	}
}

func (h *HeartbeatManager) writeConfig(cfg HeartbeatConfig) error {
	if cfg.Interval < MinHeartbeatInterval {
		cfg.Interval = MinHeartbeatInterval
	}
	tmp := h.cfgPath + ".tmp"
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, h.cfgPath)
}

// GetConfig returns the latest config, automatically reloading if file mtime changed.
func (h *HeartbeatManager) GetConfig() HeartbeatConfig {
	h.mu.Lock()
	defer h.mu.Unlock()

	fi, err := os.Stat(h.cfgPath)
	if err == nil {
		if fi.ModTime().After(h.lastMtime) {
			if data, err := os.ReadFile(h.cfgPath); err == nil {
				var parsed HeartbeatConfig
				if err := json.Unmarshal(data, &parsed); err == nil {
					if parsed.Interval < MinHeartbeatInterval {
						parsed.Interval = MinHeartbeatInterval
					}
					h.cfg = parsed
					h.lastMtime = fi.ModTime()
				}
			}
		}
	}
	return h.cfg
}

// UpdateConfig updates the heartbeat configuration and writes it to disk.
func (h *HeartbeatManager) UpdateConfig(cfg HeartbeatConfig) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if cfg.Interval < MinHeartbeatInterval {
		cfg.Interval = MinHeartbeatInterval
	}
	if err := h.writeConfig(cfg); err != nil {
		return err
	}
	h.cfg = cfg
	if fi, err := os.Stat(h.cfgPath); err == nil {
		h.lastMtime = fi.ModTime()
	}
	return nil
}

// ReadGoal reads the content of GOAL.md.
func (h *HeartbeatManager) ReadGoal() string {
	data, err := os.ReadFile(h.goalPath)
	if err != nil {
		return EmptyGoalMessage
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return EmptyGoalMessage
	}
	return s
}

// WriteGoal updates the content of GOAL.md.
func (h *HeartbeatManager) WriteGoal(goalContent string) error {
	return os.WriteFile(h.goalPath, []byte(strings.TrimSpace(goalContent)), 0644)
}

// SetIdle notifies the HeartbeatManager of the Agent's idle/busy state.
// Heartbeat timing only advances while the agent is in idle state.
func (h *HeartbeatManager) SetIdle(idle bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.isIdle && idle {
		h.idleSince = time.Now()
	}
	h.isIdle = idle
}

// Start begins the heartbeat daemon.
func (h *HeartbeatManager) Start() {
	h.mu.Lock()
	if h.running {
		h.mu.Unlock()
		return
	}
	h.running = true
	h.stopCh = make(chan struct{})
	h.mu.Unlock()

	go h.loop()
}

// Stop terminates the heartbeat daemon.
func (h *HeartbeatManager) Stop() {
	h.mu.Lock()
	if !h.running {
		h.mu.Unlock()
		return
	}
	h.running = false
	close(h.stopCh)
	h.mu.Unlock()
}

func (h *HeartbeatManager) loop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-h.stopCh:
			return
		case <-ticker.C:
			cfg := h.GetConfig()
			if !cfg.Active {
				continue
			}

			h.mu.RLock()
			idle := h.isIdle
			since := h.idleSince
			h.mu.RUnlock()

			if !idle {
				continue
			}

			elapsed := time.Since(since)
			if elapsed >= time.Duration(cfg.Interval)*time.Second {
				// Trigger heartbeat
				goal := h.ReadGoal()
				if h.onHeartbeat != nil {
					h.onHeartbeat(goal)
				}
				// Reset idle timer
				h.mu.Lock()
				h.idleSince = time.Now()
				h.mu.Unlock()
			}
		}
	}
}
