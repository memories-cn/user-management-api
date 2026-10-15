package middleware

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/example/iam-service/internal/domain/identity"
	"github.com/example/iam-service/internal/ports"
	"github.com/example/iam-service/internal/transport/http/responses"
)

// RequireRateLimit 按 keyFn 给出的键限流。
//
// 与 RequirePermission 的分层刻意不同：权限不足返回 403 表示"身份有效但无权"，
// 限流返回 429 表示"请求过于频繁"，二者不可混淆——否则客户端无法区分
// "我该重试"与"我该放弃"。
//
// 故障语义：limiter.Take 返回错误时**放行**并记录 ERROR 日志。
// 限流是可用性保护而非授权判定；内存限流器结构上不会因数据库或缓存故障失败，
// 唯一现实的故障是内存压力，此时把服务整体打死比放过请求更糟。
func RequireRateLimit(limiter ports.RateLimiter, keyFn func(*gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := keyFn(c)

		decision, err := limiter.Take(c.Request.Context(), key)
		if err != nil {
			// 保护组件故障不演变成服务中断，但必须留下可排查的痕迹。
			slog.ErrorContext(c.Request.Context(), "限流器故障，本次请求放行",
				"error", err,
				"request_id", c.GetString(responses.RequestIDContextKey))
			c.Next()
			return
		}

		if decision.Allowed {
			c.Next()
			return
		}

		retryAfter := int(decision.RetryAfter / time.Second)
		if retryAfter < 1 {
			retryAfter = 1
		}
		c.Header("Retry-After", strconv.Itoa(retryAfter))

		slog.WarnContext(c.Request.Context(), "请求被限流",
			"key", key,
			"path", c.Request.URL.Path,
			"request_id", c.GetString(responses.RequestIDContextKey))

		responses.Fail(c, http.StatusTooManyRequests, responses.CodeRateLimited, "too many requests")
		c.Abort()
	}
}

// SubjectRateLimitKey 返回已认证用户的限流键。
//
// 身份只来自已验证的 Principal，绝不接受请求头声明的用户标识
// （docs/TDD.md 10.3：内部网络与请求头都不构成认证）。
func SubjectRateLimitKey(c *gin.Context) string {
	if principal, ok := identity.FromContext(c.Request.Context()); ok && principal.Authenticated && principal.UserID != "" {
		return "subject:" + principal.UserID
	}
	return ""
}

// IPRateLimitKey 返回未认证请求的限流键。
//
// 注意：ClientIP() 的可信度取决于 SetTrustedProxies 的配置。
// 未配置可信代理时该值来自直接连接对端，部署在反代之后则可能是代理地址——
// 这会让全部请求共享同一个桶，因此生产环境必须显式配置 TRUSTED_PROXIES。
func IPRateLimitKey(c *gin.Context) string {
	return "ip:" + c.ClientIP()
}

// AuthenticatedOrIPRateLimitKey 已认证按用户、未认证按 IP 限流。
func AuthenticatedOrIPRateLimitKey(c *gin.Context) string {
	if key := SubjectRateLimitKey(c); key != "" {
		return key
	}
	return IPRateLimitKey(c)
}
