package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/example/iam-service/internal/application/authorization"
	"github.com/example/iam-service/internal/domain/identity"
	"github.com/example/iam-service/internal/transport/http/middleware"
	"github.com/example/iam-service/internal/transport/http/responses"
)

// authorizeRequest 是授权判定请求体。
//
// 只允许表达"要判断什么"，不允许声明调用者身份：
// 身份唯一来源是已验证令牌构造的 Principal（docs/TDD.md 9.5）。
type authorizeRequest struct {
	TenantID string `json:"tenant_id" binding:"required"`
	Resource string `json:"resource" binding:"required"`
	Action   string `json:"action" binding:"required"`
}

// authorizeResponse 是授权判定响应体。
type authorizeResponse struct {
	Allowed  bool   `json:"allowed"`
	TenantID string `json:"tenant_id"`
	Resource string `json:"resource"`
	Action   string `json:"action"`
}

// AuthorizeHandler 提供统一授权判定接口，供业务微服务调用。
type AuthorizeHandler struct {
	authorization *authorization.Service
}

// NewAuthorizeHandler 构造授权判定处理器。
func NewAuthorizeHandler(svc *authorization.Service) *AuthorizeHandler {
	return &AuthorizeHandler{authorization: svc}
}

// Authorize 判定当前身份能否在指定租户下对资源执行操作。
//
// 响应语义：业务性拒绝返回 200 + allowed=false（避免枚举租户是否存在）；
// 依赖不可用返回 503，调用方必须失败关闭而不是当作无权限放行。
func (h *AuthorizeHandler) Authorize(c *gin.Context) {
	principal, ok := identity.FromContext(c.Request.Context())
	if !ok || !principal.Authenticated {
		responses.Fail(c, http.StatusUnauthorized, middleware.CodeUnauthenticated, "missing or invalid credentials")
		return
	}

	var req authorizeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		responses.Fail(c, http.StatusBadRequest, CodeInvalidArgument, "invalid request body")
		return
	}
	if _, err := uuid.Parse(req.TenantID); err != nil {
		responses.Fail(c, http.StatusBadRequest, CodeInvalidArgument, "invalid tenant_id")
		return
	}

	decision, err := h.authorization.Authorize(c.Request.Context(), principal, req.TenantID, req.Resource, req.Action)
	if err != nil {
		writeError(c, err)
		return
	}

	responses.OK(c, authorizeResponse{
		Allowed:  decision.Allowed,
		TenantID: decision.TenantID,
		Resource: decision.Resource,
		Action:   decision.Action,
	})
}
