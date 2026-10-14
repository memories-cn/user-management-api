package middleware

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/example/iam-service/internal/application/platformadmin"
	"github.com/example/iam-service/internal/domain/identity"
	"github.com/example/iam-service/internal/transport/http/responses"
)

// CodeForbidden 表示身份有效但权限不足。
const CodeForbidden = "FORBIDDEN"

// RequirePlatformAdmin 只允许持有平台管理员角色的身份继续。
//
// 必须挂在 AuthN 之后：身份来自请求上下文中已验证的 Principal，
// 不接受任何请求头传入的角色信息，也不存在白名单或应急通道。
func RequirePlatformAdmin(policy *platformadmin.Policy) gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, ok := identity.FromContext(c.Request.Context())
		if !ok {
			responses.Fail(c, http.StatusUnauthorized, CodeUnauthenticated, "missing or invalid credentials")
			c.Abort()
			return
		}

		if !policy.IsPlatformAdmin(principal) {
			slog.WarnContext(c.Request.Context(), "platform admin role required",
				"subject", principal.Subject,
				"roles", principal.Roles,
				"request_id", c.GetString(responses.RequestIDContextKey))
			responses.Fail(c, http.StatusForbidden, CodeForbidden, "platform admin role required")
			c.Abort()
			return
		}

		c.Next()
	}
}
