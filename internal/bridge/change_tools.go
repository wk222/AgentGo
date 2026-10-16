package bridge

import "agentgo/internal/codetools"

// editToolNames are the engine tool names whose calls change a file and carry
// the target in "file_path"; core's change capture is told about them here
// because the kernel does not know concrete tool plugins.
var editToolNames = map[string]bool{
	codetools.ToolName("edit"):      true,
	codetools.ToolName("multiedit"): true,
	codetools.ToolName("write"):     true,
}
