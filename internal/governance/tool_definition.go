package governance

import "context"

type toolDefinitionKey struct{}

// ToolDefinitionFromContext is the definition governance evaluated. Adapters
// can refuse a captured tool which no longer matches that decision.
func ToolDefinitionFromContext(ctx context.Context) string {
	id, _ := ctx.Value(toolDefinitionKey{}).(string)
	return id
}

// A governed call with missing metadata must not accidentally authorize a
// versioned adapter. Direct library callers without governance remain valid.
func ToolDefinitionMatches(ctx context.Context, definition string) bool {
	expected, governed := ctx.Value(toolDefinitionKey{}).(string)
	return !governed || expected == definition
}

func withToolDefinition(ctx context.Context, name string) context.Context {
	traits, _ := ToolTraitsOf(name)
	return context.WithValue(ctx, toolDefinitionKey{}, traits.DefinitionID)
}
