package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/iam-service/internal/domain/identity"
	"github.com/example/iam-service/internal/transport/http/middleware"
	"github.com/example/iam-service/internal/transport/http/responses"
)

// TestMe_WithoutPrincipal 校验没有可信身份时返回 401（防御性：端点必须挂在认证之后）。
func TestMe_WithoutPrincipal(t *testing.T) {
	rec := serveMe(t, nil)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "UNAUTHENTICATED")
}

// TestMe_WithPrincipal 校验返回统一格式且只暴露必要字段。
func TestMe_WithPrincipal(t *testing.T) {
	rec := serveMe(t, &identity.Principal{
		Subject:       "zitadel-001",
		UserID:        "11111111-2222-3333-4444-555555555555",
		Authenticated: true,
	})

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"data":{"user_id":"11111111-2222-3333-4444-555555555555",`+
		`"zitadel_user_id":"zitadel-001"},"request_id":"req-me-1"}`, rec.Body.String())
}

// serveMe 在请求上下文中注入身份后调用 /me 端点。
func serveMe(t *testing.T, principal *identity.Principal) *httptest.ResponseRecorder {
	t.Helper()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.RequestID())
	r.GET("/me", func(c *gin.Context) {
		if principal != nil {
			c.Request = c.Request.WithContext(identity.WithPrincipal(c.Request.Context(), *principal))
		}
		NewMeHandler().Me(c)
	})

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set(responses.HeaderRequestID, "req-me-1")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// 确保 context 包被使用（与生产代码保持一致的写法）。
var _ = context.Background
