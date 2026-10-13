package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/iam-service/internal/transport/http/middleware"
	"github.com/example/iam-service/internal/transport/http/responses"
)

// stubProbe 是可控的就绪探针，用于覆盖成功与失败两条路径。
type stubProbe struct {
	err error
}

func (s stubProbe) Check(context.Context) error { return s.err }

// newHealthRouter 构建装配 RequestID 与健康检查端点的测试路由。
func newHealthRouter(probe ReadinessProbe) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.RequestID())
	h := NewHealthHandler(probe)
	r.GET("/health/live", h.Live)
	r.GET("/health/ready", h.Ready)
	return r
}

func get(t *testing.T, router *gin.Engine, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set(responses.HeaderRequestID, "req-fixed-1")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// TestHealth_Live 校验存活检查恒返回 200，且不依赖任何外部组件。
func TestHealth_Live(t *testing.T) {
	rec := get(t, newHealthRouter(stubProbe{err: errors.New("boom")}), "/health/live")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"data":{"status":"ok"},"request_id":"req-fixed-1"}`, rec.Body.String())
}

// TestHealth_LiveIgnoresProbe 校验存活探针不受就绪状态影响，避免容器被误杀。
func TestHealth_LiveIgnoresProbe(t *testing.T) {
	rec := get(t, newHealthRouter(stubProbe{}), "/health/live")

	require.Equal(t, http.StatusOK, rec.Code)
}

// TestHealth_Ready_OK 校验依赖就绪时返回 200。
func TestHealth_Ready_OK(t *testing.T) {
	rec := get(t, newHealthRouter(stubProbe{}), "/health/ready")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"data":{"status":"ok"},"request_id":"req-fixed-1"}`, rec.Body.String())
}

// TestHealth_Ready_NotReady 校验依赖不可用时返回 503，且 request_id 与响应头一致。
func TestHealth_Ready_NotReady(t *testing.T) {
	rec := get(t, newHealthRouter(stubProbe{err: errors.New("database unreachable")}), "/health/ready")

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, "req-fixed-1", rec.Header().Get(responses.HeaderRequestID))
	assert.JSONEq(t,
		`{"error":{"code":"NOT_READY","message":"service not ready"},"request_id":"req-fixed-1"}`,
		rec.Body.String())
}

// TestHealth_Ready_DoesNotLeakInternals 校验失败响应不泄漏内部错误细节。
func TestHealth_Ready_DoesNotLeakInternals(t *testing.T) {
	rec := get(t, newHealthRouter(stubProbe{err: errors.New("password=secret")}), "/health/ready")

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.NotContains(t, rec.Body.String(), "secret")
}
