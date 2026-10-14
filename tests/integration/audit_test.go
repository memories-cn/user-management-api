package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// auditListResponse 是审计列表的分页信封。
type auditListResponse struct {
	Data struct {
		Items []struct {
			ID          string         `json:"id"`
			TenantID    string         `json:"tenant_id"`
			ActorUserID string         `json:"actor_user_id"`
			Action      string         `json:"action"`
			ResourceID  string         `json:"resource_id"`
			Result      string         `json:"result"`
			RequestID   string         `json:"request_id"`
			Metadata    map[string]any `json:"metadata"`
		} `json:"items"`
		Pagination struct {
			Limit  int   `json:"limit"`
			Offset int   `json:"offset"`
			Total  int64 `json:"total"`
		} `json:"pagination"`
	} `json:"data"`
}

// listAudits 查询租户审计日志。
func (f *rbacFixture) listAudits(token, query string) (*httptest.ResponseRecorder, auditListResponse) {
	f.stack.t.Helper()

	path := "/api/v1/tenants/" + f.tenantID + "/audit-logs"
	if query != "" {
		path += "?" + query
	}
	rec := f.stack.do(http.MethodGet, path, token, nil)

	var parsed auditListResponse
	if rec.Code == http.StatusOK {
		require.NoError(f.stack.t, json.Unmarshal(rec.Body.Bytes(), &parsed))
	}
	return rec, parsed
}

