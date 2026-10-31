package sessions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxTitleRunes bounds a title typed by the user, so one line in the sidebar stays one line.
	MaxTitleRunes = 80
	// ExportMaxMessages is how many of the newest messages an export holds.
	ExportMaxMessages = 5000

	quoteMaxRunes = 300
)

// ErrNotFound is returned for a session id that does not exist.
var ErrNotFound = errors.New("session not found")

// Get returns one session with its message count.
func (s *Store) Get(ctx context.Context, sessionID string) (Session, error) {
	var x Session
	err := s.db.QueryRowContext(ctx, `
		SELECT s.id, s.title, s.created_at, s.updated_at,
			(SELECT COUNT(*) FROM chat_messages m WHERE m.session_id = s.id)
		FROM chat_sessions s WHERE s.id = ?
	`, strings.TrimSpace(sessionID)).Scan(&x.ID, &x.Title, &x.CreatedAt, &x.UpdatedAt, &x.MessageCount)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return x, err
}

// CleanTitle turns what the user typed into a one-line title of at most MaxTitleRunes runes; "" if nothing is left.
func CleanTitle(raw string) string {
	title := strings.Join(strings.Fields(raw), " ")
	if utf8.RuneCountInString(title) > MaxTitleRunes {
		title = strings.TrimSpace(string([]rune(title)[:MaxTitleRunes]))
	}
	return title
}

// Rename gives a session the title the user chose. AutoTitleFromUserMessage only fills in default titles, so a
// chosen one stays. updated_at is left alone: renaming is no activity, and the sidebar is ordered by it.
func (s *Store) Rename(ctx context.Context, sessionID, title string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", errors.New("empty session id")
	}
	title = CleanTitle(title)
	if title == "" {
		return "", errors.New("title is empty")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE chat_sessions SET title = ? WHERE id = ?`, title, sessionID)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrNotFound
	}
	return title, nil
}

// Markdown renders a conversation as a document someone can open anywhere. Only what was said is in it: replies
// and prompts, plus a one-line note where an approval, a question or a card was. Reasoning and tool calls are not
// stored with the conversation, and arguments of approvals are left out on purpose, they can hold commands and
// paths. olderOmitted says the conversation is longer than what msgs holds.
func Markdown(sess Session, msgs []Message, exportedAt time.Time, olderOmitted bool) string {
	var blocks []string
	for _, m := range msgs {
		if block := renderMessage(m); block != "" {
			blocks = append(blocks, block)
		}
	}
	title := strings.TrimSpace(sess.Title)
	if title == "" {
		title = "对话"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", oneLine(title))
	fmt.Fprintf(&b, "> 导出自 AgentGo · %s · %d 条消息", exportedAt.Format("2006-01-02 15:04"), len(blocks))
	if olderOmitted {
		b.WriteString("（更早的消息未包含）")
	}
	b.WriteString("\n")
	for _, block := range blocks {
		b.WriteString("\n---\n\n")
		b.WriteString(block)
		b.WriteString("\n")
	}
	return b.String()
}

func renderMessage(m Message) string {
	when := ""
	if m.CreatedAt > 0 {
		when = " · " + time.Unix(m.CreatedAt, 0).Format("2006-01-02 15:04")
	}
	label := map[string]string{"user": "🧑 用户", "assistant": "🤖 助手", "system": "⚙️ 系统"}[m.Role]
	if label == "" {
		label = "💬 " + m.Role
	}

	switch m.Type {
	case "", "text":
		text := strings.TrimSpace(m.Content)
		note := ""
		if n := imageCount(m.MetaJSON); n > 0 {
			note = fmt.Sprintf("> 📎 附图 %d 张（图片未包含在导出中）\n\n", n)
		}
		if text == "" && note == "" {
			return ""
		}
		return fmt.Sprintf("**%s**%s\n\n%s%s\n", label, when, note, text)
	case "approval":
		return quote("🔒 请求批准："+metaString(m.MetaJSON, "tool_name"), when)
	case "question":
		return quote("❓ 智能体提问："+strings.TrimSpace(m.Content), when)
	case "aui":
		return quote("🧩 此处有一张界面卡片（导出时省略）", when)
	case "artifact":
		var r struct {
			Name    string `json:"name"`
			Version int    `json:"version"`
			Status  string `json:"status"`
		}
		if json.Unmarshal([]byte(m.Content), &r) != nil || r.Name == "" {
			return ""
		}
		if r.Status == "removed" {
			return quote(fmt.Sprintf("📦 已移除交付文件：%s（第 %d 版）", r.Name, r.Version), when)
		}
		return quote(fmt.Sprintf("📦 交付文件：%s（第 %d 版，文件本身未包含在导出中）", r.Name, r.Version), when)
	}
	return ""
}

func quote(text, when string) string {
	return "> " + oneLine(clipRunes(text, quoteMaxRunes)) + when + "\n"
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func metaString(metaJSON, key string) string {
	if metaJSON == "" {
		return ""
	}
	var meta map[string]any
	if json.Unmarshal([]byte(metaJSON), &meta) != nil {
		return ""
	}
	v, _ := meta[key].(string)
	return v
}

func imageCount(metaJSON string) int {
	if metaJSON == "" {
		return 0
	}
	var meta struct {
		Images []any `json:"images"`
	}
	if json.Unmarshal([]byte(metaJSON), &meta) != nil {
		return 0
	}
	return len(meta.Images)
}

// SafeFileName makes a title usable as the name of a file on Windows: characters it forbids become "_", the
// length is bounded and a name Windows reserves (CON, NUL, ...) is avoided. An empty result becomes fallback.
func SafeFileName(title, fallback string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(title) {
		switch {
		case unicode.IsSpace(r):
			b.WriteRune(' ')
		case r < 0x20 || strings.ContainsRune(`\/:*?"<>|`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	name := strings.Trim(strings.Join(strings.Fields(b.String()), " "), ". ")
	if utf8.RuneCountInString(name) > 60 {
		name = strings.TrimRight(string([]rune(name)[:60]), ". ")
	}
	if name == "" {
		return fallback
	}
	switch strings.ToUpper(strings.SplitN(name, ".", 2)[0]) {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return "_" + name
	}
	return name
}
