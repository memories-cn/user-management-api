package integration

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/iam-service/internal/transport/http/middleware"
)

// expectedTenantPermissions 声明每条租户级路由所需的权限。
//
// 这是"漏挂权限"的防线：新增租户级路由时若忘记在此登记，
// TestTenantRoutesDeclarePermissions 会直接失败。
var expectedTenantPermissions = map[string][2]string{
	"GET /api/v1/tenants/:tenantID":                              {"tenant", "read"},
	"PATCH /api/v1/tenants/:tenantID":                            {"tenant", "update"},
	"GET /api/v1/tenants/:tenantID/members":                      {"member", "read"},
	"GET /api/v1/tenants/:tenantID/members/:userID":              {"member", "read"},
	"PATCH /api/v1/tenants/:tenantID/members/:userID":            {"member", "update"},
	"DELETE /api/v1/tenants/:tenantID/members/:userID":           {"member", "remove"},
	"GET /api/v1/tenants/:tenantID/members/:userID/roles":        {"role", "read"},
	"PUT /api/v1/tenants/:tenantID/members/:userID/roles":        {"role", "assign"},
	"POST /api/v1/tenants/:tenantID/invitations":                 {"member", "invite"},
	"GET /api/v1/tenants/:tenantID/invitations":                  {"member", "read"},
	"DELETE /api/v1/tenants/:tenantID/invitations/:invitationID": {"member", "invite"},
	"GET /api/v1/tenants/:tenantID/roles":                        {"role", "read"},
	"POST /api/v1/tenants/:tenantID/roles":                       {"role", "create"},
	"GET /api/v1/tenants/:tenantID/roles/:roleID":                {"role", "read"},
	"PATCH /api/v1/tenants/:tenantID/roles/:roleID":              {"role", "update"},
	"DELETE /api/v1/tenants/:tenantID/roles/:roleID":             {"role", "delete"},
	"PUT /api/v1/tenants/:tenantID/roles/:roleID/permissions":    {"permission", "assign"},
	"GET /api/v1/tenants/:tenantID/audit-logs":                   {"audit", "read"},
}

// TestTenantRoutesDeclarePermissions 校验所有租户级路由都显式声明了所需权限。
//
// 漏挂 RequirePermission 等价于退回"仅成员校验即可访问"，是本里程碑最大的越权面，
// 因此用测试把路由表与权限声明锁死。
func TestTenantRoutesDeclarePermissions(t *testing.T) {
	stack := newTenancyStack(t)

	registered := 0
	for _, route := range stack.router.Routes() {
		if !isTenantScopedRoute(route.Path) {
			continue
		}
		registered++
		key := route.Method + " " + route.Path
		_, ok := expectedTenantPermissions[key]
		assert.True(t, ok, "租户级路由 %s 未声明所需权限，请补 RequirePermission 并登记到 expectedTenantPermissions", key)
	}

	require.Equal(t, len(expectedTenantPermissions), registered,
		"路由表与权限声明数量不一致：存在已声明但未注册的路由")
}

// isTenantScopedRoute 判断路径是否属于租户级资源。
func isTenantScopedRoute(path string) bool {
	const prefix = "/api/v1/tenants/:tenantID/"
	return len(path) > len(prefix) && path[:len(prefix)] == prefix ||
		path == "/api/v1/tenants/:tenantID"
}

// TestTenantWriteRoutesDenyViewer 逐条验证只读角色无法访问写操作路由。
//
// 与声明表互补：声明表防止"忘记登记"，本用例防止"登记了但没真正生效"。
func TestTenantWriteRoutesDenyViewer(t *testing.T) {
	f := newRBACFixture(t)

	// 造一个只有 viewer 角色的成员。
	viewer := f.inviteAndAccept(t, "viewer-1", "viewer@example.com")
	require.NotEmpty(t, viewer)

	rec := f.setMemberRoles(f.ownerToken, viewer, []string{"viewer"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	viewerToken := f.stack.userToken("viewer-1", "viewer@example.com")

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/tenants/" + f.tenantID + "/invitations"},
		{http.MethodPost, "/api/v1/tenants/" + f.tenantID + "/roles"},
		{http.MethodPatch, "/api/v1/tenants/" + f.tenantID},
		{http.MethodDelete, "/api/v1/tenants/" + f.tenantID + "/members/" + f.ownerUserID},
	}

	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			rec := f.stack.do(c.method, c.path, viewerToken, map[string]any{})
			require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), middleware.CodeForbidden)
		})
	}
}

// TestTenantRoutesRequireAuthentication 校验租户级路由拒绝未认证请求。
func TestTenantRoutesRequireAuthentication(t *testing.T) {
	f := newRBACFixture(t)

	rec := f.stack.do(http.MethodGet, "/api/v1/tenants/"+f.tenantID+"/roles", "", nil)

	require.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
}

// TestPlatformRoutesAreDeclared 校验平台路由全部经过 RequirePlatformAdmin。
//
// 平台路径与租户路径权限体系不同：前者只看 ZITADEL 平台管理员角色，
// 因此任何漏挂都会直接变成"任意用户可停用租户"。
func TestPlatformRoutesAreDeclared(t *testing.T) {
	expected := map[string]bool{
		"POST /api/v1/platform/tenants":                   true,
		"PATCH /api/v1/platform/tenants/:tenantID/status": true,
	}

	stack := newTenancyStack(t)
	registered := 0
	for _, route := range stack.router.Routes() {
		if len(route.Path) < len("/api/v1/platform") || route.Path[:len("/api/v1/platform")] != "/api/v1/platform" {
			continue
		}
		registered++
		assert.True(t, expected[route.Method+" "+route.Path],
			"平台路由 %s %s 未登记到 expectedTenantRoutes", route.Method, route.Path)
	}
	require.Equal(t, len(expected), registered, "平台路由表与登记不一致")
}

// TestPlatformRoutesRejectNonAdmin 校验普通身份无法调用平台接口。
func TestPlatformRoutesRejectNonAdmin(t *testing.T) {
	stack := newTenancyStack(t)
	user := stack.userToken("normal-user", "user@example.com")

	rec := stack.do(http.MethodPost, "/api/v1/platform/tenants", user, map[string]string{
		"name": "Acme", "slug": "acme", "owner_email": "owner@example.com",
	})
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())

	rec = stack.do(http.MethodPatch, "/api/v1/platform/tenants/11111111-1111-1111-1111-111111111111/status",
		user, map[string]string{"status": "suspended"})
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
}
