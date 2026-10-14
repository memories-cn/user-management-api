package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	approle "github.com/example/iam-service/internal/application/role"
	"github.com/example/iam-service/internal/domain/role"
	"github.com/example/iam-service/internal/transport/http/middleware"
	"github.com/example/iam-service/internal/transport/http/responses"
)

// roleResponse 是角色响应体。
type roleResponse struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsSystem    bool   `json:"is_system"`
}

// RoleHandler 提供租户角色与角色权限管理接口。
type RoleHandler struct {
	roles *approle.Service
}

// NewRoleHandler 构造角色处理器。
func NewRoleHandler(roles *approle.Service) *RoleHandler {
	return &RoleHandler{roles: roles}
}

// ListRoles 返回租户角色列表。
func (h *RoleHandler) ListRoles(c *gin.Context) {
	tenantID, ok := middleware.TenantIDFrom(c.Request.Context())
	if !ok {
		responses.Fail(c, http.StatusNotFound, CodeNotFound, "resource not found")
		return
	}

	roles, err := h.roles.ListRoles(c.Request.Context(), tenantID)
	if err != nil {
		writeError(c, err)
		return
	}

	responses.OK(c, toRoleResponses(roles))
}

// GetRole 返回单个角色。
func (h *RoleHandler) GetRole(c *gin.Context) {
	tenantID, roleID, ok := tenantAndRoleID(c)
	if !ok {
		return
	}

	rl, err := h.roles.GetRole(c.Request.Context(), tenantID, roleID)
	if err != nil {
		writeError(c, err)
		return
	}

	responses.OK(c, toRoleResponse(rl))
}

// CreateRole 创建租户自定义角色。
func (h *RoleHandler) CreateRole(c *gin.Context) {
	tenantID, ok := middleware.TenantIDFrom(c.Request.Context())
	if !ok {
		responses.Fail(c, http.StatusNotFound, CodeNotFound, "resource not found")
		return
	}

	var req struct {
		Key         string `json:"key" binding:"required"`
		Name        string `json:"name" binding:"required"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		responses.Fail(c, http.StatusBadRequest, CodeInvalidArgument, "invalid request body")
		return
	}

	rl, err := h.roles.CreateRole(c.Request.Context(), tenantID, req.Key, req.Name, req.Description)
	if err != nil {
		writeError(c, err)
		return
	}

	responses.OK(c, toRoleResponse(rl))
}

// UpdateRole 修改角色展示信息。
func (h *RoleHandler) UpdateRole(c *gin.Context) {
	tenantID, roleID, ok := tenantAndRoleID(c)
	if !ok {
		return
	}

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		responses.Fail(c, http.StatusBadRequest, CodeInvalidArgument, "invalid request body")
		return
	}

	rl, err := h.roles.UpdateRole(c.Request.Context(), tenantID, roleID, req.Name, req.Description)
	if err != nil {
		writeError(c, err)
		return
	}

	responses.OK(c, toRoleResponse(rl))
}

// DeleteRole 删除租户自定义角色。
func (h *RoleHandler) DeleteRole(c *gin.Context) {
	tenantID, roleID, ok := tenantAndRoleID(c)
	if !ok {
		return
	}

	if err := h.roles.DeleteRole(c.Request.Context(), tenantID, roleID); err != nil {
		writeError(c, err)
		return
	}

	responses.OK(c, map[string]string{"status": "deleted"})
}

// SetRolePermissions 用给定权限代码整体替换角色的权限集合。
func (h *RoleHandler) SetRolePermissions(c *gin.Context) {
	tenantID, roleID, ok := tenantAndRoleID(c)
	if !ok {
		return
	}

	var req struct {
		Permissions []string `json:"permissions" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		responses.Fail(c, http.StatusBadRequest, CodeInvalidArgument, "invalid request body")
		return
	}

	perms, err := h.roles.SetRolePermissions(c.Request.Context(), tenantID, roleID, req.Permissions)
	if err != nil {
		writeError(c, err)
		return
	}

	responses.OK(c, toPermissionResponses(perms))
}

// ListRolePermissions 返回角色当前的权限集合。
func (h *RoleHandler) ListRolePermissions(c *gin.Context) {
	tenantID, roleID, ok := tenantAndRoleID(c)
	if !ok {
		return
	}

	perms, err := h.roles.ListRolePermissions(c.Request.Context(), tenantID, roleID)
	if err != nil {
		writeError(c, err)
		return
	}

	responses.OK(c, toPermissionResponses(perms))
}

// tenantAndRoleID 从上下文与路径参数中取出租户与角色标识。
func tenantAndRoleID(c *gin.Context) (string, string, bool) {
	tenantID, ok := middleware.TenantIDFrom(c.Request.Context())
	if !ok {
		responses.Fail(c, http.StatusNotFound, CodeNotFound, "resource not found")
		return "", "", false
	}

	roleID := c.Param("roleID")
	if _, err := uuid.Parse(roleID); err != nil {
		responses.Fail(c, http.StatusNotFound, CodeNotFound, "resource not found")
		return "", "", false
	}
	return tenantID, roleID, true
}

// toRoleResponses 批量转换角色响应。
func toRoleResponses(roles []*role.Role) []roleResponse {
	items := make([]roleResponse, 0, len(roles))
	for _, rl := range roles {
		items = append(items, toRoleResponse(rl))
	}
	return items
}

// toRoleResponse 转换单个角色响应。
func toRoleResponse(rl *role.Role) roleResponse {
	return roleResponse{
		ID:          rl.ID.String(),
		Key:         rl.Key,
		Name:        rl.Name,
		Description: rl.Description,
		IsSystem:    rl.IsSystem,
	}
}
