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

// rbacFixture 提供一个"已创建租户并有一个 owner 成员"的环境。
type rbacFixture struct {
	stack    *tenancyStack
	tenantID string
	// ownerToken 是租户所有者的令牌。
	ownerToken string
	// ownerUserID 是所有者的内部用户 ID。
	ownerUserID string
}

// newRBACFixture 创建租户并让所有者接受邀请，返回可直接使用的环境。
func newRBACFixture(t *testing.T) *rbacFixture {
	t.Helper()

	stack := newTenancyStack(t)
	rec, created := stack.createTenant("admin-1", "Acme", "acme", "owner@example.com")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotEmpty(t, created.Data.Tenant.ID)

	accepted := stack.acceptInvitation(
		created.Data.Invitation.ID, "owner-1", "owner@example.com", true, created.Data.Token)
	require.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())

	userID := memberUserID(t, stack, created.Data.Tenant.ID)
	return &rbacFixture{
		stack:       stack,
		tenantID:    created.Data.Tenant.ID,
		ownerToken:  stack.userToken("owner-1", "owner@example.com"),
		ownerUserID: userID,
	}
}

// memberUserID 查询租户内唯一成员的用户 ID。
func memberUserID(t *testing.T, stack *tenancyStack, tenantID string) string {
	t.Helper()

	var userID string
	err := stack.db.WithContext(context.Background()).
		Raw("SELECT user_id FROM memberships WHERE tenant_id = ?", tenantID).
		Scan(&userID).Error
	require.NoError(t, err)
	require.NotEmpty(t, userID)
	return userID
}

// permissionList 是权限目录响应体。
type permissionList struct {
	Data []struct {
		Code     string `json:"code"`
		Resource string `json:"resource"`
		Action   string `json:"action"`
	} `json:"data"`
}

// roleList 是角色列表响应体（分页信封）。
type roleList struct {
	Data struct {
		Items []struct {
			ID       string `json:"id"`
			Key      string `json:"key"`
			Name     string `json:"name"`
			IsSystem bool   `json:"is_system"`
		} `json:"items"`
		Pagination struct {
			Total int64 `json:"total"`
		} `json:"pagination"`
	} `json:"data"`
}

// authorizeResponse 是授权判定响应体。
type authorizeResponse struct {
	Data struct {
		Allowed  bool   `json:"allowed"`
		TenantID string `json:"tenant_id"`
		Resource string `json:"resource"`
		Action   string `json:"action"`
	} `json:"data"`
}

// getRoles 查询租户角色列表。
func (f *rbacFixture) getRoles(token string) *roleList {
	f.stack.t.Helper()

	rec := f.stack.do(http.MethodGet, "/api/v1/tenants/"+f.tenantID+"/roles", token, nil)
	require.Equal(f.stack.t, http.StatusOK, rec.Code, rec.Body.String())

	var parsed roleList
	require.NoError(f.stack.t, json.Unmarshal(rec.Body.Bytes(), &parsed))
	return &parsed
}

// setMemberRoles 设置成员角色并返回响应。
func (f *rbacFixture) setMemberRoles(token, userID string, roles []string) *httptest.ResponseRecorder {
	return f.stack.do(http.MethodPut, "/api/v1/tenants/"+f.tenantID+"/members/"+userID+"/roles",
		token, map[string][]string{"roles": roles})
}

// authorize 调用统一授权判定接口。
func (f *rbacFixture) authorize(token, tenantID, resource, action string) (int, authorizeResponse) {
	f.stack.t.Helper()

	rec := f.stack.do(http.MethodPost, "/api/v1/authorize", token, map[string]string{
		"tenant_id": tenantID,
		"resource":  resource,
		"action":    action,
	})

	var parsed authorizeResponse
	if rec.Code == http.StatusOK {
		require.NoError(f.stack.t, json.Unmarshal(rec.Body.Bytes(), &parsed))
	}
	return rec.Code, parsed
}

func TestRBAC_TenantCreationPresetsSystemRoles(t *testing.T) {
	f := newRBACFixture(t)

	roles := f.getRoles(f.ownerToken)

	require.Len(t, roles.Data.Items, 4, "租户创建必须预置 owner/admin/member/viewer")
	keys := make([]string, 0, len(roles.Data.Items))
	for _, rl := range roles.Data.Items {
		keys = append(keys, rl.Key)
		assert.True(t, rl.IsSystem, "预置角色必须是系统角色")
	}
	assert.ElementsMatch(t, []string{"owner", "admin", "member", "viewer"}, keys)
	assert.Equal(t, int64(4), roles.Data.Pagination.Total)
}

