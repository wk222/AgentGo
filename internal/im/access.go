package im

import (
	"fmt"
	"strings"
)

// 渠道机器人暴露在公网上：谁都能给它发消息。agentgo 能执行命令、读写文件，
// 所以默认拒绝所有人，必须在该实例的配置里显式列出 allow_from（用户 ID 或会话 ID），
// 或者明确写 allow_all: true。

// Access 是某个机器人实例的访问策略。
type Access struct {
	AllowAll  bool
	AllowFrom map[string]bool
}

// ParseAccess 从实例的 PlatformConfig 里读取 allow_all / allow_from。
// allow_from 可以是 YAML 列表，也可以是逗号/空白分隔的字符串。
func ParseAccess(cfg map[string]any) Access {
	a := Access{AllowFrom: map[string]bool{}}
	switch v := cfg["allow_all"].(type) {
	case bool:
		a.AllowAll = v
	case string:
		a.AllowAll = v == "true" || v == "yes" || v == "1"
	}
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			a.AllowFrom[s] = true
		}
	}
	switch v := cfg["allow_from"].(type) {
	case []any:
		for _, x := range v {
			add(fmt.Sprint(x))
		}
	case []string:
		for _, x := range v {
			add(x)
		}
	case string:
		for _, x := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' }) {
			add(x)
		}
	case int, int64, float64:
		add(fmt.Sprint(v))
	}
	return a
}

// Permits 报告发送者（userID）或所在会话（chatID）是否被允许。
func (a Access) Permits(userID, chatID string) bool {
	if a.AllowAll {
		return true
	}
	return (userID != "" && a.AllowFrom[userID]) || (chatID != "" && a.AllowFrom[chatID])
}
