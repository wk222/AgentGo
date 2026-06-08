package capability

import (
	"context"
	"fmt"
	"time"
)

// SynthesizeRequest describes a tool/skill compile artifact for the bus.
type SynthesizeRequest struct {
	Kind     string            // tool, skill, agent
	Name     string
	Scope    string
	Source   string
	Metadata map[string]string
}

// SynthesizePipeline registers grants, records metrics, and publishes bus events (PyBot capability synthesize subset).
type SynthesizePipeline struct {
	bus *Bus
}

func NewSynthesizePipeline(bus *Bus) *SynthesizePipeline {
	return &SynthesizePipeline{bus: bus}
}

// CompileAndRegister runs grant + metric + event fan-out.
func (p *SynthesizePipeline) CompileAndRegister(ctx context.Context, req SynthesizeRequest) (Grant, error) {
	_ = ctx
	if p == nil || p.bus == nil {
		return Grant{}, fmt.Errorf("capability bus unavailable")
	}
	name := req.Name
	if name == "" {
		return Grant{}, fmt.Errorf("synthesize: name required")
	}
	scope := req.Scope
	if scope == "" {
		scope = "agent"
	}
	kind := req.Kind
	if kind == "" {
		kind = "tool"
	}
	start := time.Now()
	g := p.bus.Register(kind, name, scope, req.Metadata)
	p.bus.RecordMetric("capability.synthesize", name, float64(time.Since(start).Milliseconds()), 0)
	switch kind {
	case "tool":
		p.bus.PublishToolCompiled(name, scope)
	default:
		p.bus.Publish(Event{
			Type: "capability.compiled", Source: name,
			Payload: map[string]string{"kind": kind, "scope": scope, "source": req.Source},
		})
	}
	return g, nil
}
