package bridge

import (
	"agentgo/internal/capability"
)

// ListCapabilityGrants returns all registered capability grants, optionally filtered by kind.
func (s *AppService) ListCapabilityGrants(kind string) []capability.Grant {
	if s.rt.capBus == nil {
		return nil
	}
	return s.rt.capBus.List(kind)
}

// DeleteCapabilityGrant removes a capability grant.
func (s *AppService) DeleteCapabilityGrant(id string) error {
	if s.rt.capBus == nil {
		return nil
	}
	return s.rt.capBus.Delete(id)
}

// TransitionCapabilityGrant changes the lifecycle status of a capability grant.
func (s *AppService) TransitionCapabilityGrant(id string, status string) error {
	if s.rt.capBus == nil {
		return nil
	}
	return s.rt.capBus.Transition(id, capability.AssetStatus(status))
}

// VerifyCapabilityGrant updates the verification result of a capability grant.
func (s *AppService) VerifyCapabilityGrant(id string, result string) error {
	if s.rt.capBus == nil {
		return nil
	}
	s.rt.capBus.Verify(id, result)
	return nil
}

// DeprecateCapabilityGrant marks a capability grant as deprecated.
func (s *AppService) DeprecateCapabilityGrant(id string, supersededBy string) error {
	if s.rt.capBus == nil {
		return nil
	}
	return s.rt.capBus.Deprecate(id, supersededBy)
}
