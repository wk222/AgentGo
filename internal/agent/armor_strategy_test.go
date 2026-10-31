package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

func TestArmorStrategySelection(t *testing.T) {
	t.Setenv("AGENTGO_ARMOR_BREAK", "1")
	red := WithSessionMode(context.Background(), SessionMode{Profile: ModeRedTeam, Canvas: CanvasDeep})
	res := &RunResult{Content: "抱歉，我无法协助。"}

	if !shouldArmorBreak(WithArmorStrategy(red, ArmorStrategyVerifySteer), res) {
		t.Fatal("verify_steer should still trigger")
	}
	if shouldArmorBreak(WithArmorStrategy(red, ArmorStrategyNone), res) {
		t.Fatal("none strategy (baseline) must not nudge")
	}
	if armorStrategyFromContext(red) != ArmorStrategyRollbackVerifySteer {
		t.Fatal("production default strategy must be rollback_verify_steer")
	}
	if armorAckFor(ArmorStrategyRollbackVerifySteer) != armorAckFor(ArmorStrategyVerifySteer) ||
		armorUserTextFor(ArmorStrategyRollbackVerifySteer) != armorUserTextFor(ArmorStrategyVerifySteer) {
		t.Fatal("rollback_verify_steer must reuse the verify-steer ack and nudge text")
	}
	if !strings.Contains(armorAckFor(ArmorStrategyVerifySteer), "按任务继续") {
		t.Fatal("verify steer ack should be honest short ack")
	}
	if !strings.Contains(armorUserTextFor(ArmorStrategyVerifySteer), "verify") && !strings.Contains(armorUserTextFor(ArmorStrategyVerifySteer), "验证") {
		t.Fatal("verify steer text must require verification")
	}
	// history modifier carries the selected ack
	mod := armorBreakHistoryModifierFor(res.Content, ArmorStrategyVerifySteer)
	out := mod(context.Background(), []adk.Message{
		schema.UserMessage("task"), schema.AssistantMessage(res.Content, nil), schema.UserMessage(armorUserTextFor(ArmorStrategyVerifySteer)),
	})
	found := false
	for _, m := range out {
		if m.Role == schema.Assistant && strings.Contains(m.Content, "按任务继续") {
			found = true
		}
		if strings.Contains(m.Content, "已经开始施工") {
			t.Fatal("verify steer view must not contain fabricated started-work ack")
		}
	}
	if !found {
		t.Fatal("verify steer ack missing from model view")
	}
}
