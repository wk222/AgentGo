package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSkillEvolveTool(t *testing.T) {
	r := NewRegistry()
	assert.NoError(t, RegisterSkillEvolveTool(r, "/test/workspace"))

	// 1. Generate skill
	genJSON := `{
		"action": "generate",
		"skill_name": "weather_lookup",
		"description": "查询指定城市的天气与风向预报"
	}`
	res, err := r.InvokeJSON(context.Background(), "evolve_skill", genJSON)
	assert.NoError(t, err)
	assert.Contains(t, res, "RegisterWeather_lookupTool")
	assert.Contains(t, res, "Weather_lookupInput")

	// 2. Evaluate skill with clean code
	cleanCode := `type In struct { Q string }; func run(ctx context.Context) {}`
	evalMap := map[string]string{
		"action":       "evaluate",
		"skill_name":   "clean_tool",
		"code_snippet": cleanCode,
	}
	evalBytes, _ := json.Marshal(evalMap)
	resEval, err := r.InvokeJSON(context.Background(), "evolve_skill", string(evalBytes))
	assert.NoError(t, err)
	assert.Contains(t, resEval, "综合评分")

	// 3. Guide generation
	guideJSON := `{
		"action": "guide",
		"skill_name": "weather_lookup",
		"description": "天气查询"
	}`
	resGuide, err := r.InvokeJSON(context.Background(), "evolve_skill", guideJSON)
	assert.NoError(t, err)
	assert.Contains(t, resGuide, "# 技能指南：weather_lookup")
}
