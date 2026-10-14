package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/iam-service/internal/application/authorization"
	"github.com/example/iam-service/internal/domain/identity"
)

// errDependency 模拟授权依赖不可用。
var errDependency = errors.New("授权依赖不可用")

// fakeChecker 是授权判定替身。
type fakeChecker struct {
	allowed bool
	err     error
	// seen 记录收到的租户与资源，用于验证租户只来自上下文。
	seen struct {
		tenantID string
		resource string
		action   string
	}
}

func (f *fakeChecker) Authorize(_ context.Context, _ identity.Principal, tenantID, resource, action string) (*authorization.Decision, error) {
	f.seen.tenantID, f.seen.resource, f.seen.action = tenantID, resource, action
	if f.err != nil {
		return nil, f.err
	}
	return &authorization.Decision{Allowed: f.allowed, TenantID: tenantID, Resource: resource, Action: action}, nil
}

// newPermissionContext 构造带有已认证身份与已校验租户的请求上下文。
func newPermissionContext(tenantID string, authenticated bool, checker *fakeChecker, resource, action string) (*httptest.ResponseRecorder, *gin.Context, *gin.Engine) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.GET("/tenants/:tenantID/resource",
		func(c *gin.Context) {
			// 模拟 TenantGuard：把已校验的租户写入上下文，并注入 Principal。
			ctx := WithTenantID(c.Request.Context(), tenantID)
			if authenticated {
				ctx = identity.WithPrincipal(ctx, identity.Principal{UserID: "user-1", Authenticated: true})
			}
			c.Request = c.Request.WithContext(ctx)
			c.Next()
		},
		RequirePermission(checker, resource, action),
		func(c *gin.Context) { c.Status(http.StatusOK) },
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tenants/"+tenantID+"/resource", nil)
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	return rec, c, router
}

// serve 执行请求并返回响应码。
func serve(router *gin.Engine, tenantID string) int {
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/tenants/"+tenantID+"/resource", nil))
	return rec.Code
}

func TestRequirePermission_AllowsWhenDecisionPermits(t *testing.T) {
	checker := &fakeChecker{allowed: true}
	_, _, router := newPermissionContext("11111111-1111-1111-1111-111111111111", true, checker, "member", "invite")

	code := serve(router, "11111111-1111-1111-1111-111111111111")

	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", checker.seen.tenantID)
	assert.Equal(t, "member", checker.seen.resource)
	assert.Equal(t, "invite", checker.seen.action)
}

func TestRequirePermission_DeniesWhenNotAllowed(t *testing.T) {
	checker := &fakeChecker{allowed: false}
	_, _, router := newPermissionContext("11111111-1111-1111-1111-111111111111", true, checker, "member", "invite")

	code := serve(router, "11111111-1111-1111-1111-111111111111")

	assert.Equal(t, http.StatusForbidden, code)
}

func TestRequirePermission_FailsClosedOnDependencyError(t *testing.T) {
	checker := &fakeChecker{err: errDependency}
	_, _, router := newPermissionContext("11111111-1111-1111-1111-111111111111", true, checker, "member", "invite")

	code := serve(router, "11111111-1111-1111-1111-111111111111")

	assert.Equal(t, http.StatusServiceUnavailable, code, "依赖不可用必须失败关闭，不得放行也不得降级为 403")
}

func TestRequirePermission_RejectsUnauthenticated(t *testing.T) {
	checker := &fakeChecker{allowed: true}
	_, _, router := newPermissionContext("11111111-1111-1111-1111-111111111111", false, checker, "member", "invite")

	code := serve(router, "11111111-1111-1111-1111-111111111111")

	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Empty(t, checker.seen.tenantID, "未认证请求不得进入授权判定")
}

func TestRequirePermission_RejectsMissingTenantContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	checker := &fakeChecker{allowed: true}

	// 不经过 TenantGuard，上下文中没有已校验租户。
	router := gin.New()
	router.GET("/resource",
		func(c *gin.Context) {
			c.Request = c.Request.WithContext(
				identity.WithPrincipal(c.Request.Context(), identity.Principal{UserID: "user-1", Authenticated: true}))
			c.Next()
		},
		RequirePermission(checker, "member", "invite"),
		func(c *gin.Context) { c.Status(http.StatusOK) },
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/resource", nil))

	assert.Equal(t, http.StatusNotFound, rec.Code, "缺少已校验租户时不得继续处理")
}

// 编译期断言：确保替身实现接口。
var _ PermissionChecker = (*fakeChecker)(nil)

func TestRequirePermission_AbortsChain(t *testing.T) {
	checker := &fakeChecker{allowed: false}
	gin.SetMode(gin.TestMode)

	reached := false
	router := gin.New()
	router.GET("/resource",
		func(c *gin.Context) {
			c.Request = c.Request.WithContext(WithTenantID(
				identity.WithPrincipal(c.Request.Context(),
					identity.Principal{UserID: "user-1", Authenticated: true}),
				"11111111-1111-1111-1111-111111111111"))
			c.Next()
		},
		RequirePermission(checker, "member", "invite"),
		func(c *gin.Context) { reached = true; c.Status(http.StatusOK) },
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/resource", nil))

	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.False(t, reached, "被拒绝的请求必须中止，后续处理器不得执行")
}
