package admin

import "fmt"

// Valid transition graph for durable tasks (PyBot admin runtime subset).
var allowedTransitions = map[TaskStatus]map[TaskStatus]bool{
	StatusPending:   {StatusRunning: true, StatusFailed: true, StatusCancelled: true, StatusPaused: true},
	StatusRunning:   {StatusPending: true, StatusCompleted: true, StatusFailed: true, StatusPaused: true, StatusCancelled: true},
	StatusPaused:    {StatusRunning: true, StatusCancelled: true, StatusPending: true},
	StatusCompleted: {},
	StatusFailed:    {StatusPending: true},
	StatusCancelled: {StatusPending: true},
}

// CanTransition reports whether status may change from → to.
func CanTransition(from, to TaskStatus) bool {
	if from == to {
		return true
	}
	next, ok := allowedTransitions[from]
	if !ok {
		return false
	}
	return next[to]
}

// AssertTransition returns an error on illegal admin state changes.
func AssertTransition(from, to TaskStatus) error {
	if CanTransition(from, to) {
		return nil
	}
	return fmt.Errorf("illegal admin task transition %s → %s", from, to)
}