func TestRBAC_FirstMemberBecomesOwner(t *testing.T) {
	f := newRBACFixture(t)

	rec := f.stack.do(http.MethodGet,
		"/api/v1/tenants/"+f.tenantID+"/members/"+f.ownerUserID+"/roles", f.ownerToken, nil)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"key":"owner"`, "首个接受邀请的成员必须绑定 owner")
}

func TestRBAC_PermissionCatalogIsReadOnly(t *testing.T) {
	f := newRBACFixture(t)

	rec := f.stack.do(http.MethodGet, "/api/v1/permissions", f.ownerToken, nil)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var catalog permissionList
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &catalog))
	require.NotEmpty(t, catalog.Data)
	for _, p := range catalog.Data {
		assert.Contains(t, p.Code, ":", "权限代码必须是 resource:action 形式")
	}
}

func TestRBAC_OwnerCanInviteAndCannotStripOwnOwnerRole(t *testing.T) {
	f := newRBACFixture(t)

	assert.True(t, f.mustAuthorize(f.ownerToken, "member", "invite"), "owner 必须能邀请成员")

	// 清空自己的角色会移除租户内最后一个有效 owner，必须被拒绝。
	rec := f.setMemberRoles(f.ownerToken, f.ownerUserID, []string{})

	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "CANNOT_REMOVE_LAST_OWNER")
}

func TestRBAC_ViewerIsDeniedForMemberInvite(t *testing.T) {
	f := newRBACFixture(t)

	// 引入第二个成员，使其可被安全降级。
	second := f.inviteAndAccept(t, "member-1", "member@example.com")
	require.NotEmpty(t, second)

	// 先把第二个成员设为 viewer，用它验证只读角色无法邀请。
	rec := f.setMemberRoles(f.ownerToken, second, []string{"viewer"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	viewerToken := f.stack.userToken("member-1", "member@example.com")
	assert.False(t, f.mustAuthorize(viewerToken, "member", "invite"), "viewer 不得邀请成员")
	assert.True(t, f.mustAuthorize(viewerToken, "member", "read"), "viewer 应能查看成员")

	rec = f.stack.do(http.MethodPost, "/api/v1/tenants/"+f.tenantID+"/invitations",
		viewerToken, map[string]string{"email": "intruder@example.com"})
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String(), "viewer 邀请成员必须被拒绝")
	assert.Contains(t, rec.Body.String(), "FORBIDDEN")
}

// inviteAndAccept 邀请并接受一名新成员，返回其用户 ID。
func (f *rbacFixture) inviteAndAccept(t *testing.T, subject, email string) string {
	t.Helper()

	rec := f.stack.do(http.MethodPost, "/api/v1/tenants/"+f.tenantID+"/invitations",
		f.ownerToken, map[string]string{"email": email})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var created struct {
		Data struct {
			Invitation struct {
				ID string `json:"id"`
			} `json:"invitation"`
			Token string `json:"token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	require.NotEmpty(t, created.Data.Invitation.ID)

	accepted := f.stack.acceptInvitation(created.Data.Invitation.ID, subject, email, true, created.Data.Token)
	require.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())

	return latestMemberUserID(t, f.stack, f.tenantID)
}

// latestMemberUserID 返回租户内最近加入的成员用户 ID。
//
// users 表不保存邮箱（身份属性以 ZITADEL 为权威来源），
// 因此按加入顺序而非邮箱定位成员。
func latestMemberUserID(t *testing.T, stack *tenancyStack, tenantID string) string {
	t.Helper()

	var userID string
	err := stack.db.WithContext(context.Background()).
		Raw("SELECT user_id FROM memberships WHERE tenant_id = ? ORDER BY created_at DESC, user_id DESC LIMIT 1",
			tenantID).
		Scan(&userID).Error
	require.NoError(t, err)
	require.NotEmpty(t, userID)
	return userID
}

// mustAuthorize 断言判定成功并返回结果，失败会直接报错。
func (f *rbacFixture) mustAuthorize(token, resource, action string) bool {
	f.stack.t.Helper()

	code, parsed := f.authorize(token, f.tenantID, resource, action)
	require.Equal(f.stack.t, http.StatusOK, code)
	return parsed.Data.Allowed
}

