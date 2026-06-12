package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// NotifyNodeExecutor emits workflow/task events (PyFlow notify subset).
type NotifyNodeExecutor struct{}

func (e *NotifyNodeExecutor) Execute(_ context.Context, n Node, input, last string, rc RunContext) (string, error) {
	channel := "desktop"
	message := expandTemplateVars(n.Prompt, input, last, rc.Vars)
	if n.Config != nil {
		if c, ok := n.Config["channel"].(string); ok && c != "" {
			channel = c
		}
		if m, ok := n.Config["message"].(string); ok && m != "" {
			message = expandTemplateVars(m, input, last, rc.Vars)
		}
	}
	if message == "" {
		message = last
	}
	rc.OnEvent(Event{
		ID: n.ID, RunID: rc.RunID, NodeID: n.ID, NodeType: "notify",
		Type: "notify", Input: message, Output: channel, Timestamp: time.Now().Unix(),
	})
	payload := map[string]any{"channel": channel, "message": message, "run_id": rc.RunID}
	b, _ := json.Marshal(payload)
	return string(b), nil
}

// MonitorNodeExecutor checks a metric/threshold and branches via output JSON.
type MonitorNodeExecutor struct{}

func (e *MonitorNodeExecutor) Execute(_ context.Context, n Node, input, last string, rc RunContext) (string, error) {
	metric := "last_output_len"
	op := "gte"
	threshold := 0.0
	varName := ""
	if n.Config != nil {
		if m, ok := n.Config["metric"].(string); ok && m != "" {
			metric = m
		}
		if o, ok := n.Config["op"].(string); ok && o != "" {
			op = strings.ToLower(strings.TrimSpace(o))
		}
		if vn, ok := n.Config["var"].(string); ok {
			varName = strings.TrimSpace(vn)
		}
		switch v := n.Config["threshold"].(type) {
		case float64:
			threshold = v
		case int:
			threshold = float64(v)
		case string:
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				threshold = f
			}
		}
	}
	value, verr := monitorMetricValue(metric, varName, last, rc)
	ok := verr == nil && compareMetric(value, op, threshold)
	status := "alert"
	if ok {
		status = "ok"
	}
	rc.OnEvent(Event{
		ID: n.ID, RunID: rc.RunID, NodeID: n.ID, NodeType: "monitor",
		Type: "monitor", Input: fmt.Sprintf("%s %s %.2f (value=%v)", metric, op, threshold, value), Output: status, Timestamp: time.Now().Unix(),
	})
	out := map[string]any{"metric": metric, "op": op, "value": value, "threshold": threshold, "status": status, "ok": ok}
	if verr != nil {
		out["error"] = verr.Error()
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}

// monitorMetricValue resolves the configured monitor metric to a numeric value.
// Supported metrics: last_output_len (rune count, default), output_bytes,
// word_count, line_count, number (parse a number from the last output), and
// var (numeric value of the workflow variable named by config "var").
func monitorMetricValue(metric, varName, last string, rc RunContext) (float64, error) {
	switch strings.ToLower(strings.TrimSpace(metric)) {
	case "", "last_output_len", "output_len", "len":
		return float64(len([]rune(last))), nil
	case "output_bytes", "bytes":
		return float64(len(last)), nil
	case "word_count", "words":
		return float64(len(strings.Fields(last))), nil
	case "line_count", "lines":
		if strings.TrimSpace(last) == "" {
			return 0, nil
		}
		return float64(len(strings.Split(strings.TrimRight(last, "\r\n"), "\n"))), nil
	case "number", "numeric", "value":
		return parseFirstNumber(last)
	case "var", "variable":
		if varName == "" {
			return 0, fmt.Errorf("monitor metric var requires config \"var\"")
		}
		if rc.Vars != nil {
			if v, ok := rc.Vars[varName]; ok {
				return parseFirstNumber(v)
			}
		}
		return 0, fmt.Errorf("monitor var %q not set", varName)
	default:
		// Unknown metric name (likely a config typo) — surface it as a soft error
		// rather than silently measuring something the author did not ask for.
		return 0, fmt.Errorf("unknown monitor metric %q", metric)
	}
}

// parseFirstNumber parses s as a float, or extracts the first numeric token from it.
func parseFirstNumber(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f, nil
	}
	for _, tok := range strings.FieldsFunc(s, func(r rune) bool {
		return !((r >= '0' && r <= '9') || r == '.' || r == '-' || r == '+')
	}) {
		if f, err := strconv.ParseFloat(tok, 64); err == nil {
			return f, nil
		}
	}
	clip := s
	if len(clip) > 60 {
		clip = clip[:60]
	}
	return 0, fmt.Errorf("no numeric value in %q", clip)
}

// compareMetric compares value against threshold using op (default gte).
func compareMetric(value float64, op string, threshold float64) bool {
	switch op {
	case "lte", "le", "<=":
		return value <= threshold
	case "lt", "<":
		return value < threshold
	case "eq", "==", "=":
		return value == threshold
	case "ne", "!=", "<>":
		return value != threshold
	case "gt", ">":
		return value > threshold
	default: // gte, ">="
		return value >= threshold
	}
}

// DataSourceNodeExecutor loads JSON/text from workspace path or inline config.
type DataSourceNodeExecutor struct{}

func (e *DataSourceNodeExecutor) Execute(ctx context.Context, n Node, input, last string, rc RunContext) (string, error) {
	if n.Config != nil {
		if inline, ok := n.Config["data"].(string); ok && strings.TrimSpace(inline) != "" {
			return expandTemplateVars(inline, input, last, rc.Vars), nil
		}
		if path, ok := n.Config["path"].(string); ok && strings.TrimSpace(path) != "" {
			path = expandTemplateVars(path, input, last, rc.Vars)
			b, err := os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("data_source %s: %w", n.ID, err)
			}
			return string(b), nil
		}
	}
	if strings.TrimSpace(last) != "" {
		return last, nil
	}
	return input, nil
}
