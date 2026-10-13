package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	appuser "github.com/example/iam-service/internal/application/user"
	"github.com/example/iam-service/internal/domain/identity"
	"github.com/example/iam-service/internal/domain/user"
	"github.com/example/iam-service/internal/ports"
	"github.com/example/iam-service/internal/transport/http/responses"
)

// 认证相关错误码。
const (
	// CodeUnauthenticated 表示缺少或无效的凭证。
	CodeUnauthenticated = "UNAUTHENTICATED"
	// CodeUserDisabled 表示身份有效但用户已被停用。
	CodeUserDisabled = "USER_DISABLED"
	// CodeDependencyUnavailable 表示认证链路依赖不可用，必须失败关闭。
	CodeDependencyUnavailable = "DEPENDENCY_UNAVAILABLE"
	// CodeInternal 表示未预期的服务端错误。
	CodeInternal = "INTERNAL"
)

// UserEnsurer 抽象"依据已验证声明确保本地用户可用"的用例，
// 便于传输层用测试替身覆盖 403/503 等分支。
type UserEnsurer interface {
	Ensure(ctx context.Context, claims *ports.Claims) (*user.User, error)
}

// AuthN 校验访问令牌并注入可信身份。
//
// 错误语义（见 docs/TDD.md 9.6）：
//   - 401：缺少凭证、凭证格式错误或凭证无效；
//   - 403：身份有效但用户已停用；
//   - 503：身份平台或数据库不可用，一律失败关闭，绝不放行。
//
// 安全约束：身份的唯一来源是 Authorization 头中的已验证令牌，
// 任何 X-User-ID 之类的请求头都被忽略（见 docs/TDD.md 10.3）。
func AuthN(verifier ports.TokenVerifier, ensurer UserEnsurer) gin.HandlerFunc {
	return func(c *gin.Context) {
		rawToken := bearerToken(c.GetHeader("Authorization"))
		if rawToken == "" {
			responses.Fail(c, http.StatusUnauthorized, CodeUnauthenticated, "missing or invalid credentials")
			c.Abort()
			return
		}

		claims, err := verifier.Verify(c.Request.Context(), rawToken)
		if err != nil {
			handleVerifyError(c, err)
			return
		}

		u, err := ensurer.Ensure(c.Request.Context(), claims)
		if err != nil {
			handleEnsureError(c, err)
			return
		}

		// Authenticated 只由中间件设置，不允许请求参数覆盖。
		principal := identity.Principal{
			Subject:       claims.Subject,
			UserID:        u.ID.String(),
			ClientID:      claims.ClientID,
			Authenticated: true,
		}
		c.Request = c.Request.WithContext(identity.WithPrincipal(c.Request.Context(), principal))
		c.Next()
	}
}

// handleVerifyError 把令牌校验失败映射为合适的状态码。
func handleVerifyError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ports.ErrIdentityProviderUnavailable):
		slog.WarnContext(c.Request.Context(), "identity provider unavailable", "error", err)
		responses.Fail(c, http.StatusServiceUnavailable, CodeDependencyUnavailable, "identity provider unavailable")
	default:
		slog.WarnContext(c.Request.Context(), "invalid access token", "error", err)
		responses.Fail(c, http.StatusUnauthorized, CodeUnauthenticated, "missing or invalid credentials")
	}
	c.Abort()
}

// handleEnsureError 把用户同步失败映射为合适的状态码。
func handleEnsureError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, appuser.ErrUserDisabled):
		slog.WarnContext(c.Request.Context(), "user disabled", "error", err)
		responses.Fail(c, http.StatusForbidden, CodeUserDisabled, "user is disabled")
	case errors.Is(err, appuser.ErrInvalidClaims):
		slog.WarnContext(c.Request.Context(), "invalid claims", "error", err)
		responses.Fail(c, http.StatusUnauthorized, CodeUnauthenticated, "missing or invalid credentials")
	default:
		// 数据库不可用等故障必须失败关闭，不能因依赖故障放行。
		slog.ErrorContext(c.Request.Context(), "failed to ensure user", "error", err)
		responses.Fail(c, http.StatusServiceUnavailable, CodeDependencyUnavailable, "dependency unavailable")
	}
	c.Abort()
}

// bearerToken 从 Authorization 头中提取 Bearer 令牌，格式不符返回空串。
func bearerToken(header string) string {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	token := strings.TrimSpace(header[len(prefix):])
	if token == "" {
		return ""
	}
	return token
}
