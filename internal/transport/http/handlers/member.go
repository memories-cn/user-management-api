package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	appmembership "github.com/example/iam-service/internal/application/membership"
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

// MemberHandler 提供成员管理接口。
type MemberHandler struct {
	members *appmembership.Service
}

// NewMemberHandler 构造成员处理器。
func NewMemberHandler(members *appmembership.Service) *MemberHandler {
	return &MemberHandler{members: members}
}

// ListMembers 返回租户成员列表。
func (h *MemberHandler) ListMembers(c *gin.Context) {
	tenantID, ok := middleware.TenantIDFrom(c.Request.Context())
	if !ok {
		responses.Fail(c, http.StatusNotFound, CodeNotFound, "resource not found")
		return
	}

	limit, offset := parsePaging(c)
	members, err := h.members.List(c.Request.Context(), tenantID, limit, offset)
	if err != nil {
		writeError(c, err)
		return
	}

	responses.OK(c, toMemberResponses(members))
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

	var req struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		responses.Fail(c, http.StatusBadRequest, CodeInvalidArgument, "invalid request body")
		return
	}

	m, err := h.members.UpdateStatus(c.Request.Context(), tenantID, userID, membershipdomain.Status(req.Status))
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

	if err := h.members.Remove(c.Request.Context(), tenantID, userID); err != nil {
		writeError(c, err)
		return
	}

	responses.OK(c, map[string]string{"status": "removed"})
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