// TestAudit_MemberRemovalIsRecorded 校验成员移除产生审计留痕。
func TestAudit_MemberRemovalIsRecorded(t *testing.T) {
	f := newRBACFixture(t)
	second := f.inviteAndAccept(t, "member-1", "member@example.com")
	require.NotEmpty(t, second)

	rec := f.stack.do(http.MethodDelete,
		"/api/v1/tenants/"+f.tenantID+"/members/"+second, f.ownerToken, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	_, list := f.listAudits(f.ownerToken, "action=member.remove")
	require.Equal(t, int64(1), list.Data.Pagination.Total, "成员移除必须留痕")
	entry := list.Data.Items[0]
	assert.Equal(t, "member.remove", entry.Action)
	assert.Equal(t, second, entry.ResourceID)
	assert.Equal(t, "success", entry.Result)
	assert.Equal(t, f.tenantID, entry.TenantID)
	assert.Equal(t, f.ownerUserID, entry.ActorUserID)
	assert.NotEmpty(t, entry.RequestID, "审计必须能关联到具体请求")
	assert.Equal(t, "removed", entry.Metadata["to_status"])
}

// TestAudit_RoleAssignmentIsRecorded 校验角色分配留痕。
func TestAudit_RoleAssignmentIsRecorded(t *testing.T) {
	f := newRBACFixture(t)
	second := f.inviteAndAccept(t, "member-1", "member@example.com")
	require.NotEmpty(t, second)

	rec := f.setMemberRoles(f.ownerToken, second, []string{"viewer"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	_, list := f.listAudits(f.ownerToken, "action=role.assign")
	require.Equal(t, int64(1), list.Data.Pagination.Total)
	assert.Equal(t, second, list.Data.Items[0].ResourceID)
}

// TestAudit_RolePermissionChangeIsRecorded 校验角色权限变更留痕。
func TestAudit_RolePermissionChangeIsRecorded(t *testing.T) {
	f := newRBACFixture(t)

	roles := f.getRoles(f.ownerToken)
	var ownerRoleID string
	for _, rl := range roles.Data.Items {
		if rl.Key == "owner" {
			ownerRoleID = rl.ID
		}
	}
	require.NotEmpty(t, ownerRoleID)

	rec := f.stack.do(http.MethodPut,
		"/api/v1/tenants/"+f.tenantID+"/roles/"+ownerRoleID+"/permissions", f.ownerToken,
		// 保留 audit:read，否则收窄权限后审计查询本身会被拒绝。
		map[string][]string{"permissions": {"member:read", "audit:read"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	_, list := f.listAudits(f.ownerToken, "action=role.permission_update")
	require.Equal(t, int64(1), list.Data.Pagination.Total)
	assert.Equal(t, float64(2), list.Data.Items[0].Metadata["permission_count"])
}

// TestAudit_InvitationIsRecorded 校验发起邀请留痕。
func TestAudit_InvitationIsRecorded(t *testing.T) {
	f := newRBACFixture(t)

	rec := f.stack.do(http.MethodPost, "/api/v1/tenants/"+f.tenantID+"/invitations",
		f.ownerToken, map[string]string{"email": "newcomer@example.com"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// 注意：创建租户时为 owner 生成的邀请同样留痕，因此这里有两条。
	_, list := f.listAudits(f.ownerToken, "action=member.invite")
	assert.Equal(t, int64(2), list.Data.Pagination.Total)
}

// TestAudit_TenantLifecycleIsRecorded 校验租户停用与恢复都留痕。
func TestAudit_TenantLifecycleIsRecorded(t *testing.T) {
	f := newRBACFixture(t)

	suspend := f.stack.do(http.MethodPatch,
		"/api/v1/platform/tenants/"+f.tenantID+"/status", f.stack.adminToken("admin-1"),
		map[string]string{"status": "suspended"})
	require.Equal(t, http.StatusOK, suspend.Code, suspend.Body.String())

	activate := f.stack.do(http.MethodPatch,
		"/api/v1/platform/tenants/"+f.tenantID+"/status", f.stack.adminToken("admin-1"),
		map[string]string{"status": "active"})
	require.Equal(t, http.StatusOK, activate.Code, activate.Body.String())

	var suspendCount, activateCount int64
	var suspendMeta, activateMeta map[string]any
	_, list := f.listAudits(f.ownerToken, "action=tenant.suspend")
	suspendCount = list.Data.Pagination.Total
	if len(list.Data.Items) > 0 {
		suspendMeta = list.Data.Items[0].Metadata
	}
	_, list = f.listAudits(f.ownerToken, "action=tenant.activate")
	activateCount = list.Data.Pagination.Total
	if len(list.Data.Items) > 0 {
		activateMeta = list.Data.Items[0].Metadata
	}

	assert.Equal(t, int64(1), suspendCount, "停用是高风险操作，必须留痕")
	assert.Equal(t, int64(1), activateCount, "恢复同样是高风险操作")
	assert.Equal(t, "active", suspendMeta["from_status"])
	assert.Equal(t, "suspended", suspendMeta["to_status"])
	assert.Equal(t, "suspended", activateMeta["from_status"])
}

// TestAudit_PlatformOperationHasNoTenantID 校验平台级操作不挂租户。
func TestAudit_PlatformOperationHasNoTenantID(t *testing.T) {
	f := newRBACFixture(t)

	var count int64
	err := f.stack.db.WithContext(context.Background()).
		Raw("SELECT COUNT(*) FROM audit_logs WHERE action = 'tenant.create' AND tenant_id IS NULL").
		Scan(&count).Error
	require.NoError(t, err)

	assert.Equal(t, int64(1), count,
		"平台级创建租户必须留痕，且 tenant_id 为空——空租户不得成为隐式提权通道")
}

// TestAudit_IsTenantScoped 校验审计查询强制限定在当前租户。
func TestAudit_IsTenantScoped(t *testing.T) {
	f := newRBACFixture(t)

	// 另一个租户，构造同名角色的变更以便验证不可见。
	otherRec, other := f.stack.createTenant("admin-1", "Other", "other", "other@example.com")
	require.Equal(t, http.StatusOK, otherRec.Code, otherRec.Body.String())
	accepted := f.stack.acceptInvitation(
		other.Data.Invitation.ID, "other-owner", "other@example.com", true, other.Data.Token)
	require.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())

	// 在另一个租户产生一条审计。
	otherOwner := f.stack.userToken("other-owner", "other@example.com")
	rec := f.stack.do(http.MethodPost, "/api/v1/tenants/"+other.Data.Tenant.ID+"/invitations",
		otherOwner, map[string]string{"email": "someone@example.com"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// 本租户查询不应看到另一个租户的记录。
	_, mine := f.listAudits(f.ownerToken, "action=member.invite")
	for _, item := range mine.Data.Items {
		assert.Equal(t, f.tenantID, item.TenantID, "不得返回其他租户的审计记录")
	}
}

// TestAudit_RequiresIndependentPermission 校验审计访问受独立权限保护。
func TestAudit_RequiresIndependentPermission(t *testing.T) {
	f := newRBACFixture(t)
	viewer := f.inviteAndAccept(t, "viewer-1", "viewer@example.com")
	require.NotEmpty(t, viewer)

	rec := f.setMemberRoles(f.ownerToken, viewer, []string{"viewer"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	viewerToken := f.stack.userToken("viewer-1", "viewer@example.com")
	rec = f.stack.do(http.MethodGet, "/api/v1/tenants/"+f.tenantID+"/audit-logs", viewerToken, nil)

	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "FORBIDDEN")
}

// TestAudit_FiltersAndPaging 校验过滤与分页生效。
func TestAudit_FiltersAndPaging(t *testing.T) {
	f := newRBACFixture(t)
	second := f.inviteAndAccept(t, "member-1", "member@example.com")
	require.NotEmpty(t, second)

	rec := f.setMemberRoles(f.ownerToken, second, []string{"viewer"})
	require.Equal(t, http.StatusOK, rec.Code)
	rec = f.stack.do(http.MethodPost, "/api/v1/tenants/"+f.tenantID+"/invitations",
		f.ownerToken, map[string]string{"email": "third@example.com"})
	require.Equal(t, http.StatusOK, rec.Code)

	// 以未过滤的总数为基准，避免把断言写死在操作顺序上。
	_, all := f.listAudits(f.ownerToken, "")
	total := all.Data.Pagination.Total
	require.Greater(t, total, int64(1))

	// 按操作者过滤：owner 发起两次邀请 + 一次角色分配。
	_, byActor := f.listAudits(f.ownerToken, "actor_user_id="+f.ownerUserID)
	assert.Equal(t, int64(3), byActor.Data.Pagination.Total)

	// 按资源类型过滤：三次邀请（含创建租户时为 owner 生成的那次）。
	_, byResource := f.listAudits(f.ownerToken, "resource_type=invitation")
	assert.Equal(t, int64(3), byResource.Data.Pagination.Total)

	// 分页：limit=1 时只返回一条，但 total 反映总数。
	_, paged := f.listAudits(f.ownerToken, "limit=1")
	assert.Equal(t, 1, paged.Data.Pagination.Limit)
	assert.Len(t, paged.Data.Items, 1)
	assert.Equal(t, total, paged.Data.Pagination.Total)
}

// TestAudit_RejectsInvalidFilters 校验非法过滤参数返回 400。
func TestAudit_RejectsInvalidFilters(t *testing.T) {
	f := newRBACFixture(t)

	rec := f.stack.do(http.MethodGet,
		"/api/v1/tenants/"+f.tenantID+"/audit-logs?action=member.explode", f.ownerToken, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "AUDIT_ACTION_INVALID")

	rec = f.stack.do(http.MethodGet,
		"/api/v1/tenants/"+f.tenantID+"/audit-logs?since=not-a-time", f.ownerToken, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "AUDIT_FILTER_INVALID")
}

// TestAudit_SuspendedTenantBlocksAuditAccess 校验租户停用后审计接口同样被拦截。
func TestAudit_SuspendedTenantBlocksAuditAccess(t *testing.T) {
	f := newRBACFixture(t)

	rec := f.stack.do(http.MethodPatch,
		"/api/v1/platform/tenants/"+f.tenantID+"/status", f.stack.adminToken("admin-1"),
		map[string]string{"status": "suspended"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = f.stack.do(http.MethodGet, "/api/v1/tenants/"+f.tenantID+"/audit-logs", f.ownerToken, nil)
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "TENANT_SUSPENDED")
}

// TestAudit_PlatformStatusRejectsInvalidInput 校验平台状态接口的参数校验。
func TestAudit_PlatformStatusRejectsInvalidInput(t *testing.T) {
	f := newRBACFixture(t)
	admin := f.stack.adminToken("admin-1")

	rec := f.stack.do(http.MethodPatch,
		"/api/v1/platform/tenants/"+f.tenantID+"/status", admin,
		map[string]string{"status": "deleted"})
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "INVALID_STATUS", "不提供删除：deleted 未定义清理策略")

	rec = f.stack.do(http.MethodPatch,
		"/api/v1/platform/tenants/11111111-1111-1111-1111-111111111111/status", admin,
		map[string]string{"status": "suspended"})
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "TENANT_NOT_FOUND")
}

// TestAudit_StatusChangeIsIdempotent 校验重复设置同一状态不重复留痕。
func TestAudit_StatusChangeIsIdempotent(t *testing.T) {
	f := newRBACFixture(t)
	admin := f.stack.adminToken("admin-1")

	first := f.stack.do(http.MethodPatch,
		"/api/v1/platform/tenants/"+f.tenantID+"/status", admin,
		map[string]string{"status": "suspended"})
	require.Equal(t, http.StatusOK, first.Code)

	second := f.stack.do(http.MethodPatch,
		"/api/v1/platform/tenants/"+f.tenantID+"/status", admin,
		map[string]string{"status": "suspended"})
	require.Equal(t, http.StatusOK, second.Code)

	var count int64
	require.NoError(t, f.stack.db.WithContext(context.Background()).
		Raw("SELECT COUNT(*) FROM audit_logs WHERE tenant_id = ? AND action = 'tenant.suspend'", f.tenantID).
		Scan(&count).Error)
	assert.Equal(t, int64(1), count, "状态未变化时不应产生写操作与留痕")
}
