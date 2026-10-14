package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInvitationEndToEnd 校验完整的邀请与接受流程（先认证、后绑定）。
func TestInvitationEndToEnd(t *testing.T) {
	stack := newTenancyStack(t)

	_, created := stack.createTenant("admin-1", "Acme", "acme", "Owner@Example.com")
	require.NotEmpty(t, created.Data.Invitation.ID)

	// 受邀人可见"给我的邀请"。
	pending := stack.do(http.MethodGet, "/api/v1/invitations/pending",
		stack.userToken("owner-a", "owner@example.com"), nil)
	require.Equal(t, http.StatusOK, pending.Code, pending.Body.String())
	assert.Contains(t, pending.Body.String(), created.Data.Invitation.ID)

	// 接受邀请后成为 active 成员。
	accept := stack.acceptInvitation(created.Data.Invitation.ID, "owner-a", "owner@example.com", true, created.Data.Token)
	require.Equal(t, http.StatusOK, accept.Code, accept.Body.String())
	assert.Contains(t, accept.Body.String(), `"status":"active"`)

	// 接受后应能看到该租户。
	list := stack.do(http.MethodGet, "/api/v1/tenants", stack.userToken("owner-a", "owner@example.com"), nil)
	require.Equal(t, http.StatusOK, list.Code)
	assert.Contains(t, list.Body.String(), created.Data.Tenant.ID)
}

// TestAcceptRequiresVerifiedEmail 校验邮箱未验证时不得绑定。
func TestAcceptRequiresVerifiedEmail(t *testing.T) {
	stack := newTenancyStack(t)

	_, created := stack.createTenant("admin-1", "Acme", "acme", "owner@example.com")

	rec := stack.acceptInvitation(created.Data.Invitation.ID, "owner-a", "owner@example.com", false, created.Data.Token)

	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "EMAIL_NOT_VERIFIED")
}

// TestAcceptRejectsEmailMismatch 校验身份邮箱与邀请邮箱不一致时拒绝。
func TestAcceptRejectsEmailMismatch(t *testing.T) {
	stack := newTenancyStack(t)

	_, created := stack.createTenant("admin-1", "Acme", "acme", "owner@example.com")

	rec := stack.acceptInvitation(created.Data.Invitation.ID, "attacker", "attacker@example.com", true, created.Data.Token)

	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "INVITATION_EMAIL_MISMATCH")
}

// TestAcceptRejectsWrongToken 校验令牌不匹配时拒绝。
func TestAcceptRejectsWrongToken(t *testing.T) {
	stack := newTenancyStack(t)

	_, created := stack.createTenant("admin-1", "Acme", "acme", "owner@example.com")

	rec := stack.acceptInvitation(created.Data.Invitation.ID, "owner-a", "owner@example.com", true, "wrong-token")

	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "INVALID_INVITATION_TOKEN")
}

// TestAcceptRejectsExpiredInvitation 校验过期邀请被拒绝。
func TestAcceptRejectsExpiredInvitation(t *testing.T) {
	stack := newTenancyStack(t)

	_, created := stack.createTenant("admin-1", "Acme", "acme", "owner@example.com")

	// 直接把有效期改到过去，避免依赖真实时间流逝。
	err := stack.db.WithContext(context.Background()).
		Exec("UPDATE invitations SET expires_at = now() - interval '1 day' WHERE id = ?",
			created.Data.Invitation.ID).Error
	require.NoError(t, err)

	rec := stack.acceptInvitation(created.Data.Invitation.ID, "owner-a", "owner@example.com", true, created.Data.Token)

	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "INVITATION_EXPIRED")
}

// TestCancelInvitationThenAccept 校验撤销后的邀请无法被接受。
//
// 撤销由租户成员发起：平台管理员不是租户成员（职责分离），因此无权调用租户级接口。
func TestCancelInvitationThenAccept(t *testing.T) {
	stack := newTenancyStack(t)

	_, created := stack.createTenant("admin-1", "Acme", "acme", "owner@example.com")
	tenantID := created.Data.Tenant.ID
	require.Equal(t, http.StatusOK,
		stack.acceptInvitation(created.Data.Invitation.ID, "owner-a", "owner@example.com", true, created.Data.Token).Code)
	ownerToken := stack.userToken("owner-a", "owner@example.com")

	// 平台管理员撤销租户邀请应被拒绝（其不是成员）。
	byAdmin := stack.do(http.MethodDelete,
		"/api/v1/tenants/"+tenantID+"/invitations/"+created.Data.Invitation.ID,
		stack.adminToken("admin-1"), nil)
	require.Equal(t, http.StatusNotFound, byAdmin.Code, "平台管理员不得操作租户资源")

	invite := stack.do(http.MethodPost, "/api/v1/tenants/"+tenantID+"/invitations", ownerToken,
		map[string]string{"email": "member@example.com"})
	require.Equal(t, http.StatusOK, invite.Code, invite.Body.String())
	invitationID, token := parseInvitation(invite)

	cancel := stack.do(http.MethodDelete,
		"/api/v1/tenants/"+tenantID+"/invitations/"+invitationID, ownerToken, nil)
	require.Equal(t, http.StatusOK, cancel.Code, cancel.Body.String())

	rec := stack.acceptInvitation(invitationID, "member-b", "member@example.com", true, token)

	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "INVITATION_NOT_PENDING")
}

