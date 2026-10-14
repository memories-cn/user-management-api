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

// TestPlatformAdminCanCreateTenant 校验平台管理员可以创建租户，且不会自动成为成员。
func TestPlatformAdminCanCreateTenant(t *testing.T) {
	stack := newTenancyStack(t)

	rec, created := stack.createTenant("admin-1", "Acme", "acme", "owner@example.com")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotEmpty(t, created.Data.Tenant.ID)
	assert.Equal(t, "acme", created.Data.Tenant.Slug)
	assert.Equal(t, "pending", created.Data.Invitation.Status)
	assert.Equal(t, "owner@example.com", created.Data.Invitation.Email)
	assert.NotEmpty(t, created.Data.Token, "创建响应应返回明文令牌以便投递")

	// 平台管理员不成为成员：其租户列表应为空。
	listRec := stack.do(http.MethodGet, "/api/v1/tenants", stack.adminToken("admin-1"), nil)
	require.Equal(t, http.StatusOK, listRec.Code)
	assert.JSONEq(t, `{"data":[],"request_id":""}`, withEmptyRequestID(listRec))
}

// TestNormalUserCannotCreateTenant 校验未持平台管理员角色的用户被拒绝。
func TestNormalUserCannotCreateTenant(t *testing.T) {
	stack := newTenancyStack(t)

	rec := stack.do(http.MethodPost, "/api/v1/platform/tenants", stack.userToken("user-1", "user@example.com"),
		map[string]string{"name": "Acme", "slug": "acme", "owner_email": "owner@example.com"})

	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "FORBIDDEN")
}

// TestUnauthenticatedCannotCreateTenant 校验无凭证访问被拒绝。
func TestUnauthenticatedCannotCreateTenant(t *testing.T) {
	stack := newTenancyStack(t)

	rec := stack.do(http.MethodPost, "/api/v1/platform/tenants", "",
		map[string]string{"name": "Acme", "slug": "acme", "owner_email": "owner@example.com"})

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestCreateTenantSlugConflict 校验 slug 冲突返回 409。
func TestCreateTenantSlugConflict(t *testing.T) {
	stack := newTenancyStack(t)

	_, _ = stack.createTenant("admin-1", "Acme", "acme", "owner@example.com")
	rec, _ := stack.createTenant("admin-1", "Acme 2", "ACME", "owner2@example.com")

	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "TENANT_SLUG_CONFLICT")
}

// TestTenantIsolation 校验跨租户访问被拒绝，且成员只能看到自己的租户。
func TestTenantIsolation(t *testing.T) {
	stack := newTenancyStack(t)

	// 租户 A 的 owner 接受邀请成为成员。
	_, tenantA := stack.createTenant("admin-1", "Acme", "acme", "owner-a@example.com")
	require.Equal(t, http.StatusOK,
		stack.acceptInvitation(tenantA.Data.Invitation.ID, "owner-a", "owner-a@example.com", true, tenantA.Data.Token).Code)

	// 租户 B 由另一位 owner 持有。
	_, tenantB := stack.createTenant("admin-1", "Globex", "globex", "owner-b@example.com")
	require.Equal(t, http.StatusOK,
		stack.acceptInvitation(tenantB.Data.Invitation.ID, "owner-b", "owner-b@example.com", true, tenantB.Data.Token).Code)

	ownerAToken := stack.userToken("owner-a", "owner-a@example.com")

	// 访问自己的租户成员列表应成功。
	own := stack.do(http.MethodGet, "/api/v1/tenants/"+tenantA.Data.Tenant.ID+"/members", ownerAToken, nil)
	require.Equal(t, http.StatusOK, own.Code, own.Body.String())

	// 访问他人租户必须被拒绝，且返回 404 以隐藏资源存在性。
	other := stack.do(http.MethodGet, "/api/v1/tenants/"+tenantB.Data.Tenant.ID+"/members", ownerAToken, nil)
	require.Equal(t, http.StatusNotFound, other.Code, other.Body.String())
	assert.Contains(t, other.Body.String(), "NOT_FOUND")

	// 租户列表只返回自己所属的租户。
	list := stack.do(http.MethodGet, "/api/v1/tenants", ownerAToken, nil)
	require.Equal(t, http.StatusOK, list.Code)
	assert.Contains(t, list.Body.String(), tenantA.Data.Tenant.ID)
	assert.NotContains(t, list.Body.String(), tenantB.Data.Tenant.ID)
}

