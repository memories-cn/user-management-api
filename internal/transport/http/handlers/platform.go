package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	apptenant "github.com/example/iam-service/internal/application/tenant"
	"github.com/example/iam-service/internal/domain/identity"
	"github.com/example/iam-service/internal/transport/http/responses"
)

// createTenantRequest 是创建租户的请求体。
type createTenantRequest struct {
	Name       string `json:"name" binding:"required"`
	Slug       string `json:"slug" binding:"required"`
	OwnerEmail string `json:"owner_email" binding:"required"`
}

// tenantResponse 是租户响应体。
type tenantResponse struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Slug   string `json:"slug"`
	Status string `json:"status"`
}

// invitationResponse 是邀请响应体。
type invitationResponse struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Status    string `json:"status"`
	ExpiresAt string `json:"expires_at"`
}

// createTenantResponse 是创建租户的响应体。
type createTenantResponse struct {
	Tenant     tenantResponse     `json:"tenant"`
	Invitation invitationResponse `json:"invitation"`
	// Token 是邀请的明文令牌，仅在新建邀请时返回；生产环境应改为邮件投递。
	Token string `json:"token,omitempty"`
}

// PlatformHandler 提供平台管理接口。
type PlatformHandler struct {
	creator *apptenant.CreateService
}

// NewPlatformHandler 构造平台管理处理器。
func NewPlatformHandler(creator *apptenant.CreateService) *PlatformHandler {
	return &PlatformHandler{creator: creator}
}

// CreateTenant 创建租户并为 owner 邮箱创建邀请。
//
// 只有持有 ZITADEL 平台管理员角色的身份可以调用（由 RequirePlatformAdmin 保证）。
func (h *PlatformHandler) CreateTenant(c *gin.Context) {
	principal, ok := identity.FromContext(c.Request.Context())
	if !ok {
		responses.Fail(c, http.StatusUnauthorized, "UNAUTHENTICATED", "missing or invalid credentials")
		return
	}

	var req createTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		responses.Fail(c, http.StatusBadRequest, CodeInvalidArgument, "invalid request body")
		return
	}

	result, err := h.creator.Create(c.Request.Context(), actorID(principal), req.Name, req.Slug, req.OwnerEmail)
	if err != nil {
		writeError(c, err)
		return
	}

	responses.OK(c, createTenantResponse{
		Tenant: tenantResponse{
			ID:     result.Tenant.ID.String(),
			Name:   result.Tenant.Name,
			Slug:   result.Tenant.Slug,
			Status: string(result.Tenant.Status),
		},
		Invitation: invitationResponse{
			ID:        result.Invitation.ID.String(),
			Email:     result.Invitation.Email,
			Status:    string(result.Invitation.Status),
			ExpiresAt: result.Invitation.ExpiresAt.Format(dateLayout),
		},
		Token: result.Token,
	})
}

// dateLayout 是统一的时间输出格式。
const dateLayout = "2006-01-02T15:04:05Z07:00"