// TestRepeatedInviteIsIdempotent 校验重复邀请复用同一条邀请。
func TestRepeatedInviteIsIdempotent(t *testing.T) {
	stack := newTenancyStack(t)

	_, created := stack.createTenant("admin-1", "Acme", "acme", "owner@example.com")
	tenantID := created.Data.Tenant.ID
	require.Equal(t, http.StatusOK,
		stack.acceptInvitation(created.Data.Invitation.ID, "owner-a", "owner@example.com", true, created.Data.Token).Code)
	ownerToken := stack.userToken("owner-a", "owner@example.com")

	first := stack.do(http.MethodPost, "/api/v1/tenants/"+tenantID+"/invitations", ownerToken,
		map[string]string{"email": "member@example.com"})
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	firstID, firstToken := parseInvitation(first)
	require.NotEmpty(t, firstToken)

	second := stack.do(http.MethodPost, "/api/v1/tenants/"+tenantID+"/invitations", ownerToken,
		map[string]string{"email": "member@example.com"})
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())
	secondID, secondToken := parseInvitation(second)

	assert.Equal(t, firstID, secondID, "重复邀请必须复用同一条邀请")
	assert.Empty(t, secondToken, "复用时不应重新下发令牌")
	assert.Contains(t, second.Body.String(), `"reused":true`)
}

// TestAcceptIsIdempotent 校验重复接受邀请不会产生重复成员。
func TestAcceptIsIdempotent(t *testing.T) {
	stack := newTenancyStack(t)

	_, created := stack.createTenant("admin-1", "Acme", "acme", "owner@example.com")
	tenantID := created.Data.Tenant.ID

	first := stack.acceptInvitation(created.Data.Invitation.ID, "owner-a", "owner@example.com", true, created.Data.Token)
	require.Equal(t, http.StatusOK, first.Code)
	second := stack.acceptInvitation(created.Data.Invitation.ID, "owner-a", "owner@example.com", true, created.Data.Token)
	require.Equal(t, http.StatusOK, second.Code, "重复接受必须幂等成功")

	members := stack.do(http.MethodGet, "/api/v1/tenants/"+tenantID+"/members",
		stack.userToken("owner-a", "owner@example.com"), nil)
	require.Equal(t, http.StatusOK, members.Code)
	assert.Equal(t, 1, countMembers(members), "不得产生重复成员")
}

// TestInvitationsAreTenantScoped 校验邀请列表与撤销均受租户范围约束。
func TestInvitationsAreTenantScoped(t *testing.T) {
	stack := newTenancyStack(t)

	_, created := stack.createTenant("admin-1", "Acme", "acme", "owner@example.com")
	require.Equal(t, http.StatusOK,
		stack.acceptInvitation(created.Data.Invitation.ID, "owner-a", "owner@example.com", true, created.Data.Token).Code)

	// 另一个租户的成员不得撤销本租户的邀请。
	_, other := stack.createTenant("admin-1", "Globex", "globex", "owner-b@example.com")
	require.Equal(t, http.StatusOK,
		stack.acceptInvitation(other.Data.Invitation.ID, "owner-b", "owner-b@example.com", true, other.Data.Token).Code)

	rec := stack.do(http.MethodDelete,
		"/api/v1/tenants/"+other.Data.Tenant.ID+"/invitations/"+created.Data.Invitation.ID,
		stack.userToken("owner-b", "owner-b@example.com"), nil)

	require.Equal(t, http.StatusNotFound, rec.Code, "不得跨租户操作邀请")
}

// TestInvitationDefaultTTLIsSevenDays 校验默认有效期为 7 天。
func TestInvitationDefaultTTLIsSevenDays(t *testing.T) {
	stack := newTenancyStack(t)

	_, created := stack.createTenant("admin-1", "Acme", "acme", "owner@example.com")

	var expiresAt time.Time
	err := stack.db.WithContext(context.Background()).
		Raw("SELECT expires_at FROM invitations WHERE id = ?", created.Data.Invitation.ID).
		Scan(&expiresAt).Error
	require.NoError(t, err)

	remaining := time.Until(expiresAt)
	assert.Greater(t, remaining, 6*24*time.Hour, "有效期应约为 7 天")
	assert.LessOrEqual(t, remaining, 7*24*time.Hour+time.Minute)
}

// countMembers 统计成员列表中的成员数量。
func countMembers(rec *httptest.ResponseRecorder) int {
	return len(parseMemberIDs(rec))
}

// parseMemberIDs 解析成员列表中的用户 ID。
func parseMemberIDs(rec *httptest.ResponseRecorder) []string {
	var parsed struct {
		Data []struct {
			UserID string `json:"user_id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &parsed)
	ids := make([]string, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		ids = append(ids, m.UserID)
	}
	return ids
}
