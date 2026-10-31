package bridge

import (
	"sync/atomic"
	"time"

	"github.com/cloudwego/eino/schema"
)

// chatUsageFunc books one finished ChatOnce call in the daily usage ledger.
type chatUsageFunc func(model string, prompt, cached, completion, reasoning int, took time.Duration)

// chatUsageSink is the ledger hook of the running runtime. The agent loop books its own model calls through run
// callbacks; everything that goes through ChatOnce (the memory journal, learning and summaries, workflow nodes,
// inline edits, the message gateway) would otherwise be missing from the usage figures although it is billed all
// the same. A journal call alone can carry thousands of tokens.
var chatUsageSink atomic.Pointer[chatUsageFunc]

// setChatUsageSink installs f and returns the function that removes it again (only while it is still the
// installed one, so a runtime that closes late cannot unplug its successor).
func setChatUsageSink(f chatUsageFunc) func() {
	p := &f
	chatUsageSink.Store(p)
	return func() { chatUsageSink.CompareAndSwap(p, nil) }
}

// recordChatUsage reports a finished call. A reply without usage figures still counts as a call, as it does in
// the agent loop ("services that return no usage count as 0").
func recordChatUsage(model string, out *schema.Message, took time.Duration) {
	sink := chatUsageSink.Load()
	if sink == nil {
		return
	}
	var prompt, cached, completion, reasoning int
	if out != nil && out.ResponseMeta != nil && out.ResponseMeta.Usage != nil {
		u := out.ResponseMeta.Usage
		prompt, cached = u.PromptTokens, u.PromptTokenDetails.CachedTokens
		completion, reasoning = u.CompletionTokens, u.CompletionTokensDetails.ReasoningTokens
	}
	(*sink)(model, prompt, cached, completion, reasoning, took)
}
