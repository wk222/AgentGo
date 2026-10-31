package memory

import (
	"fmt"
	"strings"
)

// Recall is evidence from the past, not a new instruction or proof of success.
func formatRecallRecord(record Record, maxRunes int) string {
	text := strings.TrimSpace(record.Content)
	runes := []rune(text)
	if maxRunes > 0 && len(runes) > maxRunes {
		text = string(runes[:maxRunes]) + " …[memory excerpt]"
	}
	evidence := "历史参考；当前更正优先"
	if record.Metadata["verification"] == "assistant_report_unverified" {
		evidence = "助手自述，未核实"
	} else if record.Metadata["verification"] == "derived_unverified" {
		evidence = "\u5f52\u7eb3\uff0c\u672a\u6838\u5b9e"
	} else {
		switch record.Modality {
		case "episode", "journal", "reflection", "insight":
			evidence = "历史记录或归纳，需核实"
		}
	}
	if note := sourceStateNote(record); note != "" {
		evidence += "；" + note
	}
	return fmt.Sprintf("- [id:%s; %s; %s] %s", record.ID, record.Modality, evidence, text)
}
