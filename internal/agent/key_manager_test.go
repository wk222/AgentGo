package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAPIKeyManager_AffinityAndLeastBusy(t *testing.T) {
	km := NewAPIKeyManager(1 * time.Minute)
	keys := []string{"key-1", "key-2", "key-3"}

	// 1. First allocation: should pick key-1 (or any least busy)
	k1, err := km.AllocateKey(keys, "sess-A")
	assert.NoError(t, err)
	assert.Equal(t, "key-1", k1)
	assert.Equal(t, 1, km.GetActiveCount("key-1"))

	// 2. Second allocation for different session: should pick least busy (key-2)
	k2, err := km.AllocateKey(keys, "sess-B")
	assert.NoError(t, err)
	assert.Equal(t, "key-2", k2)

	// 3. Third allocation for same session "sess-A": should reuse "key-1" (KV cache affinity!)
	k3, err := km.AllocateKey(keys, "sess-A")
	assert.NoError(t, err)
	assert.Equal(t, "key-1", k3)
	assert.Equal(t, 2, km.GetActiveCount("key-1"))

	// 4. Release
	km.ReleaseKey("key-1")
	assert.Equal(t, 1, km.GetActiveCount("key-1"))
	km.ReleaseKey("key-1")
	assert.Equal(t, 0, km.GetActiveCount("key-1"))
}

func TestConcurrencyController(t *testing.T) {
	cc := NewConcurrencyController(2)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Acquire 2 slots
	assert.NoError(t, cc.Acquire(ctx, "key-1", 2))
	assert.NoError(t, cc.Acquire(ctx, "key-1", 2))

	// Third acquire with short timeout should fail
	ctxShort, cancelShort := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelShort()
	err := cc.Acquire(ctxShort, "key-1", 2)
	assert.Error(t, err)

	// Release one
	cc.Release("key-1")

	// Now acquire should succeed
	assert.NoError(t, cc.Acquire(ctx, "key-1", 2))
	cc.Release("key-1")
	cc.Release("key-1")
}

func TestWithExponentialBackoff(t *testing.T) {
	attempts := 0
	op := func() error {
		attempts++
		if attempts < 3 {
			return assert.AnError
		}
		return nil
	}

	err := WithExponentialBackoff(context.Background(), 4, 10*time.Millisecond, op)
	assert.NoError(t, err)
	assert.Equal(t, 3, attempts)
}
