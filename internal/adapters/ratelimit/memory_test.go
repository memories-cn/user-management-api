package ratelimit

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMemoryLimiter_ValidatesParameters(t *testing.T) {
	_, err := NewMemoryLimiter(0, 1)
	require.Error(t, err, "速率必须为正")

	_, err = NewMemoryLimiter(10, 0)
	require.Error(t, err, "桶容量必须不小于 1")
}

func TestTake_AllowsWithinBurst(t *testing.T) {
	limiter, err := NewMemoryLimiter(1, 3)
	require.NoError(t, err)

	// 桶容量 3，前 3 个请求应立即放行。
	for i := 0; i < 3; i++ {
		decision, err := limiter.Take(context.Background(), "k")
		require.NoError(t, err)
		assert.True(t, decision.Allowed, "第 %d 个请求应在突发容量内", i+1)
	}
}

func TestTake_RejectsBeyondBurst(t *testing.T) {
	limiter, err := NewMemoryLimiter(1, 2)
	require.NoError(t, err)

	for i := 0; i < 2; i++ {
		_, err := limiter.Take(context.Background(), "k")
		require.NoError(t, err)
	}

	decision, err := limiter.Take(context.Background(), "k")
	require.NoError(t, err)
	assert.False(t, decision.Allowed)
	assert.Equal(t, time.Second, decision.RetryAfter, "重试等待应为一个补充周期")
}

func TestTake_IsolatesKeys(t *testing.T) {
	limiter, err := NewMemoryLimiter(1, 1)
	require.NoError(t, err)

	_, err = limiter.Take(context.Background(), "user-a")
	require.NoError(t, err)

	// 另一个键必须完全不受影响——这正是"不同 subject 互不受累"的语义。
	decision, err := limiter.Take(context.Background(), "user-b")
	require.NoError(t, err)
	assert.True(t, decision.Allowed)
}

func TestTake_EmptyKeyIsNotLimited(t *testing.T) {
	limiter, err := NewMemoryLimiter(1, 1)
	require.NoError(t, err)

	// 没有可用限流键时不限流：把所有无键请求归为一类比放过请求更糟。
	for i := 0; i < 10; i++ {
		decision, err := limiter.Take(context.Background(), "")
		require.NoError(t, err)
		assert.True(t, decision.Allowed)
	}
}

func TestTake_RespectsContextCancellation(t *testing.T) {
	limiter, err := NewMemoryLimiter(1, 1)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = limiter.Take(ctx, "k")
	require.Error(t, err, "请求已取消时不应继续消耗令牌")
}

func TestTake_IsConcurrencySafe(t *testing.T) {
	limiter, err := NewMemoryLimiter(1000, 1000)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_, err := limiter.Take(context.Background(), "shared")
				assert.NoError(t, err)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, 1, limiter.Len(), "并发下桶数量应保持稳定")
}

func TestEvict_RemovesStaleBuckets(t *testing.T) {
	limiter, err := NewMemoryLimiter(1, 1)
	require.NoError(t, err)

	now := time.Now()
	limiter.now = func() time.Time { return now }

	_, err = limiter.Take(context.Background(), "active")
	require.NoError(t, err)
	_, err = limiter.Take(context.Background(), "stale")
	require.NoError(t, err)
	require.Equal(t, 2, limiter.Len())

	// 推进到超过保留时长：两个桶都应被清理。
	limiter.evict(now.Add(bucketTTL + time.Minute))
	assert.Zero(t, limiter.Len(), "过期桶必须被清理，否则 map 会无限增长")
}

func TestEvict_KeepsRecentBuckets(t *testing.T) {
	limiter, err := NewMemoryLimiter(1, 1)
	require.NoError(t, err)

	now := time.Now()
	limiter.now = func() time.Time { return now }
	_, err = limiter.Take(context.Background(), "active")
	require.NoError(t, err)

	// 仅推进到保留时长的一半：仍在窗口内，不应被清理。
	limiter.evict(now.Add(bucketTTL / 2))
	assert.Equal(t, 1, limiter.Len())
}
