// Package responses 统一 HTTP 成功与失败响应格式。
//
// 约定（见 docs/TDD.md 9.6）：
//   - 成功：{"data": ..., "request_id": "..."}
//   - 失败：{"error": {"code": "...", "message": "..."}, "request_id": "..."}
//
// request_id 的上下文键定义在本包，避免 middleware 与 responses 相互 import 造成循环依赖。
package responses

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const (
	// RequestIDContextKey 是 request_id 在 Gin 上下文中的键名。
	RequestIDContextKey = "request_id"
	// HeaderRequestID 是请求 ID 的响应头名称。
	HeaderRequestID = "X-Request-ID"
)

// Envelope 是成功响应的统一外层结构。
type Envelope struct {
	Data      any    `json:"data,omitempty"`
	RequestID string `json:"request_id"`
}

// ErrorBody 描述错误码与面向客户端的错误信息。
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ErrorEnvelope 是失败响应的统一外层结构。
type ErrorEnvelope struct {
	Error     ErrorBody `json:"error"`
	RequestID string    `json:"request_id"`
}

// OK 以 200 状态码返回统一格式的成功响应。
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Envelope{Data: data, RequestID: requestID(c)})
}

// Fail 以指定状态码返回统一格式的失败响应，并保证响应头携带同一 request_id。
func Fail(c *gin.Context, status int, code, message string) {
	id := requestID(c)
	if id != "" {
		c.Writer.Header().Set(HeaderRequestID, id)
	}
	c.JSON(status, ErrorEnvelope{
		Error:     ErrorBody{Code: code, Message: message},
		RequestID: id,
	})
}

// requestID 依次从 Gin 上下文与响应头中取请求 ID，取不到时返回空串。
func requestID(c *gin.Context) string {
	if id := c.GetString(RequestIDContextKey); id != "" {
		return id
	}
	return c.Writer.Header().Get(HeaderRequestID)
}
