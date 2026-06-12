package agent

import (
	"strings"
	"unicode"
)

const maxAutoContinue = 3

// NeedsContinuation detects max_tokens truncation or broken JSON/tool output tails.
func NeedsContinuation(content, finishReason string) bool {
	if strings.EqualFold(finishReason, "length") {
		return true
	}
	c := strings.TrimSpace(content)
	if c == "" {
		return false
	}
	if looksTruncatedJSON(c) {
		return true
	}
	// Common model stop mid-sentence without finish_reason in some gateways
	if len(c) > 200 && !strings.HasSuffix(c, ".") && !strings.HasSuffix(c, "。") &&
		!strings.HasSuffix(c, "}") && !strings.HasSuffix(c, "]") && !strings.HasSuffix(c, "```") {
		open := strings.Count(c, "{") - strings.Count(c, "}")
		if open > 0 {
			return true
		}
	}
	return false
}

func looksTruncatedJSON(s string) bool {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "{") {
		return false
	}
	// Ends inside a string or with dangling structure
	if strings.HasSuffix(s, ",") || strings.HasSuffix(s, ":") || strings.HasSuffix(s, "[") {
		return true
	}
	openBrace := strings.Count(s, "{") - strings.Count(s, "}")
	openBracket := strings.Count(s, "[") - strings.Count(s, "]")
	if openBrace > 0 || openBracket > 0 {
		// If last non-space char suggests cut mid-value
		r := []rune(s)
		for i := len(r) - 1; i >= 0; i-- {
			if unicode.IsSpace(r[i]) {
				continue
			}
			ch := r[i]
			return ch == '"' || ch == '\'' || ch == ',' || ch == ':' || ch == '{' || ch == '['
		}
	}
	return false
}

// RepairContinuationPrompt asks the model to continue without repeating prior content.
func RepairContinuationPrompt(prior string) string {
	hint := ""
	if looksTruncatedJSON(strings.TrimSpace(prior)) {
		hint = "上一段 JSON/工具输出不完整，请从断点续写并补全合法 JSON，不要重复已输出部分。"
	} else {
		hint = "上一段因长度截断，请从断点继续输出，不要重复已写内容。"
	}
	return "[系统续接] " + hint
}

// MergeContinuation stitches continuation onto prior assistant text.
func MergeContinuation(prior, cont string) string {
	prior = strings.TrimRight(prior, " \n\r\t")
	cont = strings.TrimLeft(cont, " \n\r\t")
	if prior == "" {
		return cont
	}
	if cont == "" {
		return prior
	}
	// JSON merge: naive concat when prior ends mid-structure
	if looksTruncatedJSON(prior) && (strings.HasPrefix(cont, "{") || strings.HasPrefix(cont, "[")) {
		return prior + cont
	}
	return prior + cont
}
