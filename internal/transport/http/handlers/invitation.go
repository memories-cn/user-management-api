package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	appinvitation "github.com/example/iam-service/internal/application/invitation"
	"github.com/example/iam-service/internal/domain/identity"
	invitationdomain "github.com/example/iam-service/internal/domain/invitation"
	"github.com/example/iam-service/internal/transport/http/middleware"
	"github.com/example/iam-service/internal/transport/http/responses"
)

// InvitationHandler 提供邀请相关接口。
type InvitationHandler struct {
	invitations *appinvitation.Service
}

// NewInvitationHandler 构造邀请处理器。
func NewInvitationHandler(invitations *appinvitation.Service) *InvitationHandler {
	return &InvitationHandler{invitations: invitations}
}

// CreateInvitation 为指定邮箱创建邀请（幂等）。
func (h *InvitationHandler) CreateInvitation(c *gin.Context) {
	tenantID, ok := middleware.TenantIDFrom(c.Request.Context())
	if !ok {
		responses.Fail(c, http.StatusNotFound, CodeNotFound, "resource not found")
		return
	}
	principal, ok := identity.FromContext(c.Request.Context())
	if !ok {
		responses.Fail(c, http.StatusUnauthorized, "UNAUTHENTICATED", "missing or invalid credentials")
		return
	}

	var req struct {
		Email string `json:"email" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		responses.Fail(c, http.StatusBadRequest, CodeInvalidArgument, "invalid request body")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		responses.Fail(c, http.StatusNotFound, CodeNotFound, "resource not found")
		return
	}

	result, err := h.invitations.Invite(c.Request.Context(), actorID(principal), tenantUUID, req.Email)
	if err != nil {
		writeError(c, err)
		return
	}

	responses.OK(c, map[string]any{
		"invitation": toInvitationResponse(result.Invitation),
		"token":      result.Token,
		"reused":     result.Reused,
	})
}

// ListInvitations 返回租户的邀请列表。
func (h *InvitationHandler) ListInvitations(c *gin.Context) {
	tenantID, ok := middleware.TenantIDFrom(c.Request.Context())
	if !ok {
		responses.Fail(c, http.StatusNotFound, CodeNotFound, "resource not found")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		responses.Fail(c, http.StatusNotFound, CodeNotFound, "resource not found")
		return
	}

	list, err := h.invitations.ListByTenant(c.Request.Context(), tenantUUID)
	if err != nil {
		writeError(c, err)
		return
	}

	items := make([]invitationResponse, 0, len(list))
	for _, inv := range list {
		items = append(items, toInvitationResponse(inv))
	}
	responses.OK(c, items)
}

// CancelInvitation 撤销邀请。
func (h *InvitationHandler) CancelInvitation(c *gin.Context) {
	tenantID, invitationID, ok := tenantAndInvitationID(c)
	if !ok {
		return
	}

	if err := h.invitations.Cancel(c.Request.Context(), tenantID, invitationID); err != nil {
		writeError(c, err)
		return
	}

	responses.OK(c, map[string]string{"status": "cancelled"})
}

// ListPendingForMe 返回当前已验证邮箱收到的待接受邀请。
//
// 邮箱未验证时返回空列表：不泄漏任何邀请信息给未通过校验的身份。
func (h *InvitationHandler) ListPendingForMe(c *gin.Context) {
	principal, ok := identity.FromContext(c.Request.Context())
	if !ok {
		responses.Fail(c, http.StatusUnauthorized, "UNAUTHENTICATED", "missing or invalid credentials")
		return
	}
	if !principal.EmailVerified || principal.Email == "" {
		responses.OK(c, []invitationResponse{})
		return
	}

	list, err := h.invitations.ListPendingForEmail(c.Request.Context(), principal.Email)
	if err != nil {
		writeError(c, err)
		return
	}

	items := make([]invitationResponse, 0, len(list))
	for _, inv := range list {
		items = append(items, toInvitationResponse(inv))
	}
	responses.OK(c, items)
}

// AcceptInvitation 接受邀请并绑定当前已认证身份。
func (h *InvitationHandler) AcceptInvitation(c *gin.Context) {
	principal, ok := identity.FromContext(c.Request.Context())
	if !ok {
		responses.Fail(c, http.StatusUnauthorized, "UNAUTHENTICATED", "missing or invalid credentials")
		return
	}

	invitationID, err := uuid.Parse(c.Param("invitationID"))
	if err != nil {
		responses.Fail(c, http.StatusNotFound, CodeNotFound, "resource not found")
		return
	}

	var req struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		responses.Fail(c, http.StatusBadRequest, CodeInvalidArgument, "invalid request body")
		return
	}

	m, err := h.invitations.Accept(c.Request.Context(), principal, invitationID, req.Token)
	if err != nil {
		writeError(c, err)
		return
	}

	responses.OK(c, toMemberResponse(m))
}

// tenantAndInvitationID 取出租户与邀请标识。
func tenantAndInvitationID(c *gin.Context) (uuid.UUID, uuid.UUID, bool) {
	tenantIDStr, ok := middleware.TenantIDFrom(c.Request.Context())
	if !ok {
		responses.Fail(c, http.StatusNotFound, CodeNotFound, "resource not found")
		return uuid.Nil, uuid.Nil, false
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		responses.Fail(c, http.StatusNotFound, CodeNotFound, "resource not found")
		return uuid.Nil, uuid.Nil, false
	}
	invitationID, err := uuid.Parse(c.Param("invitationID"))
	if err != nil {
		responses.Fail(c, http.StatusNotFound, CodeNotFound, "resource not found")
		return uuid.Nil, uuid.Nil, false
	}
	return tenantID, invitationID, true
}

// toInvitationResponse 转换邀请响应。
func toInvitationResponse(inv *invitationdomain.Invitation) invitationResponse {
	return invitationResponse{
		ID:        inv.ID.String(),
		Email:     inv.Email,
		Status:    string(inv.Status),
		ExpiresAt: inv.ExpiresAt.Format(dateLayout),
	}
}
