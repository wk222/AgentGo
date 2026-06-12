package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/compose"
)

// PipelineInput represents the input for the Eino-based memory pipeline graph.
type PipelineInput struct {
	Scope        string
	Conversation string
}

// BuildPipelineGraph constructs the Eino compose.Graph for the 4-phase memory pipeline.
func (p *Pipeline) BuildPipelineGraph(ctx context.Context, caller LLMCaller) (compose.Runnable[PipelineInput, error], error) {
	g := compose.NewGraph[PipelineInput, error]()

	// Node 1: RunJournal
	journalLambda := compose.InvokableLambda(func(ctx context.Context, in PipelineInput) (PipelineInput, error) {
		if _, err := p.RunJournal(ctx, caller, in.Scope, in.Conversation); err != nil {
			return in, err
		}
		return in, nil
	})
	err := g.AddLambdaNode("journal", journalLambda)
	if err != nil {
		return nil, fmt.Errorf("failed to add journal node: %w", err)
	}

	// Node 2: RunDistillLLM
	distillLambda := compose.InvokableLambda(func(ctx context.Context, in PipelineInput) (PipelineInput, error) {
		journals, err := p.store.ListByModality(ctx, modalityJournal, in.Scope, 7)
		if err != nil {
			return in, err
		}
		var jb strings.Builder
		for _, j := range journals {
			jb.WriteString(j.Content)
			jb.WriteString("\n---\n")
		}
		snap, _ := p.ContextPrompt(ctx, in.Scope)
		if _, err := p.RunDistillLLM(ctx, caller, in.Scope, jb.String(), snap); err != nil {
			return in, err
		}
		return in, nil
	})
	err = g.AddLambdaNode("distill", distillLambda)
	if err != nil {
		return nil, fmt.Errorf("failed to add distill node: %w", err)
	}

	// Node 3: RunReflect
	reflectLambda := compose.InvokableLambda(func(ctx context.Context, in PipelineInput) (PipelineInput, error) {
		snap, _ := p.ContextPrompt(ctx, in.Scope)
		if snap != "" {
			_, _ = p.RunReflect(ctx, caller, in.Scope, snap)
		}
		return in, nil
	})
	err = g.AddLambdaNode("reflect", reflectLambda)
	if err != nil {
		return nil, fmt.Errorf("failed to add reflect node: %w", err)
	}

	// Node 4: RunGC
	gcLambda := compose.InvokableLambda(func(ctx context.Context, in PipelineInput) (error, error) {
		_, err := p.RunGC(ctx, 30, 0.4)
		return err, nil
	})
	err = g.AddLambdaNode("gc", gcLambda)
	if err != nil {
		return nil, fmt.Errorf("failed to add gc node: %w", err)
	}

	// Link nodes sequentially (JOURNAL -> DISTILL -> REFLECT -> GC)
	if err = g.AddEdge(compose.START, "journal"); err != nil {
		return nil, fmt.Errorf("failed to link start to journal: %w", err)
	}
	if err = g.AddEdge("journal", "distill"); err != nil {
		return nil, fmt.Errorf("failed to link journal to distill: %w", err)
	}
	if err = g.AddEdge("distill", "reflect"); err != nil {
		return nil, fmt.Errorf("failed to link distill to reflect: %w", err)
	}
	if err = g.AddEdge("reflect", "gc"); err != nil {
		return nil, fmt.Errorf("failed to link reflect to gc: %w", err)
	}
	if err = g.AddEdge("gc", compose.END); err != nil {
		return nil, fmt.Errorf("failed to link gc to end: %w", err)
	}

	return g.Compile(ctx)
}
