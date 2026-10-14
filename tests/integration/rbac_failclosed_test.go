package integration

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRBAC_AuthorizeFailsClosedWhenDatabaseUnavailable 校验依赖不可用时不会放行。
//
// 安全要求（docs/TDD.md 7.5）：授权依赖不可用必须失败关闭，
// 既不能返回 allowed=true，也不能把故障降级成"权限不足"后让调用方自行放行。
func TestRBAC_AuthorizeFailsClosedWhenDatabaseUnavailable(t *testing.T) {
	f := newRBACFixture(t)

	// 先确认依赖正常时判定可用。
	assert.True(t, f.mustAuthorize(f.ownerToken, "member", "read"))

	// 断开数据库连接，模拟授权依赖不可用。
	sqlDB, err := f.stack.db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	rec := f.stack.do(http.MethodPost, "/api/v1/authorize", f.ownerToken, map[string]string{
		"tenant_id": f.tenantID,
		"resource":  "member",
		"action":    "read",
	})

	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "DEPENDENCY_UNAVAILABLE")
	assert.NotContains(t, rec.Body.String(), `"allowed":true`, "依赖故障时绝不能放行")
}

// TestRBAC_TenantRoutesFailClosedWhenDatabaseUnavailable 校验租户级管理接口同样失败关闭。
func TestRBAC_TenantRoutesFailClosedWhenDatabaseUnavailable(t *testing.T) {
	f := newRBACFixture(t)

	rec := f.stack.do(http.MethodGet, "/api/v1/tenants/"+f.tenantID+"/members", f.ownerToken, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	sqlDB, err := f.stack.db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	rec = f.stack.do(http.MethodGet, "/api/v1/tenants/"+f.tenantID+"/members", f.ownerToken, nil)

	// 租户守卫阶段就查库失败：返回 503 而不是继续用可能过期的授权状态放行。
	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
}
