package capability

import "fmt"

// AssetStatus represents the lifecycle state of a capability asset.
type AssetStatus string

const (
	StatusDraft      AssetStatus = "draft"
	StatusVerified   AssetStatus = "verified"
	StatusPublished  AssetStatus = "published"
	StatusDeprecated AssetStatus = "deprecated"
	StatusRetired    AssetStatus = "retired"
)

var allowedTransitions = map[AssetStatus]map[AssetStatus]bool{
	StatusDraft: {
		StatusVerified:   true,
		StatusPublished:  true,
		StatusDeprecated: true,
	},
	StatusVerified: {
		StatusPublished:  true,
		StatusDeprecated: true,
	},
	StatusPublished: {
		StatusDeprecated: true,
		StatusRetired:    true,
	},
	StatusDeprecated: {
		StatusPublished: true,
		StatusRetired:   true,
	},
	StatusRetired: {
		StatusDraft: true,
	},
}

// CanTransition reports whether the capability asset status may change from → to.
func CanTransition(from, to AssetStatus) bool {
	if from == to {
		return true
	}
	next, ok := allowedTransitions[from]
	if !ok {
		return false
	}
	return next[to]
}

// AssertTransition returns an error if the status transition is not permitted.
func AssertTransition(from, to AssetStatus) error {
	if CanTransition(from, to) {
		return nil
	}
	return fmt.Errorf("illegal asset capability transition %s → %s", from, to)
}