// TestSuspendedTenantBlocksAccess 校验租户停用后成员无法继续访问。
func TestSuspendedTenantBlocksAccess(t *testing.T) {
	stack := newTenancyStack(t)

	_, created := stack.createTenant("admin-1", "Acme", "acme", "owner@example.com")
	require.Equal(t, http.StatusOK,
		stack.acceptInvitation(created.Data.Invitation.ID, "owner-a", "owner@example.com", true, created.Data.Token).Code)

	stack.suspendTenant(created.Data.Tenant.ID)

	rec := stack.do(http.MethodGet, "/api/v1/tenants/"+created.Data.Tenant.ID+"/members",
		stack.userToken("owner-a", "owner@example.com"), nil)

	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "TENANT_SUSPENDED")
}

// TestRemovedMemberLosesAccess 校验成员被移除后不得继续访问。
func TestRemovedMemberLosesAccess(t *testing.T) {
	stack := newTenancyStack(t)

	_, created := stack.createTenant("admin-1", "Acme", "acme", "owner@example.com")
	tenantID := created.Data.Tenant.ID
	require.Equal(t, http.StatusOK,
		stack.acceptInvitation(created.Data.Invitation.ID, "owner-a", "owner@example.com", true, created.Data.Token).Code)

	ownerToken := stack.userToken("owner-a", "owner@example.com")

	// 先记录 owner 的用户 ID，便于随后区分第二位成员。
	ownerMembers := stack.do(http.MethodGet, "/api/v1/tenants/"+tenantID+"/members", ownerToken, nil)
	require.Equal(t, http.StatusOK, ownerMembers.Code)
	ownerUserID := findUserIDExcept(ownerMembers, "")
	require.NotEmpty(t, ownerUserID)

	// 邀请并接受第二位成员，以便移除其中一位。
	inviteRec := stack.do(http.MethodPost, "/api/v1/tenants/"+tenantID+"/invitations", ownerToken,
		map[string]string{"email": "member@example.com"})
	require.Equal(t, http.StatusOK, inviteRec.Code, inviteRec.Body.String())
	invitationID, token := parseInvitation(inviteRec)
	require.Equal(t, http.StatusOK,
		stack.acceptInvitation(invitationID, "member-b", "member@example.com", true, token).Code)

	// 取与 owner 不同的那个成员作为移除目标。
	membersRec := stack.do(http.MethodGet, "/api/v1/tenants/"+tenantID+"/members", ownerToken, nil)
	require.Equal(t, http.StatusOK, membersRec.Code)
	targetUserID := findUserIDExcept(membersRec, ownerUserID)
	require.NotEmpty(t, targetUserID)

	removeRec := stack.do(http.MethodDelete, "/api/v1/tenants/"+tenantID+"/members/"+targetUserID, ownerToken, nil)
	require.Equal(t, http.StatusOK, removeRec.Code)

	// 被移除成员访问时必须被拒绝。
	rec := stack.do(http.MethodGet, "/api/v1/tenants/"+tenantID+"/members",
		stack.userToken("member-b", "member@example.com"), nil)
	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "MEMBERSHIP_INACTIVE")
}

