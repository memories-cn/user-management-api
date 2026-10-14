package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/iam-service/internal/application/platformadmin"
	"github.com/example/iam-service/internal/domain/identity"
	"github.com/example/iam-service/internal/transport/http/responses"
)

// newPlatformRouter 构建挂载平台管理员守卫的测试路由。
func newPlatformRouter(policy *platformadmin.Policy, principal *identity.Principal) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestID())
	r.Use(func(c *gin.Context) {
		if principal != nil {
			c.Request = c.Request.WithContext(identity.WithPrincipal(c.Request.Context(), *principal))
		}
	})
	r.Use(RequirePlatformAdmin(policy))
	r.POST("/platform/tenants", func(c *gin.Context) {
		responses.OK(c, map[string]string{"status": "created"})
	})
	return r
}

func postPlatform(t *testing.T, router *gin.Engine) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/platform/tenants", nil)
	req.Header.Set(responses.HeaderRequestID, "req-admin-1")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// TestRequirePlatformAdmin_AllowsAdmin 校验带平台管理员角色时放行。
func TestRequirePlatformAdmin_AllowsAdmin(t *testing.T) {
	policy, err := platformadmin.NewPolicy("platform_admin")
	require.NoError(t, err)

	rec := postPlatform(t, newPlatformRouter(policy, &identity.Principal{
		Subject:       "zitadel-001",
		Authenticated: true,
		Roles:         []string{"platform_admin"},
	}))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "created")
}

// TestRequirePlatformAdmin_RejectsNormalUser 校验普通用户被拒绝。
func TestRequirePlatformAdmin_RejectsNormalUser(t *testing.T) {
	policy, err := platformadmin.NewPolicy("platform_admin")
	require.NoError(t, err)

	rec := postPlatform(t, newPlatformRouter(policy, &identity.Principal{
		Subject:       "zitadel-002",
		Authenticated: true,
		Roles:         []string{"member"},
	}))

	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), CodeForbidden)
	assert.Equal(t, "req-admin-1", rec.Header().Get(responses.HeaderRequestID))
}

// TestRequirePlatformAdmin_RejectsForgedRoleHeader 校验请求头伪造角色无效。
func TestRequirePlatformAdmin_RejectsForgedRoleHeader(t *testing.T) {
	policy, err := platformadmin.NewPolicy("platform_admin")
	require.NoError(t, err)

	router := newPlatformRouter(policy, &identity.Principal{
		Subject:       "zitadel-002",
		Authenticated: true,
	})
	req := httptest.NewRequest(http.MethodPost, "/platform/tenants", nil)
	req.Header.Set("X-Roles", "platform_admin")
	req.Header.Set("X-Platform-Admin", "true")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code, "角色只认已验证令牌，不接受请求头")
}

// TestRequirePlatformAdmin_RejectsUnauthenticated 校验无身份时返回 401。
func TestRequirePlatformAdmin_RejectsUnauthenticated(t *testing.T) {
	policy, err := platformadmin.NewPolicy("platform_admin")
	require.NoError(t, err)

	rec := postPlatform(t, newPlatformRouter(policy, nil))

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestRequirePlatformAdmin_RejectsUnauthenticatedPrincipal 校验未认证身份即使带角色也被拒绝。
//
// Authenticated 只允许认证中间件设置，FromContext 对未认证身份返回 false，因此是 401。
func TestRequirePlatformAdmin_RejectsUnauthenticatedPrincipal(t *testing.T) {
	policy, err := platformadmin.NewPolicy("platform_admin")
	require.NoError(t, err)

	rec := postPlatform(t, newPlatformRouter(policy, &identity.Principal{
		Subject: "zitadel-003",
		Roles:   []string{"platform_admin"},
	}))

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

// 确保 context 包被使用。
var _ = context.Background
