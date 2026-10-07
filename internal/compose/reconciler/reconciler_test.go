package reconciler_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agentgo/internal/compose/lifecycle"
	"agentgo/internal/compose/reconciler"
)

type dummyComponent struct {
	id         string
	mountCount int
	cleanCount int
}

func (d *dummyComponent) ID() string {
	return d.id
}

func (d *dummyComponent) Mount(scope lifecycle.Scope) error {
	d.mountCount++
	scope.OnDispose(func() error {
		d.cleanCount++
		return nil
	})
	return nil
}

func TestReconciler_DiffAndReconcile(t *testing.T) {
	root := lifecycle.NewScope()
	rec := reconciler.New(root)

	c1 := &dummyComponent{id: "mcp-git"}
	c2 := &dummyComponent{id: "mcp-postgres"}
	c3 := &dummyComponent{id: "mcp-fetch"}

	// 1. Initial reconcile: mount c1 and c2
	err := rec.Reconcile([]reconciler.Component{c1, c2})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"mcp-git", "mcp-postgres"}, rec.ActiveIDs())
	assert.Equal(t, 1, c1.mountCount)
	assert.Equal(t, 1, c2.mountCount)
	assert.Equal(t, 0, c1.cleanCount)
	assert.Equal(t, 0, c2.cleanCount)

	// 2. Second reconcile: remove c2, keep c1, add c3
	err = rec.Reconcile([]reconciler.Component{c1, c3})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"mcp-git", "mcp-fetch"}, rec.ActiveIDs())
	// c1 was unchanged so mountCount is still 1 and cleanCount is 0
	assert.Equal(t, 1, c1.mountCount)
	assert.Equal(t, 0, c1.cleanCount)
	// c2 was removed so cleanCount is 1
	assert.Equal(t, 1, c2.cleanCount)
	// c3 was mounted
	assert.Equal(t, 1, c3.mountCount)
	assert.Equal(t, 0, c3.cleanCount)

	// 3. Close: unmount all
	err = rec.Close()
	require.NoError(t, err)
	assert.Empty(t, rec.ActiveIDs())
	assert.Equal(t, 1, c1.cleanCount)
	assert.Equal(t, 1, c3.cleanCount)
}
