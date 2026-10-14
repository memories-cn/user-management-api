package audit

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestContext_RoundTrip(t *testing.T) {
	rc := RequestContext{RequestID: "req-1", IPAddress: "203.0.113.7", UserAgent: "curl/8"}

	ctx := WithRequestContext(context.Background(), rc)
	got, ok := RequestContextFrom(ctx)

	require.True(t, ok)
	assert.Equal(t, rc, got)
}

func TestRequestContext_MissingReturnsZeroValue(t *testing.T) {
	// 缺失审计上下文不得让业务失败：留痕是必要项，请求上下文只是增强信息。
	got, ok := RequestContextFrom(context.Background())

	assert.False(t, ok)
	assert.Equal(t, RequestContext{}, got)
}
