package bridge

// modelLimitResolvers let optional plugins fill in a model's context window and
// output limit for connections they own. The host never guesses model specs: a
// resolver that does not recognise the connection must return cfg unchanged.
// Registration happens in init(), so a build without such plugins has none.
var modelLimitResolvers []func(rt *Runtime, cfg LLMConfig) LLMConfig

func (rt *Runtime) resolveModelLimits(cfg LLMConfig) LLMConfig {
	for _, resolve := range modelLimitResolvers {
		cfg = resolve(rt, cfg)
	}
	return cfg
}