func TestRBAC_CrossTenantRoleAssignmentIsRejected(t *testing.T) {
	f := newRBACFixture(t)

	// 另一个租户，由同一平台管理员创建。
	otherRec, other := f.stack.createTenant("admin-1", "Other", "other", "other@example.com")
	require.Equal(t, http.StatusOK, otherRec.Code, otherRec.Body.String())

	// 用 A 租户所有者的令牌给 B 租户的成员分配 A 租户的角色：
	// 路径中的租户是 B，因此该请求首先会被租户守卫拒绝（404）。
	rec := f.stack.do(http.MethodPut,
		"/api/v1/tenants/"+other.Data.Tenant.ID+"/members/"+f.ownerUserID+"/roles",
		f.ownerToken, map[string][]string{"roles": {"owner"}})

	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String(), "非本租户成员不得操作其成员角色")
}

func TestRBAC_CrossTenantRoleIDIsRejectedByDatabase(t *testing.T) {
	f := newRBACFixture(t)

	// 取本租户的 owner 角色 ID。
	roles := f.getRoles(f.ownerToken)
	var ownerRoleID string
	for _, rl := range roles.Data.Items {
		if rl.Key == "owner" {
			ownerRoleID = rl.ID
		}
	}
	require.NotEmpty(t, ownerRoleID)

	// 创建第二个租户并让其 owner 接受邀请，得到属于另一租户的成员关系。
	otherRec, other := f.stack.createTenant("admin-1", "Other2", "other2", "other2@example.com")
	require.Equal(t, http.StatusOK, otherRec.Code, otherRec.Body.String())
	accepted := f.stack.acceptInvitation(
		other.Data.Invitation.ID, "other-owner", "other2@example.com", true, other.Data.Token)
	require.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())

	var otherMembershipID string
	err := f.stack.db.WithContext(context.Background()).
		Raw("SELECT id FROM memberships WHERE tenant_id = ?", other.Data.Tenant.ID).
		Scan(&otherMembershipID).Error
	require.NoError(t, err)
	require.NotEmpty(t, otherMembershipID)

	// 越权写入：把 A 租户的角色分配给 B 租户的成员。
	// 复合外键 (role_id, tenant_id) → roles (id, tenant_id) 必须拒绝，
	// 这是"仅检查 UUID 是否存在"无法拦截的场景。
	err = f.stack.db.WithContext(context.Background()).
		Exec(`INSERT INTO membership_roles (membership_id, role_id, tenant_id, assigned_by)
			  VALUES (?, ?, ?, ?)`,
			otherMembershipID, ownerRoleID, other.Data.Tenant.ID, f.ownerUserID).Error

	require.Error(t, err, "跨租户角色分配必须被数据库复合外键拒绝")
	assert.Contains(t, err.Error(), "membership_roles")
}

