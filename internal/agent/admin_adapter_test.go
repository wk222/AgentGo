package agent

import "testing"

func TestAdminExecPlan(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	cases := []struct {
		name     string
		hasCP    bool
		vars     map[string]string
		wantPath adminPath
		wantExcl bool
	}{
		{"no checkpoint forces legacy", false, nil, adminPathLegacy, false},
		{"no checkpoint ignores deep flags", false, map[string]string{"AGENTGO_ADMIN_DEEP_ONLY": "1"}, adminPathLegacy, false},
		{"legacy flag forces legacy", true, map[string]string{"AGENTGO_ADMIN_LEGACY": "1"}, adminPathLegacy, false},
		{"default is deep", true, nil, adminPathDeep, false},
		{"deep only is exclusive", true, map[string]string{"AGENTGO_ADMIN_DEEP_ONLY": "1"}, adminPathDeep, true},
		{"plan-execute opt-in", true, map[string]string{"AGENTGO_ADMIN_PLANEXECUTE": "1"}, adminPathPlanExecute, false},
		{"plan-execute only is exclusive", true, map[string]string{"AGENTGO_ADMIN_PLANEXECUTE": "1", "AGENTGO_ADMIN_PE_ONLY": "1"}, adminPathPlanExecute, true},
		{"legacy flag overrides plan-execute", true, map[string]string{"AGENTGO_ADMIN_LEGACY": "1", "AGENTGO_ADMIN_PLANEXECUTE": "1"}, adminPathLegacy, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path, excl := adminExecPlan(c.hasCP, env(c.vars))
			if path != c.wantPath || excl != c.wantExcl {
				t.Fatalf("got (path=%d, exclusive=%v) want (path=%d, exclusive=%v)", path, excl, c.wantPath, c.wantExcl)
			}
		})
	}
}
