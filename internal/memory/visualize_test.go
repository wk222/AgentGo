package memory

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateHTMLGraph(t *testing.T) {
	nodes := []Record{
		{
			ID:         "node-1",
			Content:    "用户偏好使用 Go 语言而非 Python 循环",
			Modality:   "fact",
			Scope:      "global",
			Importance: 1.0,
		},
		{
			ID:         "node-2",
			Content:    "Eino ADK 提供完善的 middleware 与 runner 机制",
			Modality:   "insight",
			Scope:      "global",
			Importance: 0.9,
		},
	}

	links := []MemoryLink{
		{
			SourceID: "node-1",
			TargetID: "node-2",
			Relation: "relates_to",
			Weight:   0.85,
		},
	}

	html := GenerateHTMLGraph(nodes, links)
	assert.Contains(t, html, "AgentGo 记忆与知识图谱全景")
	assert.Contains(t, html, "节点数: 2 | 关系数: 1")
	assert.Contains(t, html, "node-1")
	assert.Contains(t, html, "node-2")
	assert.Contains(t, html, "relates_to")

	tmpPath := filepath.Join(t.TempDir(), "graph.html")
	assert.NoError(t, os.WriteFile(tmpPath, []byte(html), 0644))
	info, err := os.Stat(tmpPath)
	assert.NoError(t, err)
	assert.True(t, info.Size() > 500)
}
