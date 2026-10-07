package service

import (
	"agentgo/internal/compose/lifecycle"
)

// Status represents the operational state of a capability service.
type Status string

const (
	StatusReady       Status = "ready"
	StatusDegraded    Status = "degraded"
	StatusUnavailable Status = "unavailable"
)

// Service defines a named background capability that tools and agents depend on.
type Service interface {
	Name() string
	Status() Status
}

// ChangeHandler is notified when a service status changes.
type ChangeHandler func(name string, oldStatus, newStatus Status)

// Registry manages registered capability services and tracks dependencies.
type Registry interface {
	Register(svc Service) (lifecycle.Disposer, error)
	Get(name string) (Service, bool)
	Status(name string) Status
	SetStatus(name string, status Status)
	Subscribe(handler ChangeHandler) lifecycle.Disposer
	CheckCoeffects(requiredServices ...string) (allReady bool, missing []string)
	AllServices() map[string]Status
}

// CoeffectAware specifies that an entity (like a tool or subagent) requires specific services to be ready.
type CoeffectAware interface {
	RequiredServices() []string
}
