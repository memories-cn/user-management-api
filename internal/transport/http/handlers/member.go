package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	appmembership "github.com/example/iam-service/internal/application/membership"
	approle "github.com/example/iam-service/internal/application/role"
	"github.com/example/iam-service/internal/domain/identity"
	membershipdomain "github.com/example/iam-service/internal/domain/membership"
	"github.com/example/iam-service/internal/transport/http/middleware"
	"github.com/example/iam-service/internal/transport/http/responses"
)

// memberResponse 是成员响应体。
type memberResponse struct {
	UserID   string `json:"user_id"`
	TenantID string `json:"tenant_id"`
	Status   string `json:"status"`
	JoinedAt string `json:"joined_at,omitempty"`
}

// MemberHandler 提供成员管理与成员角色接口。
type MemberHandler struct {
	members *appmembership.Service
	roles   *approle.AssignmentService
}

// NewMemberHandler 构造成员处理器。
func NewMemberHandler(members *appmembership.Service, roles *approle.AssignmentService) *MemberHandler {
	return &MemberHandler{members: members, roles: roles}
}

// ListMembers 返回租户成员列表。
func (h *MemberHandler) ListMembers(c *gin.Context) {
	tenantID, ok := middleware.TenantIDFrom(c.Request.Context())
	if !ok {
		responses.Fail(c, http.StatusNotFound, CodeNotFound, "resource not found")
		return
	}

	limit, offset := parsePaging(c)
	result, err := h.members.List(c.Request.Context(), tenantID, c.Query("status"), limit, offset)
	if err != nil {
		writeError(c, err)
		return
	}

	responses.OKList(c, toMemberResponses(result.Members), result.Limit, result.Offset, result.Total)
}

// GetMember 返回指定成员。
func (h *MemberHandler) GetMember(c *gin.Context) {
	tenantID, userID, ok := tenantAndUserID(c)
	if !ok {
		return
	}

	m, err := h.members.Get(c.Request.Context(), tenantID, userID)
	if err != nil {
		writeError(c, err)
		return
	}

	responses.OK(c, toMemberResponse(m))
}

// UpdateMemberStatus 变更成员状态，仅允许 active 与 suspended。
func (h *MemberHandler) UpdateMemberStatus(c *gin.Context) {
	tenantID, userID, ok := tenantAndUserID(c)
	if !ok {
		return
	}

	principal, ok := identity.FromContext(c.Request.Context())
	if !ok || !principal.Authenticated {
		responses.Fail(c, http.StatusUnauthorized, responses.CodeUnauthenticated, "missing or invalid credentials")
		return
	}

	var req struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		responses.Fail(c, http.StatusBadRequest, CodeInvalidArgument, "invalid request body")
		return
	}

	m, err := h.members.UpdateStatus(c.Request.Context(), tenantID, userID,
		membershipdomain.Status(req.Status), actorID(principal))
	if err != nil {
		writeError(c, err)
		return
	}

	responses.OK(c, toMemberResponse(m))
}

// RemoveMember 移除成员（软删除）。
func (h *MemberHandler) RemoveMember(c *gin.Context) {
	tenantID, userID, ok := tenantAndUserID(c)
	if !ok {
		return
	}

	principal, ok := identity.FromContext(c.Request.Context())
	if !ok || !principal.Authenticated {
		responses.Fail(c, http.StatusUnauthorized, responses.CodeUnauthenticated, "missing or invalid credentials")
		return
	}

	if err := h.members.Remove(c.Request.Context(), tenantID, userID, actorID(principal)); err != nil {
		writeError(c, err)
		return
	}

	responses.OK(c, map[string]string{"status": "removed"})
}

// GetMemberRoles 返回成员在租户内被授予的角色。
func (h *MemberHandler) GetMemberRoles(c *gin.Context) {
	tenantID, userID, ok := tenantAndUserID(c)
	if !ok {
		return
	}

	roles, err := h.roles.ListMemberRoles(c.Request.Context(), tenantID, userID)
	if err != nil {
		writeError(c, err)
		return
	}

	responses.OK(c, toRoleResponses(roles))
}

// SetMemberRoles 用给定角色标识整体替换成员的角色。
//
// 操作者取自已认证身份，不接受请求体传入的分配者（docs/TDD.md 10.3）。
func (h *MemberHandler) SetMemberRoles(c *gin.Context) {
	tenantID, userID, ok := tenantAndUserID(c)
	if !ok {
		return
	}

	principal, ok := identity.FromContext(c.Request.Context())
	if !ok || !principal.Authenticated {
		responses.Fail(c, http.StatusUnauthorized, responses.CodeUnauthenticated, "missing or invalid credentials")
		return
	}

	// roles 必须出现，但允许为空数组——清空角色是一次合法的撤权操作。
	// 因此这里不依赖 binding:"required"（它会把空数组也判为缺失）。
	var req struct {
		Roles []string `json:"roles"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Roles == nil {
		responses.Fail(c, http.StatusBadRequest, CodeInvalidArgument, "invalid request body")
		return
	}

	roles, err := h.roles.SetMemberRoles(c.Request.Context(), tenantID, userID, req.Roles, actorID(principal))
	if err != nil {
		writeError(c, err)
		return
	}

	responses.OK(c, toRoleResponses(roles))
}

// tenantAndUserID 从上下文与路径参数中取出租户与用户标识。
func tenantAndUserID(c *gin.Context) (string, string, bool) {
	tenantID, ok := middleware.TenantIDFrom(c.Request.Context())
	if !ok {
		responses.Fail(c, http.StatusNotFound, CodeNotFound, "resource not found")
		return "", "", false
	}

	userID := c.Param("userID")
	if _, err := uuid.Parse(userID); err != nil {
		responses.Fail(c, http.StatusNotFound, CodeNotFound, "resource not found")
		return "", "", false
	}
	return tenantID, userID, true
}

// toMemberResponses 批量转换成员响应。
func toMemberResponses(members []*membershipdomain.Membership) []memberResponse {
	items := make([]memberResponse, 0, len(members))
	for _, m := range members {
		items = append(items, toMemberResponse(m))
	}
	return items
}

// toMemberResponse 转换单个成员响应。
func toMemberResponse(m *membershipdomain.Membership) memberResponse {
	resp := memberResponse{
		UserID:   m.UserID.String(),
		TenantID: m.TenantID.String(),
		Status:   string(m.Status),
	}
	if m.JoinedAt != nil {
		resp.JoinedAt = m.JoinedAt.Format(dateLayout)
	}
	return resp
}
