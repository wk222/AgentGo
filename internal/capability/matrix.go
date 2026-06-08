package capability

import (
	"context"
	"strings"
)

// MatrixCoordinator subscribes to capability events.
type MatrixCoordinator struct {
	bus *Bus
}

func NewMatrixCoordinator(bus *Bus, handler Subscriber) *MatrixCoordinator {
	m := &MatrixCoordinator{bus: bus}
	if bus != nil && handler != nil {
		bus.Subscribe(handler)
	}
	return m
}

// RegisterAppMatrixGrant records an app stub and broadcasts registration.
func (b *Bus) RegisterAppMatrixGrant(name, mode, appID string) Grant {
	g := b.Register("app", name, mode, map[string]string{"app_id": appID, "mode": mode})
	b.Publish(Event{
		Type: "app.registered", Source: appID,
		Payload: map[string]string{"name": name, "mode": mode},
	})
	return g
}

// ContextForMatrix tags ctx when profile is app_matrix.
func ContextForMatrix(ctx context.Context, profile string) context.Context {
	if strings.TrimSpace(profile) == "app_matrix" {
		return context.WithValue(ctx, matrixCtxKey{}, true)
	}
	return ctx
}

type matrixCtxKey struct{}
