package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// VisNode represents a node in the Vis.js network graph.
type VisNode struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Title string `json:"title"`
	Color string `json:"color"`
	Size  int    `json:"size"`
	Group string `json:"group"`
}

// VisEdge represents an edge in the Vis.js network graph.
type VisEdge struct {
	From    string  `json:"from"`
	To      string  `json:"to"`
	Label   string  `json:"label"`
	Title   string  `json:"title"`
	Width   float64 `json:"width"`
	Opacity float64 `json:"opacity"`
	Color   string  `json:"color"`
}

// GenerateHTMLGraph transforms memory records and links into a self-contained, interactive HTML5
// knowledge graph visualization with Vis.js physics, ported and enhanced from PurrCat's visualize_graph.py.
func GenerateHTMLGraph(nodes []Record, links []MemoryLink) string {
	visNodes := make([]VisNode, 0, len(nodes))
	visEdges := make([]VisEdge, 0, len(links))

	for _, n := range nodes {
		label := n.Content
		if len(label) > 30 {
			label = label[:30] + "..."
		}
		label = strings.ReplaceAll(label, "\n", " ")

		color := "#4A90E2" // default blue
		switch n.Modality {
		case "fact":
			color = "#2ECC71" // green
		case "insight":
			color = "#9B59B6" // purple
		case "reflection":
			color = "#F39C12" // orange
		case "episode":
			color = "#E74C3C" // red
		}

		size := 16 + int(n.Importance*8)
		if size < 12 {
			size = 12
		} else if size > 40 {
			size = 40
		}

		tooltip := fmt.Sprintf("ID: %s\nModality: %s\nScope: %s\nImportance: %.2f\n\n%s",
			n.ID, n.Modality, n.Scope, n.Importance, n.Content)

		visNodes = append(visNodes, VisNode{
			ID:    n.ID,
			Label: label,
			Title: tooltip,
			Color: color,
			Size:  size,
			Group: n.Modality,
		})
	}

	for _, l := range links {
		w := 1.0 + l.Weight*3.0
		opacity := 0.3 + l.Weight*0.7
		if opacity > 1.0 {
			opacity = 1.0
		}

		tooltip := fmt.Sprintf("关系: %s\n权重: %.2f", l.Relation, l.Weight)

		visEdges = append(visEdges, VisEdge{
			From:    l.SourceID,
			To:      l.TargetID,
			Label:   l.Relation,
			Title:   tooltip,
			Width:   w,
			Opacity: opacity,
			Color:   "#8B4513",
		})
	}

	nodesJSON, _ := json.Marshal(visNodes)
	edgesJSON, _ := json.Marshal(visEdges)

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <title>AgentGo 记忆与知识图谱全景 (PurrMemo 架构)</title>
  <script type="text/javascript" src="https://unpkg.com/vis-network/standalone/umd/vis-network.min.js"></script>
  <style>
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #0f172a; color: #f8fafc; overflow: hidden; }
    #header { height: 50px; background: #1e293b; border-bottom: 1px solid #334155; display: flex; align-items: center; justify-content: space-between; padding: 0 20px; }
    #header h1 { font-size: 16px; font-weight: 600; color: #38bdf8; }
    #stats { font-size: 13px; color: #94a3b8; }
    #network { width: 100vw; height: calc(100vh - 50px); }
    #legend { position: absolute; bottom: 20px; left: 20px; background: rgba(30, 41, 59, 0.85); backdrop-filter: blur(8px); padding: 12px 16px; border-radius: 8px; border: 1px solid #334155; font-size: 12px; }
    .legend-item { display: flex; align-items: center; margin-bottom: 6px; }
    .legend-item:last-child { margin-bottom: 0; }
    .dot { width: 12px; height: 12px; border-radius: 50%%; margin-right: 8px; }
  </style>
</head>
<body>
  <div id="header">
    <h1>AgentGo 记忆知识图谱 (PurrMemo Visualizer)</h1>
    <div id="stats">节点数: %d | 关系数: %d</div>
  </div>
  <div id="network"></div>
  <div id="legend">
    <div class="legend-item"><div class="dot" style="background:#2ECC71;"></div>Fact (事实记忆)</div>
    <div class="legend-item"><div class="dot" style="background:#9B59B6;"></div>Insight (洞察归纳)</div>
    <div class="legend-item"><div class="dot" style="background:#F39C12;"></div>Reflection (反思与教训)</div>
    <div class="legend-item"><div class="dot" style="background:#E74C3C;"></div>Episode (情景会话)</div>
  </div>
  <script type="text/javascript">
    const nodes = new vis.DataSet(%s);
    const edges = new vis.DataSet(%s);

    const container = document.getElementById("network");
    const data = { nodes: nodes, edges: edges };
    const options = {
      nodes: {
        shape: "dot",
        font: { color: "#f8fafc", size: 12, strokeWidth: 2, strokeColor: "#0f172a" },
        borderWidth: 2
      },
      edges: {
        arrows: { to: { enabled: true, scaleFactor: 0.6 } },
        color: { color: "#64748b", highlight: "#38bdf8" },
        font: { color: "#94a3b8", size: 10, align: "middle" },
        smooth: { type: "continuous" }
      },
      physics: {
        forceAtlas2Based: {
          gravitationalConstant: -80,
          centralGravity: 0.015,
          springLength: 120,
          springConstant: 0.05,
          damping: 0.4
        },
        solver: "forceAtlas2Based",
        stabilization: { iterations: 200 }
      },
      interaction: {
        hover: true,
        tooltipDelay: 100,
        navigationButtons: true,
        keyboard: true
      }
    };
    const network = new vis.Network(container, data, options);
  </script>
</body>
</html>`, len(visNodes), len(visEdges), string(nodesJSON), string(edgesJSON))
}

// ExportHTMLGraph queries records and links and saves the interactive HTML to outputPath.
func ExportHTMLGraph(ctx context.Context, store *SQLiteStore, centerID string, limit int, outputPath string) error {
	gv, err := store.BuildGraphView(ctx, centerID, limit)
	if err != nil {
		return fmt.Errorf("build graph view: %w", err)
	}

	html := GenerateHTMLGraph(gv.Nodes, gv.Edges)
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(outputPath, []byte(html), 0644)
}