// TestCannotRemoveLastMember 校验不能移除最后一个有效成员。
func TestCannotRemoveLastMember(t *testing.T) {
	stack := newTenancyStack(t)

	_, created := stack.createTenant("admin-1", "Acme", "acme", "owner@example.com")
	tenantID := created.Data.Tenant.ID
	require.Equal(t, http.StatusOK,
		stack.acceptInvitation(created.Data.Invitation.ID, "owner-a", "owner@example.com", true, created.Data.Token).Code)

	ownerToken := stack.userToken("owner-a", "owner@example.com")
	membersRec := stack.do(http.MethodGet, "/api/v1/tenants/"+tenantID+"/members", ownerToken, nil)
	require.Equal(t, http.StatusOK, membersRec.Code)
	onlyUserID := findUserIDExcept(membersRec, "")
	require.NotEmpty(t, onlyUserID)

	rec := stack.do(http.MethodDelete, "/api/v1/tenants/"+tenantID+"/members/"+onlyUserID, ownerToken, nil)

	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "CANNOT_REMOVE_LAST_MEMBER")
}

// TestMemberStatusUpdate 校验成员状态可在 active 与 suspended 之间切换。
func TestMemberStatusUpdate(t *testing.T) {
	stack := newTenancyStack(t)

	_, created := stack.createTenant("admin-1", "Acme", "acme", "owner@example.com")
	tenantID := created.Data.Tenant.ID
	require.Equal(t, http.StatusOK,
		stack.acceptInvitation(created.Data.Invitation.ID, "owner-a", "owner@example.com", true, created.Data.Token).Code)

	ownerToken := stack.userToken("owner-a", "owner@example.com")
	membersRec := stack.do(http.MethodGet, "/api/v1/tenants/"+tenantID+"/members", ownerToken, nil)
	selfUserID := findUserIDExcept(membersRec, "")
	require.NotEmpty(t, selfUserID)

	rec := stack.do(http.MethodPatch, "/api/v1/tenants/"+tenantID+"/members/"+selfUserID, ownerToken,
		map[string]string{"status": "suspended"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "suspended")
}

// TestMembershipUniqueConstraint 校验成员关系唯一约束由数据库保证。
func TestMembershipUniqueConstraint(t *testing.T) {
	stack := newTenancyStack(t)

	_, created := stack.createTenant("admin-1", "Acme", "acme", "owner@example.com")
	tenantID := created.Data.Tenant.ID
	require.Equal(t, http.StatusOK,
		stack.acceptInvitation(created.Data.Invitation.ID, "owner-a", "owner@example.com", true, created.Data.Token).Code)

	membersRec := stack.do(http.MethodGet, "/api/v1/tenants/"+tenantID+"/members",
		stack.userToken("owner-a", "owner@example.com"), nil)
	require.Equal(t, http.StatusOK, membersRec.Code)
	userID := findUserIDExcept(membersRec, "")
	require.NotEmpty(t, userID)

	// 绕过应用层直接插入重复成员关系，验证数据库约束。
	err := stack.db.WithContext(context.Background()).
		Exec("INSERT INTO memberships (id, tenant_id, user_id, status) VALUES (gen_random_uuid(), ?, ?, 'active')",
			tenantID, userID).Error

	require.Error(t, err, "同一租户下同一用户不得存在重复成员关系")
}

// withEmptyRequestID 去掉响应体中的 request_id，便于做纯数据断言。
func withEmptyRequestID(rec *httptest.ResponseRecorder) string {
	var parsed struct {
		Data      any    `json:"data"`
		RequestID string `json:"request_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &parsed)
	out, _ := json.Marshal(map[string]any{"data": parsed.Data, "request_id": ""})
	return string(out)
}

// findUserIDExcept 从成员列表中取一个用户 ID（可排除指定 ID）。
func findUserIDExcept(rec *httptest.ResponseRecorder, except string) string {
	var parsed struct {
		Data []struct {
			UserID string `json:"user_id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &parsed)
	for _, m := range parsed.Data {
		if m.UserID != "" && m.UserID != except {
			return m.UserID
		}
	}
	return ""
}

// parseInvitation 从创建邀请响应中取邀请 ID 与明文令牌。
func parseInvitation(rec *httptest.ResponseRecorder) (string, string) {
	var parsed struct {
		Data struct {
			Invitation struct {
				ID string `json:"id"`
			} `json:"invitation"`
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &parsed)
	return parsed.Data.Invitation.ID, parsed.Data.Token
}
