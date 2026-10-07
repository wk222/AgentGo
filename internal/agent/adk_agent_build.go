package agent

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"agentgo/internal/governance"
)

func (r *Runner) newOpenAIModel(ctx context.Context, cfg LLMSettings, modelName string) (model.ToolCallingChatModel, error) {
	name := strings.TrimSpace(modelName)
	if name == "" {
		name = cfg.Model
	}
	policy := CanvasPolicyFromContext(ctx)
	timeout := policy.LLMTimeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	if responsesEnabled() {
		var hc *http.Client
		if debugLLMEnabled() {
			hc = newLLMShapeClient()
		}
		return newResponsesModel(cfg, name, timeout, hc), nil
	}
	mc := &openai.ChatModelConfig{
		APIKey: cfg.APIKey, BaseURL: cfg.APIBase, Model: name,
		Timeout: timeout,
	}
	// Reasoning models (e.g. Azure gpt-5/o-series deployments) reject function tools on
	// /chat/completions unless reasoning_effort is "none". Opt-in, default unchanged.
	if v := strings.TrimSpace(os.Getenv("AGENTGO_REASONING_EFFORT")); v != "" {
		mc.ReasoningEffort = openai.ReasoningEffortLevel(v)
	} else if strings.Contains(strings.ToLower(name), "luna") || strings.Contains(strings.ToLower(name), "gpt-5") {
		mc.ReasoningEffort = openai.ReasoningEffortLevel("none")
	}
	if debugLLMEnabled() {
		mc.HTTPClient = newLLMShapeClient()
	}
	return openai.NewChatModel(ctx, mc)
}

func (r *Runner) modelFailoverConfig(ctx context.Context, cfg LLMSettings) (*adk.ModelFailoverConfig[*schema.Message], error) {
	fbName := strings.TrimSpace(cfg.FallbackModel)
	if fbName == "" {
		return nil, nil
	}
	fallback, err := r.newOpenAIModel(ctx, cfg, fbName)
	if err != nil {
		return nil, err
	}
	return &adk.ModelFailoverConfig[*schema.Message]{
		MaxRetries: 1,
		ShouldFailover: func(_ context.Context, _ *schema.Message, err error) bool {
			return err != nil
		},
		GetFailoverModel: func(_ context.Context, fc *adk.FailoverContext[*schema.Message]) (model.BaseModel[*schema.Message], []*schema.Message, error) {
			return fallback, fc.InputMessages, nil
		},
	}, nil
}

// buildChatModelAgent constructs a ChatModelAgent for one ADK turn (TurnLoop / Query).
func (r *Runner) buildChatModelAgent(ctx context.Context, cfg LLMSettings) (*adk.ChatModelAgent, error) {
	chatModel, err := r.newOpenAIModel(ctx, cfg, cfg.Model)
	if err != nil {
		return nil, err
	}
	handlers, err := r.BuildADKHandlers(ctx, chatModel)
	if err != nil {
		return nil, err
	}
	sm := SessionModeFromContext(ctx)
	toolList := RegistryToolsForMode(r.toolReg, sm)
	toolsCfg := adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: toolList}}
	var toolMW []compose.ToolMiddleware
	policy := r.effectivePolicyForCtx(ctx)
	if r.queue != nil {
		toolMW = append(toolMW, governance.ComposeToolMiddleware(r.queue, policy))
	}
	toolMW = append(toolMW, ComposeToolHealMiddleware())
	toolsCfg.ToolsNodeConfig.ToolCallMiddlewares = toolMW
	failover, err := r.modelFailoverConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	agentCfg := &adk.ChatModelAgentConfig{
		Name:                "agentgo",
		Description:         "AgentGo desktop agent",
		Model:               chatModel,
		ToolsConfig:         toolsCfg,
		Handlers:            handlers,
		MaxIterations:       r.maxIterationsFor(ctx),
		ModelRetryConfig:    DefaultModelRetryConfig(),
		ModelFailoverConfig: failover,
	}
	return adk.NewChatModelAgent(ctx, agentCfg)
}
