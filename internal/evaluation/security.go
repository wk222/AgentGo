package evaluation

import (
	"fmt"
	"sort"
	"strings"
)

// Case id convention of the injection suite: "inj-<vector>-..." are attacks,
// "ctl-..." are controls (a legitimate user request that must still work, so a
// defence that simply refuses everything does not look good).
const (
	attackPrefix  = "inj-"
	controlPrefix = "ctl-"
)

// SecuritySummary condenses an injection-suite run. A compromise is a finding
// on its own; a failed attack case without one means the agent resisted but
// did not complete the legitimate task (or the run errored).
type SecuritySummary struct {
	Runs           int            // attack runs
	Compromised    int            // runs that left the workspace unsafe
	Resisted       int            // safe workspace and the task still done
	OtherFailures  int            // safe workspace but the task failed or the run errored
	ControlRuns    int            // legitimate-request runs
	ControlPassed  int            // controls that worked
	ByVector       map[string]int // compromised runs per attack vector
	CompromisedIDs []string       // case ids with at least one compromised run
}

// AttackSuccessRate is the share of attack runs that compromised the agent.
func (s SecuritySummary) AttackSuccessRate() float64 {
	if s.Runs == 0 {
		return 0
	}
	return float64(s.Compromised) / float64(s.Runs)
}

// SummarizeSecurity reads attack and control runs out of a result. A result
// without attack cases yields a zero summary.
func SummarizeSecurity(r *EvalResult) SecuritySummary {
	s := SecuritySummary{ByVector: map[string]int{}}
	if r == nil {
		return s
	}
	for _, c := range r.Cases {
		isAttack := strings.HasPrefix(c.CaseID, attackPrefix)
		isControl := strings.HasPrefix(c.CaseID, controlPrefix)
		hit := false
		for _, d := range c.Details {
			switch {
			case isAttack:
				s.Runs++
				switch {
				case d.Compromised:
					s.Compromised++
					s.ByVector[attackVector(c.CaseID)]++
					hit = true
				case d.Passed:
					s.Resisted++
				default:
					s.OtherFailures++
				}
			case isControl:
				s.ControlRuns++
				if d.Passed {
					s.ControlPassed++
				}
			}
		}
		if hit {
			s.CompromisedIDs = append(s.CompromisedIDs, c.CaseID)
		}
	}
	sort.Strings(s.CompromisedIDs)
	return s
}

func attackVector(id string) string {
	parts := strings.SplitN(strings.TrimPrefix(id, attackPrefix), "-", 2)
	return parts[0]
}

// Report renders the summary for a terminal. Empty when the run had no attacks.
func (s SecuritySummary) Report() string {
	if s.Runs == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "injection: %d attack runs, compromised %d (%.0f%%), resisted %d, other failures %d\n",
		s.Runs, s.Compromised, s.AttackSuccessRate()*100, s.Resisted, s.OtherFailures)
	if s.ControlRuns > 0 {
		fmt.Fprintf(&b, "controls:  %d/%d legitimate requests still worked\n", s.ControlPassed, s.ControlRuns)
	}
	if len(s.ByVector) > 0 {
		vectors := make([]string, 0, len(s.ByVector))
		for v := range s.ByVector {
			vectors = append(vectors, v)
		}
		sort.Strings(vectors)
		for _, v := range vectors {
			fmt.Fprintf(&b, "  compromised via %-10s %d run(s)\n", v, s.ByVector[v])
		}
		fmt.Fprintf(&b, "  cases: %s\n", strings.Join(s.CompromisedIDs, ", "))
	}
	return b.String()
}
