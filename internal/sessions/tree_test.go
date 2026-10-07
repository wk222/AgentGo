package sessions_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"agentgo/internal/sessions"
)

func TestStore_EntryTreeAndNamedLanes(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	store, err := sessions.Open(db)
	require.NoError(t, err)

	ctx := context.Background()

	// 1. Create a session
	sess, err := store.Create(ctx, "Tree Chat")
	require.NoError(t, err)

	// 2. Append entries on main lane
	e1, err := store.AppendEntry(ctx, sess.ID, "main", sessions.EntryKindMessage, "user", "Step 1", "text", nil)
	require.NoError(t, err)
	assert.Empty(t, e1.ParentID)

	e2, err := store.AppendEntry(ctx, sess.ID, "main", sessions.EntryKindMessage, "assistant", "Response 1", "text", nil)
	require.NoError(t, err)
	assert.Equal(t, e1.ID, e2.ParentID)

	// 3. Verify main lane history
	histMain, err := store.GetLaneHistory(ctx, sess.ID, "main", 10)
	require.NoError(t, err)
	require.Len(t, histMain, 2)
	assert.Equal(t, "Step 1", histMain[0].Content)
	assert.Equal(t, "Response 1", histMain[1].Content)

	// 4. Fork a new lane "experiment" from main
	err = store.ForkLane(ctx, sess.ID, "main", "experiment")
	require.NoError(t, err)

	// 5. Append on experiment lane
	eExp, err := store.AppendEntry(ctx, sess.ID, "experiment", sessions.EntryKindMessage, "user", "Alternative Step 2", "text", nil)
	require.NoError(t, err)
	assert.Equal(t, e2.ID, eExp.ParentID)

	// 6. Append on main lane
	eMain3, err := store.AppendEntry(ctx, sess.ID, "main", sessions.EntryKindMessage, "user", "Main Step 2", "text", nil)
	require.NoError(t, err)
	assert.Equal(t, e2.ID, eMain3.ParentID)

	// 7. Verify main vs experiment branch isolation
	histMainAfter, err := store.GetLaneHistory(ctx, sess.ID, "main", 10)
	require.NoError(t, err)
	require.Len(t, histMainAfter, 3)
	assert.Equal(t, "Main Step 2", histMainAfter[2].Content)

	histExp, err := store.GetLaneHistory(ctx, sess.ID, "experiment", 10)
	require.NoError(t, err)
	require.Len(t, histExp, 3)
	assert.Equal(t, "Alternative Step 2", histExp[2].Content)

	// 8. List Lanes
	lanes, err := store.ListLanes(ctx, sess.ID)
	require.NoError(t, err)
	require.Len(t, lanes, 2)
}
