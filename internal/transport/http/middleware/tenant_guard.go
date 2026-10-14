package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/example/iam-service/internal/application/tenancy"
	"github.com/example/iam-service/internal/domain/identity"
	"github.com/example/iam-service/internal/transport/http/responses"
)

// 租户级错误码。
const (
	// CodeTenantSuspended 表示租户已停用。
	CodeTenantSuspended = "TENANT_SUSPENDED"
	// CodeMembershipInactive 表示成员关系不可用。
	CodeMembershipInactive = "MEMBERSHIP_INACTIVE"
	// CodeNotFound 表示资源不存在或无权访问。
	CodeNotFound = "NOT_FOUND"
)

// tenantIDKey 是租户 ID 在上下文中的键。
type tenantIDKey struct{}

// TenantAccessChecker 抽象租户访问校验，便于用替身覆盖失败分支。
type TenantAccessChecker interface {
	Check(ctx context.Context, userID, tenantID string) error
}

// WithTenantID 把已校验的租户 ID 写入上下文。
func WithTenantID(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, tenantIDKey{}, tenantID)
}

// TenantIDFrom 读取上下文中已校验的租户 ID。
func TenantIDFrom(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(tenantIDKey{}).(string)
	return id, ok && id != ""
}

// TenantGuard 校验当前身份能否访问路径中的租户。
//
// 租户标识只从**路径参数**读取，不接受任何请求头（见 docs/TDD.md 6.3、10.3）。
// 非成员与租户不存在统一返回 404，避免通过状态码枚举租户是否存在。
func TenantGuard(access TenantAccessChecker) gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, ok := identity.FromContext(c.Request.Context())
		if !ok {
			responses.Fail(c, http.StatusUnauthorized, CodeUnauthenticated, "missing or invalid credentials")
			c.Abort()
			return
		}

		tenantID := c.Param("tenantID")
		if _, err := uuid.Parse(tenantID); err != nil {
			responses.Fail(c, http.StatusNotFound, CodeNotFound, "resource not found")
			c.Abort()
			return
		}

		if err := access.Check(c.Request.Context(), principal.UserID, tenantID); err != nil {
			handleTenantAccessError(c, err)
			return
		}

		c.Request = c.Request.WithContext(WithTenantID(c.Request.Context(), tenantID))
		c.Next()
	}
}

// handleTenantAccessError 把租户访问校验失败映射为合适的状态码。
func handleTenantAccessError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, tenancy.ErrTenantSuspended):
		slog.WarnContext(c.Request.Context(), "tenant suspended", "error", err)
		responses.Fail(c, http.StatusForbidden, CodeTenantSuspended, "tenant is suspended")
	case errors.Is(err, tenancy.ErrMembershipInactive):
		slog.WarnContext(c.Request.Context(), "membership inactive", "error", err)
		responses.Fail(c, http.StatusForbidden, CodeMembershipInactive, "membership is not active")
	default:
		// 租户不存在与非成员都按未找到处理，服务端日志保留真实原因。
		slog.WarnContext(c.Request.Context(), "tenant access denied", "error", err)
		responses.Fail(c, http.StatusNotFound, CodeNotFound, "resource not found")
	}
	c.Abort()
}
