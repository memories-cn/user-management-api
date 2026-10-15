package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/iam-service/internal/adapters/ratelimit"
	"github.com/example/iam-service/internal/domain/identity"
	"github.com/example/iam-service/internal/ports"
)

// errLimiterBroken 模拟限流器内部故障。
var errLimiterBroken = errors.New("limiter broken")

// brokenLimiter 永远返回错误，用于验证"保护组件故障不演变成服务中断"。
type brokenLimiter struct{}

func (brokenLimiter) Take(context.Context, string) (ports.RateLimitDecision, error) {
	return ports.RateLimitDecision{}, errLimiterBroken
}

// stubLimiter 返回预置结果。
type stubLimiter struct {
	decision ports.RateLimitDecision
	err      error
	// seen 记录最近一次收到的键。
	seen string
}

func (s *stubLimiter) Take(_ context.Context, key string) (ports.RateLimitDecision, error) {
	s.seen = key
	return s.decision, s.err
}

// serveRateLimited 用给定限流器发起一次请求，返回响应与是否触达后续处理器。
func serveRateLimited(limiter ports.RateLimiter, keyFn func(*gin.Context) string, principal *identity.Principal) (*httptest.ResponseRecorder, bool) {
	gin.SetMode(gin.TestMode)

	reached := false
	router := gin.New()
	router.GET("/resource",
		func(c *gin.Context) {
			if principal != nil {
				c.Request = c.Request.WithContext(
					identity.WithPrincipal(c.Request.Context(), *principal))
			}
			c.Next()
		},
		RequireRateLimit(limiter, keyFn),
		func(c *gin.Context) { reached = true; c.Status(http.StatusOK) },
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/resource", nil))
	return rec, reached
}

func TestRequireRateLimit_AllowsWhenUnderLimit(t *testing.T) {
	limiter := &stubLimiter{decision: ports.RateLimitDecision{Allowed: true}}

	rec, reached := serveRateLimited(limiter, AuthenticatedOrIPRateLimitKey, nil)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, reached)
}

func TestRequireRateLimit_RejectsWith429AndRetryAfter(t *testing.T) {
	limiter := &stubLimiter{
		decision: ports.RateLimitDecision{Allowed: false, RetryAfter: 2 * time.Second},
	}

	rec, reached := serveRateLimited(limiter, AuthenticatedOrIPRateLimitKey, nil)

	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, "2", rec.Header().Get("Retry-After"))
	assert.Contains(t, rec.Body.String(), "RATE_LIMITED")
	assert.False(t, reached, "被限流的请求必须中止，后续处理器不得执行")
}

func TestRequireRateLimit_FailsOpenOnLimiterError(t *testing.T) {
	// 限流是可用性保护而非授权判定：保护组件故障时放行并记录错误，
	// 把服务整体打死比放过请求更糟。
	rec, reached := serveRateLimited(brokenLimiter{}, AuthenticatedOrIPRateLimitKey, nil)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, reached)
}

func TestRequireRateLimit_RoundsRetryAfterUp(t *testing.T) {
	limiter := &stubLimiter{
		decision: ports.RateLimitDecision{Allowed: false, RetryAfter: 10 * time.Millisecond},
	}

	rec, _ := serveRateLimited(limiter, AuthenticatedOrIPRateLimitKey, nil)

	// 亚秒级等待向上取整到 1 秒，避免客户端立即重试形成尖峰。
	assert.Equal(t, "1", rec.Header().Get("Retry-After"))
}

func TestRateLimitKey_PrefersAuthenticatedSubject(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	var captured string
	router.GET("/resource",
		func(c *gin.Context) {
			c.Request = c.Request.WithContext(identity.WithPrincipal(c.Request.Context(),
				identity.Principal{UserID: "user-1", Authenticated: true}))
			c.Next()
		},
		RequireRateLimit(&stubLimiter{decision: ports.RateLimitDecision{Allowed: true}},
			func(c *gin.Context) string {
				captured = AuthenticatedOrIPRateLimitKey(c)
				return captured
			}),
		func(c *gin.Context) { c.Status(http.StatusOK) },
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/resource", nil))

	assert.Equal(t, "subject:user-1", captured)
}

func TestRateLimitKey_FallsBackToIPForUnauthenticated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	var captured string
	router.GET("/resource",
		func(c *gin.Context) {
			c.Request = c.Request.WithContext(identity.WithPrincipal(c.Request.Context(),
				identity.Principal{UserID: "user-1"})) // Authenticated 为 false
			c.Next()
		},
		RequireRateLimit(&stubLimiter{decision: ports.RateLimitDecision{Allowed: true}},
			func(c *gin.Context) string {
				captured = AuthenticatedOrIPRateLimitKey(c)
				return captured
			}),
		func(c *gin.Context) { c.Status(http.StatusOK) },
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/resource", nil))

	assert.Contains(t, captured, "ip:", "未认证请求必须按 IP 分桶")
}

// TestRateLimitKeys_DifferentSubjectsDoNotInterfere 用一个真实限流器验证隔离性。
func TestRateLimitKeys_DifferentSubjectsDoNotInterfere(t *testing.T) {
	limiter, err := ratelimit.NewMemoryLimiter(1, 1)
	require.NoError(t, err)

	// user-1 打满自己的桶。
	rec, _ := serveRateLimited(limiter, func(c *gin.Context) string { return "subject:user-1" },
		&identity.Principal{UserID: "user-1", Authenticated: true})
	require.Equal(t, http.StatusOK, rec.Code)

	rec, _ = serveRateLimited(limiter, func(c *gin.Context) string { return "subject:user-1" },
		&identity.Principal{UserID: "user-1", Authenticated: true})
	require.Equal(t, http.StatusTooManyRequests, rec.Code, "同一个用户应被限流")

	// user-2 完全不受影响。
	rec, reached := serveRateLimited(limiter, func(c *gin.Context) string { return "subject:user-2" },
		&identity.Principal{UserID: "user-2", Authenticated: true})
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, reached, "其他用户不得被连带限流")
}
