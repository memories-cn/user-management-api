package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/iam-service/internal/transport/http/responses"
)

// newTestRouter 构建一个仅装配 RequestID 中间件的测试路由，
// 其回显端点会把响应体中的 request_id 一并返回，用于断言全链路一致。
func newTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestID())
	r.GET("/echo", func(c *gin.Context) {
		responses.OK(c, map[string]any{"ok": true})
	})
	return r
}

func doGet(t *testing.T, router *gin.Engine, header string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/echo", nil)
	if header != "" {
		req.Header.Set(responses.HeaderRequestID, header)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// TestRequestID_Generated 校验未提供请求头时生成合法的请求 ID 并写入响应头。
func TestRequestID_Generated(t *testing.T) {
	rec := doGet(t, newTestRouter(), "")

	require.Equal(t, http.StatusOK, rec.Code)
	id := rec.Header().Get(responses.HeaderRequestID)
	assert.NotEmpty(t, id)
	assert.Len(t, strings.Split(id, "-"), 5, "期望使用 UUID 格式")
}

// TestRequestID_PassThrough 校验客户端提供的合法请求 ID 被原样透传。
func TestRequestID_PassThrough(t *testing.T) {
	rec := doGet(t, newTestRouter(), "abc-123")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "abc-123", rec.Header().Get(responses.HeaderRequestID))
}

// TestRequestID_RejectsMalicious 校验包含换行等注入字符的请求 ID 被丢弃并重新生成。
func TestRequestID_RejectsMalicious(t *testing.T) {
	malicious := "abc\nX-Injected: 1"

	rec := doGet(t, newTestRouter(), malicious)

	require.Equal(t, http.StatusOK, rec.Code)
	id := rec.Header().Get(responses.HeaderRequestID)
	assert.NotEqual(t, malicious, id)
	assert.False(t, strings.ContainsAny(id, "\r\n"), "请求 ID 不得包含换行，避免日志注入")
}

// TestRequestID_RejectsTooLong 校验超长请求 ID 被丢弃并重新生成。
func TestRequestID_RejectsTooLong(t *testing.T) {
	tooLong := strings.Repeat("a", 129)

	rec := doGet(t, newTestRouter(), tooLong)

	require.Equal(t, http.StatusOK, rec.Code)
	id := rec.Header().Get(responses.HeaderRequestID)
	assert.NotEqual(t, tooLong, id)
	assert.LessOrEqual(t, len(id), 128)
}

// TestRequestID_ResponseBodyMatchesHeader 校验响应体中的 request_id 与响应头完全一致。
func TestRequestID_ResponseBodyMatchesHeader(t *testing.T) {
	rec := doGet(t, newTestRouter(), "trace-me")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"data":{"ok":true},"request_id":"trace-me"}`, rec.Body.String())
}

// TestRequestID_RejectsEmptyAfterTrim 校验空白字符构成的请求 ID 也被视为非法。
func TestRequestID_RejectsEmptyAfterTrim(t *testing.T) {
	rec := doGet(t, newTestRouter(), "   ")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotEmpty(t, rec.Header().Get(responses.HeaderRequestID))
}
