package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/example/iam-service/internal/application/tenancy"
	"github.com/example/iam-service/internal/domain/identity"
	"github.com/example/iam-service/internal/ports"
	"github.com/example/iam-service/internal/transport/http/middleware"
	"github.com/example/iam-service/internal/transport/http/responses"
)

// TenantHandler 提供租户查询与改名接口。
type TenantHandler struct {
	tenants ports.TenantRepository
	access  *tenancy.AccessService
}

// NewTenantHandler 构造租户处理器。
func NewTenantHandler(tenants ports.TenantRepository, access *tenancy.AccessService) *TenantHandler {
	return &TenantHandler{tenants: tenants, access: access}
}

// ListMyTenants 返回当前用户所属的租户。
func (h *TenantHandler) ListMyTenants(c *gin.Context) {
	principal, ok := identity.FromContext(c.Request.Context())
	if !ok {
		responses.Fail(c, http.StatusUnauthorized, "UNAUTHENTICATED", "missing or invalid credentials")
		return
	}

	tenants, err := h.tenants.ListByMemberUserID(c.Request.Context(), principal.UserID)
	if err != nil {
		writeError(c, err)
		return
	}

	items := make([]tenantResponse, 0, len(tenants))
	for _, t := range tenants {
		items = append(items, tenantResponse{
			ID:     t.ID.String(),
			Name:   t.Name,
			Slug:   t.Slug,
			Status: string(t.Status),
		})
	}
	responses.OK(c, items)
}

// GetTenant 返回指定租户详情（租户守卫已校验成员资格与状态）。
func (h *TenantHandler) GetTenant(c *gin.Context) {
	tenantID, ok := middleware.TenantIDFrom(c.Request.Context())
	if !ok {
		responses.Fail(c, http.StatusNotFound, CodeNotFound, "resource not found")
		return
	}

	tn, err := h.access.ActiveTenant(c.Request.Context(), tenantID)
	if err != nil {
		writeError(c, err)
		return
	}

	responses.OK(c, tenantResponse{
		ID:     tn.ID.String(),
		Name:   tn.Name,
		Slug:   tn.Slug,
		Status: string(tn.Status),
	})
}

// UpdateTenant 修改租户名称。
func (h *TenantHandler) UpdateTenant(c *gin.Context) {
	tenantID, ok := middleware.TenantIDFrom(c.Request.Context())
	if !ok {
		responses.Fail(c, http.StatusNotFound, CodeNotFound, "resource not found")
		return
	}

	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		responses.Fail(c, http.StatusBadRequest, CodeInvalidArgument, "invalid request body")
		return
	}

	tn, err := h.access.ActiveTenant(c.Request.Context(), tenantID)
	if err != nil {
		writeError(c, err)
		return
	}

	if !tn.Rename(req.Name) {
		// 名称未变化时不产生写操作，直接返回当前租户。
		responses.OK(c, tenantResponse{ID: tn.ID.String(), Name: tn.Name, Slug: tn.Slug, Status: string(tn.Status)})
		return
	}

	if err := h.tenants.Update(c.Request.Context(), tn); err != nil {
		writeError(c, err)
		return
	}

	responses.OK(c, tenantResponse{
		ID:     tn.ID.String(),
		Name:   tn.Name,
		Slug:   tn.Slug,
		Status: string(tn.Status),
	})
}

// parsePaging 解析分页参数，非法值回退到默认值。
func parsePaging(c *gin.Context) (int, int) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "0"))
	if err != nil {
		limit = 0
	}
	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil {
		offset = 0
	}
	return limit, offset
}
