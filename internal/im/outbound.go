package im

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

var (
	defaultMu  sync.RWMutex
	defaultMgr *Manager
)

// SetDefault 登记进程内运行的管理层（运行时启动渠道插件时调用），供 send_file 之类的工具使用。
func SetDefault(m *Manager) {
	defaultMu.Lock()
	defaultMgr = m
	defaultMu.Unlock()
}

// Default 返回正在运行的管理层，没有时返回 nil。
func Default() *Manager {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultMgr
}

// ErrNotIMSession 表示当前会话不是从 IM 渠道进来的，没有可回传的对象。
var ErrNotIMSession = errors.New("this session did not come from an IM channel, so there is no chat to send to")

// SendFileToSession 把本地文件发回会话对应的 IM 聊天。
func (m *Manager) SendFileToSession(sessionID, path, name string) error {
	inst, chatID, err := m.resolveSession(sessionID)
	if err != nil {
		return err
	}
	st, statErr := os.Stat(path)
	if statErr != nil {
		return fmt.Errorf("cannot read %s: %w", path, statErr)
	}
	if st.IsDir() {
		return fmt.Errorf("%s is a directory; send a file", path)
	}
	if res := inst.adapter.SendFile(chatID, path, name, ""); !res.OK {
		return fmt.Errorf("send file via %s: %s", inst.id, res.Error)
	}
	return nil
}

// SendTextToSession 向会话对应的 IM 聊天发一段文字（比如长任务中途的进度）。
func (m *Manager) SendTextToSession(sessionID, text string) error {
	inst, chatID, err := m.resolveSession(sessionID)
	if err != nil {
		return err
	}
	if res := inst.adapter.SendText(chatID, text, ""); !res.OK {
		return fmt.Errorf("send text via %s: %s", inst.id, res.Error)
	}
	return nil
}

func (m *Manager) resolveSession(sessionID string) (*instance, string, error) {
	adapterID, chatID, ok := ParseSessionID(strings.TrimSpace(sessionID))
	if !ok {
		return nil, "", ErrNotIMSession
	}
	m.mu.Lock()
	inst := m.running[adapterID]
	m.mu.Unlock()
	if inst == nil {
		return nil, "", fmt.Errorf("IM instance %q is not running", adapterID)
	}
	return inst, chatID, nil
}
