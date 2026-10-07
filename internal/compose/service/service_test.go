package service_test

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agentgo/internal/compose/service"
)

type dummyService struct {
	name   string
	status service.Status
}

func (d *dummyService) Name() string           { return d.name }
func (d *dummyService) Status() service.Status { return d.status }

func TestRegistry_RegisterAndDispose(t *testing.T) {
	reg := service.NewRegistry()

	svc := &dummyService{name: "docker", status: service.StatusReady}
	disposer, err := reg.Register(svc)
	require.NoError(t, err)

	assert.Equal(t, service.StatusReady, reg.Status("docker"))

	ready, missing := reg.CheckCoeffects("docker")
	assert.True(t, ready)
	assert.Empty(t, missing)

	// Dispose (unregister)
	err = disposer()
	require.NoError(t, err)
	assert.Equal(t, service.StatusUnavailable, reg.Status("docker"))

	ready, missing = reg.CheckCoeffects("docker")
	assert.False(t, ready)
	assert.Equal(t, []string{"docker"}, missing)
}

func TestRegistry_Subscription(t *testing.T) {
	reg := service.NewRegistry()

	var events []string
	unsub := reg.Subscribe(func(name string, oldStatus, newStatus service.Status) {
		events = append(events, string(oldStatus)+"->"+string(newStatus))
	})

	svc := &dummyService{name: "python", status: service.StatusDegraded}
	disposer, err := reg.Register(svc)
	require.NoError(t, err)

	reg.SetStatus("python", service.StatusReady)

	_ = disposer()
	_ = unsub()

	assert.Contains(t, events, "unavailable->degraded")
	assert.Contains(t, events, "degraded->ready")
	assert.Contains(t, events, "ready->unavailable")
}

func TestFilterAvailableTools(t *testing.T) {
	reg := service.NewRegistry()

	t1, _ := utils.InferTool("normal_tool", "desc", func(ctx context.Context, in struct{}) (string, error) {
		return "ok", nil
	})
	t2, _ := utils.InferTool("python_tool", "desc", func(ctx context.Context, in struct{}) (string, error) {
		return "ok", nil
	})
	coeffectT2 := service.WrapWithCoeffects(t2, "python_runtime")

	tools := []tool.BaseTool{t1, coeffectT2}

	// Case 1: python_runtime not registered -> only normal_tool available
	avail := service.FilterAvailableTools(context.Background(), tools, reg)
	assert.Len(t, avail, 1)

	// Case 2: python_runtime registered and ready -> both tools available
	disposer, err := reg.Register(&dummyService{name: "python_runtime", status: service.StatusReady})
	require.NoError(t, err)

	avail = service.FilterAvailableTools(context.Background(), tools, reg)
	assert.Len(t, avail, 2)

	// Case 3: python_runtime degraded/unavailable -> back to 1 tool
	_ = disposer()
	avail = service.FilterAvailableTools(context.Background(), tools, reg)
	assert.Len(t, avail, 1)
}