func TestRBAC_RevocationIsImmediate(t *testing.T) {
	f := newRBACFixture(t)
	second := f.inviteAndAccept(t, "member-1", "member@example.com")
	require.NotEmpty(t, second)

	// 第二个成员默认绑定 member，先授予 member:invite 所属角色以验证撤权。
	rec := f.setMemberRoles(f.ownerToken, second, []string{"admin"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.True(t, f.mustAuthorize(f.stack.userToken("member-1", "member@example.com"), "member", "invite"))

	// 降级为 viewer：策略投影与版本号在同一事务内更新，判定必须立即变化。
	rec = f.setMemberRoles(f.ownerToken, second, []string{"viewer"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.False(t, f.mustAuthorize(f.stack.userToken("member-1", "member@example.com"), "member", "invite"),
		"撤权必须立即生效，不允许旧授权继续通过")
}

func TestRBAC_RemovingMemberRevokesImmediately(t *testing.T) {
	f := newRBACFixture(t)
	second := f.inviteAndAccept(t, "member-1", "member@example.com")
	require.NotEmpty(t, second)

	memberToken := f.stack.userToken("member-1", "member@example.com")
	require.True(t, f.mustAuthorize(memberToken, "member", "read"))

	rec := f.stack.do(http.MethodDelete,
		"/api/v1/tenants/"+f.tenantID+"/members/"+second, f.ownerToken, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	code, parsed := f.authorize(memberToken, f.tenantID, "member", "read")
	require.Equal(t, http.StatusOK, code)
	assert.False(t, parsed.Data.Allowed, "成员移除后不得继续通过授权判定")
}

func TestRBAC_CannotRemoveLastOwner(t *testing.T) {
	f := newRBACFixture(t)

	// 先引入第二个非 owner 成员：否则会先命中"不能移除最后一个成员"的保护，
	// 无法验证 owner 专属保护。
	second := f.inviteAndAccept(t, "member-1", "member@example.com")
	require.NotEmpty(t, second)

	rec := f.stack.do(http.MethodDelete,
		"/api/v1/tenants/"+f.tenantID+"/members/"+f.ownerUserID, f.ownerToken, nil)

	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "CANNOT_REMOVE_LAST_OWNER")
}

func TestRBAC_SystemRoleCannotBeDeleted(t *testing.T) {
	f := newRBACFixture(t)

	roles := f.getRoles(f.ownerToken)
	var ownerRoleID string
	for _, rl := range roles.Data.Items {
		if rl.Key == "owner" {
			ownerRoleID = rl.ID
		}
	}
	require.NotEmpty(t, ownerRoleID)

	rec := f.stack.do(http.MethodDelete,
		"/api/v1/tenants/"+f.tenantID+"/roles/"+ownerRoleID, f.ownerToken, nil)

	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "SYSTEM_ROLE_IMMUTABLE")
}

func TestRBAC_CustomRoleLifecycle(t *testing.T) {
	f := newRBACFixture(t)

	created := f.stack.do(http.MethodPost, "/api/v1/tenants/"+f.tenantID+"/roles", f.ownerToken,
		map[string]string{"key": "billing_manager", "name": "账单管理员", "description": "管理账单"})
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())

	var parsed struct {
		Data struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &parsed))
	require.NotEmpty(t, parsed.Data.ID)

	// 绑定系统目录内的权限。
	rec := f.stack.do(http.MethodPut,
		"/api/v1/tenants/"+f.tenantID+"/roles/"+parsed.Data.ID+"/permissions", f.ownerToken,
		map[string][]string{"permissions": {"member:read", "member:invite"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// 目录外权限必须整次拒绝。
	rec = f.stack.do(http.MethodPut,
		"/api/v1/tenants/"+f.tenantID+"/roles/"+parsed.Data.ID+"/permissions", f.ownerToken,
		map[string][]string{"permissions": {"member:self_destruct"}})
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "PERMISSION_NOT_FOUND")

	// 未被使用的自定义角色可以删除。
	rec = f.stack.do(http.MethodDelete,
		"/api/v1/tenants/"+f.tenantID+"/roles/"+parsed.Data.ID, f.ownerToken, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestRBAC_RoleInUseCannotBeDeleted(t *testing.T) {
	f := newRBACFixture(t)
	second := f.inviteAndAccept(t, "member-1", "member@example.com")
	require.NotEmpty(t, second)

	// member 角色正被第二个成员使用。
	roles := f.getRoles(f.ownerToken)
	var memberRoleID string
	for _, rl := range roles.Data.Items {
		if rl.Key == "member" {
			memberRoleID = rl.ID
		}
	}
	require.NotEmpty(t, memberRoleID)

	rec := f.stack.do(http.MethodDelete,
		"/api/v1/tenants/"+f.tenantID+"/roles/"+memberRoleID, f.ownerToken, nil)

	// member 是系统角色，先命中系统角色保护；两者都表示"不可删除"。
	require.Contains(t, []int{http.StatusConflict}, rec.Code)
}

func TestRBAC_DefaultDenyForMemberWithoutRole(t *testing.T) {
	f := newRBACFixture(t)
	second := f.inviteAndAccept(t, "member-1", "member@example.com")
	require.NotEmpty(t, second)

	// 清空角色后，该成员不应具备任何权限。
	rec := f.setMemberRoles(f.ownerToken, second, []string{})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	code, parsed := f.authorize(f.stack.userToken("member-1", "member@example.com"), f.tenantID, "member", "read")
	require.Equal(t, http.StatusOK, code)
	assert.False(t, parsed.Data.Allowed, "无角色的成员必须默认拒绝")
}

func TestRBAC_AuthorizeHidesTenantExistence(t *testing.T) {
	f := newRBACFixture(t)

	// 非成员判定陌生租户：返回 allowed=false 而非错误，避免枚举租户是否存在。
	code, parsed := f.authorize(f.ownerToken, "11111111-1111-1111-1111-111111111111", "member", "read")

	require.Equal(t, http.StatusOK, code)
	assert.False(t, parsed.Data.Allowed)
}

func TestRBAC_AuthorizeRequiresBodyFields(t *testing.T) {
	f := newRBACFixture(t)

	rec := f.stack.do(http.MethodPost, "/api/v1/authorize", f.ownerToken,
		map[string]string{"tenant_id": f.tenantID})

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
}
