// Package datahome 给 IM 渠道层提供数据目录（channels.yml、微信登录凭据等）。
//
// octo 把它们放在 ~/.octo；agentgo 由运行时在启动时通过 SetDir 指向自己的数据目录，
// 未设置时回退到 ~/.agentgo。
package datahome

import (
	"os"
	"path/filepath"
	"sync"
)

var (
	mu  sync.RWMutex
	dir string
)

// SetDir 指定数据目录（空串表示恢复默认）。
func SetDir(d string) {
	mu.Lock()
	dir = d
	mu.Unlock()
}

// Dir 返回当前数据目录。
func Dir() (string, error) {
	mu.RLock()
	d := dir
	mu.RUnlock()
	if d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agentgo"), nil
}

// Path 返回数据目录下 name 的绝对路径。
func Path(name string) (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, name), nil
}
