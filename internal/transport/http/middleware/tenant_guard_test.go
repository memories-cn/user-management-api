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

	"github.com/example/iam-service/internal/application/tenancy"
	"github.com/example/iam-service/internal/domain/identity"
	"github.com/example/iam-service/internal/transport/http/responses"
)

// testTenantUUID 是合法的租户标识，用于通过守卫的 UUID 校验。
const testTenantUUID = "11111111-2222-3333-4444-555555555555"

// stubAccess 是可控的租户访问校验替身。
type stubAccess struct {
	err error
	// seen 记录最后一次校验的入参。
	seenUserID   string
	seenTenantID string
}

func (s *stubAccess) Check(_ context.Context, userID, tenantID string) error {
	s.seenUserID, s.seenTenantID = userID, tenantID
	return s.err
}

// newTenantRouter 构建带租户守卫的测试路由。
func newTenantRouter(access TenantAccessChecker, principal *identity.Principal) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestID())
	r.Use(func(c *gin.Context) {
		if principal != nil {
			c.Request = c.Request.WithContext(identity.WithPrincipal(c.Request.Context(), *principal))
		}
	})
	guarded := r.Group("/tenants/:tenantID")
	guarded.Use(TenantGuard(access))
	guarded.GET("/members", func(c *gin.Context) {
		id, ok := TenantIDFrom(c.Request.Context())
		responses.OK(c, map[string]any{"tenant_id": id, "ok": ok})
	})
	return r
}

func getMembers(t *testing.T, router *gin.Engine, tenantID string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/tenants/"+tenantID+"/members", nil)
	req.Header.Set(responses.HeaderRequestID, "req-tenant-1")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// authedPrincipal 返回已认证身份。
func authedPrincipal() *identity.Principal {
	return &identity.Principal{Subject: "zitadel-1", UserID: "user-uuid-1", Authenticated: true}
}

func TestTenantGuard_PassesTenantIDFromPath(t *testing.T) {
	access := &stubAccess{}
	rec := getMembers(t, newTenantRouter(access, authedPrincipal()), testTenantUUID, nil)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, testTenantUUID, access.seenTenantID, "租户标识必须来自路径参数")
	assert.Equal(t, "user-uuid-1", access.seenUserID)
	assert.Contains(t, rec.Body.String(), `"tenant_id":"11111111-2222-3333-4444-555555555555"`)
}

func TestTenantGuard_IgnoresTenantHeader(t *testing.T) {
	access := &stubAccess{}

	rec := getMembers(t, newTenantRouter(access, authedPrincipal()), testTenantUUID, map[string]string{
		"X-Tenant-ID": "tenant-2",
	})

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, testTenantUUID, access.seenTenantID, "请求头中的租户标识不得被信任")
}

func TestTenantGuard_RejectsNonMember(t *testing.T) {
	access := &stubAccess{err: tenancy.ErrTenantNotFound}

	rec := getMembers(t, newTenantRouter(access, authedPrincipal()), testTenantUUID, nil)

	require.Equal(t, http.StatusNotFound, rec.Code, "非成员返回 404 以避免枚举租户")
	assert.Contains(t, rec.Body.String(), CodeNotFound)
}

func TestTenantGuard_RejectsSuspendedTenant(t *testing.T) {
	access := &stubAccess{err: tenancy.ErrTenantSuspended}

	rec := getMembers(t, newTenantRouter(access, authedPrincipal()), testTenantUUID, nil)

	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), CodeTenantSuspended)
}

func TestTenantGuard_RejectsInactiveMembership(t *testing.T) {
	access := &stubAccess{err: tenancy.ErrMembershipInactive}

	rec := getMembers(t, newTenantRouter(access, authedPrincipal()), testTenantUUID, nil)

	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), CodeMembershipInactive)
}

func TestTenantGuard_FailsClosedOnDependencyError(t *testing.T) {
	access := &stubAccess{err: errors.New("database down")}

	rec := getMembers(t, newTenantRouter(access, authedPrincipal()), testTenantUUID, nil)

	// 依赖不可用必须返回 503 而不是 404：404 会把故障伪装成"资源不存在"，
	// 既掩盖可用性问题，也让调用方无法区分"真的无权访问"与"服务不可用"。
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), CodeDependencyUnavailable)
}

func TestTenantGuard_HidesTenantExistence(t *testing.T) {
	access := &stubAccess{err: tenancy.ErrTenantNotFound}

	rec := getMembers(t, newTenantRouter(access, authedPrincipal()), testTenantUUID, nil)

	// 租户不存在与非成员统一 404，避免通过状态码枚举租户是否存在。
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), CodeNotFound)
}

func TestTenantGuard_RejectsUnauthenticated(t *testing.T) {
	access := &stubAccess{}

	rec := getMembers(t, newTenantRouter(access, nil), testTenantUUID, nil)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Empty(t, access.seenTenantID, "未认证时不执行租户校验")
}

func TestTenantIDFrom_Missing(t *testing.T) {
	_, ok := TenantIDFrom(context.Background())

	assert.False(t, ok)
}
