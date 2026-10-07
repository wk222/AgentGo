package lifecycle_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agentgo/internal/compose/lifecycle"
)

func TestScope_LIFODisposal(t *testing.T) {
	scope := lifecycle.NewScope()
	var order []int

	_, err := scope.Effect(func() (lifecycle.Disposer, error) {
		order = append(order, 1)
		return func() error {
			order = append(order, -1)
			return nil
		}, nil
	})
	require.NoError(t, err)

	_, err = scope.Effect(func() (lifecycle.Disposer, error) {
		order = append(order, 2)
		return func() error {
			order = append(order, -2)
			return nil
		}, nil
	})
	require.NoError(t, err)

	assert.Equal(t, []int{1, 2}, order)

	err = scope.Dispose()
	require.NoError(t, err)
	assert.True(t, scope.IsDisposed())

	// LIFO: 2 then 1 undone
	assert.Equal(t, []int{1, 2, -2, -1}, order)
}

func TestScope_NestedChildScopes(t *testing.T) {
	parent := lifecycle.NewScope()
	child := parent.Child()

	var events []string

	parent.OnDispose(func() error {
		events = append(events, "parent_cleanup")
		return nil
	})

	child.OnDispose(func() error {
		events = append(events, "child_cleanup")
		return nil
	})

	err := parent.Dispose()
	require.NoError(t, err)
	assert.True(t, parent.IsDisposed())
	assert.True(t, child.IsDisposed())

	// Child cleanups execute before parent cleanups
	assert.Equal(t, []string{"child_cleanup", "parent_cleanup"}, events)
}

func TestScope_ErrorAggregation(t *testing.T) {
	scope := lifecycle.NewScope()

	scope.OnDispose(func() error {
		return errors.New("err 1")
	})
	scope.OnDispose(func() error {
		return errors.New("err 2")
	})

	err := scope.Dispose()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "err 1")
	assert.Contains(t, err.Error(), "err 2")
}

func TestContextBinding(t *testing.T) {
	scope := lifecycle.NewScope()
	ctx := lifecycle.WithScope(context.Background(), scope)

	extracted := lifecycle.FromContext(ctx)
	assert.Equal(t, scope, extracted)

	var cleaned bool
	lifecycle.OnDispose(ctx, func() error {
		cleaned = true
		return nil
	})

	_ = scope.Dispose()
	assert.True(t, cleaned)
}
