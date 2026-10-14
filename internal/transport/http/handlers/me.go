package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/example/iam-service/internal/domain/identity"
	"github.com/example/iam-service/internal/transport/http/responses"
)

// MeResponse 是当前用户端点的最小响应。
type MeResponse struct {
	// UserID 是内部业务用户 UUID。
	UserID string `json:"user_id"`
	// ZitadelUserID 是外部身份标识，即 Token 中的 sub。
	ZitadelUserID string `json:"zitadel_user_id"`
}

// MeHandler 提供当前用户端点。
type MeHandler struct{}

// NewMeHandler 构造当前用户处理器。
func NewMeHandler() *MeHandler {
	return &MeHandler{}
}

// Me 返回当前认证用户的基本信息。
//
// 端点必须挂在认证中间件之后；此处再做一次身份校验，防止路由装配失误造成越权。
func (h *MeHandler) Me(c *gin.Context) {
	principal, ok := identity.FromContext(c.Request.Context())
	if !ok {
		responses.Fail(c, http.StatusUnauthorized, responses.CodeUnauthenticated, "missing or invalid credentials")
		return
	}

	responses.OK(c, MeResponse{
		UserID:        principal.UserID,
		ZitadelUserID: principal.Subject,
	})
}
