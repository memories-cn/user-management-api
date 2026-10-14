package handlers

import (
	"github.com/gin-gonic/gin"

	approle "github.com/example/iam-service/internal/application/role"
	"github.com/example/iam-service/internal/domain/permission"
	"github.com/example/iam-service/internal/transport/http/responses"
)

// permissionResponse 是权限目录项响应体。
type permissionResponse struct {
	Code        string `json:"code"`
	Resource    string `json:"resource"`
	Action      string `json:"action"`
	Description string `json:"description"`
}

// PermissionHandler 提供系统权限目录接口。
//
// 权限集合由系统迁移维护，因此本处理器**只提供只读查询**：
// 租户只能在既有集合内组合自定义角色（docs/TDD.md 3.5、10.2）。
type PermissionHandler struct {
	roles *approle.Service
}

// NewPermissionHandler 构造权限目录处理器。
func NewPermissionHandler(roles *approle.Service) *PermissionHandler {
	return &PermissionHandler{roles: roles}
}

// ListPermissions 返回系统权限目录。
func (h *PermissionHandler) ListPermissions(c *gin.Context) {
	perms, err := h.roles.ListPermissions(c.Request.Context())
	if err != nil {
		writeError(c, err)
		return
	}

	responses.OK(c, toPermissionResponses(perms))
}

// toPermissionResponses 批量转换权限响应。
func toPermissionResponses(perms []*permission.Permission) []permissionResponse {
	items := make([]permissionResponse, 0, len(perms))
	for _, p := range perms {
		items = append(items, permissionResponse{
			Code:        p.Code,
			Resource:    p.Resource,
			Action:      p.Action,
			Description: p.Description,
		})
	}
	return items
}
