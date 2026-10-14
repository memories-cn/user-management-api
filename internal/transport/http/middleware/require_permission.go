package middleware

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/example/iam-service/internal/application/authorization"
	"github.com/example/iam-service/internal/domain/identity"
	"github.com/example/iam-service/internal/transport/http/responses"
)

// 本中间件复用平台管理员与认证中间件已定义的错误码常量：
// CodeForbidden（platform_admin.go）与 CodeDependencyUnavailable（authn.go），
// 保证同一语义在整个传输层只有一种错误码写法。

// PermissionChecker 抽象授权判定，便于用替身覆盖拒绝与故障分支。
type PermissionChecker interface {
	// Authorize 判定主体能否在租户内对资源执行操作。
	// 约定：业务性拒绝返回 Allowed=false 且 err=nil；依赖故障返回 err。
	Authorize(ctx context.Context, principal identity.Principal, tenantID, resource, action string) (*authorization.Decision, error)
}

// RequirePermission 校验当前身份在已校验的租户下是否具备指定权限。
//
// 前置条件：本中间件必须挂在 TenantGuard 之后，租户 ID 只信任 TenantGuard 写入上下文的值，
// 不接受任何请求头或请求体传入的租户标识（见 docs/TDD.md 6.3、10.3）。
//
// 失败关闭：授权用例只在"依赖不可用"时返回 error，因此 error 一律按 503 处理，
// 既不降级为"权限不足"也不放行（docs/TDD.md 7.5）。
func RequirePermission(checker PermissionChecker, resource, action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, ok := identity.FromContext(c.Request.Context())
		if !ok || !principal.Authenticated {
			responses.Fail(c, http.StatusUnauthorized, CodeUnauthenticated, "missing or invalid credentials")
			c.Abort()
			return
		}

		tenantID, ok := TenantIDFrom(c.Request.Context())
		if !ok {
			responses.Fail(c, http.StatusNotFound, CodeNotFound, "resource not found")
			c.Abort()
			return
		}

		decision, err := checker.Authorize(c.Request.Context(), principal, tenantID, resource, action)
		if err != nil {
			slog.ErrorContext(c.Request.Context(), "授权判定不可用",
				"tenant_id", tenantID, "resource", resource, "action", action, "error", err)
			responses.Fail(c, http.StatusServiceUnavailable, CodeDependencyUnavailable, "authorization service unavailable")
			c.Abort()
			return
		}

		if !decision.Allowed {
			slog.WarnContext(c.Request.Context(), "授权判定被拒绝",
				"user_id", principal.UserID, "tenant_id", tenantID,
				"resource", resource, "action", action)
			responses.Fail(c, http.StatusForbidden, CodeForbidden, "permission denied")
			c.Abort()
			return
		}
		c.Next()
	}
}
