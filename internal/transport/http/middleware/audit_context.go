package middleware

import (
	"github.com/gin-gonic/gin"

	"github.com/example/iam-service/internal/domain/audit"
	"github.com/example/iam-service/internal/transport/http/responses"
)

// maxUserAgentLength 是 User-Agent 的截断长度。
//
// 请求头由客户端完全控制，不截断会让超长值撑爆字段，也会污染审计表。
const maxUserAgentLength = 512

// AuditContext 把请求上下文（request_id、来源地址、客户端标识）写入 context，
// 供用例层写入审计记录时读取。
//
// 必须在 AuthN 之后挂载：request_id 由 RequestID 中间件生成，
// 而用例需要的是同一个 request_id 才能把审计与日志关联起来。
//
// 注意：Gin 默认信任所有代理，ClientIP() 会采信 X-Forwarded-For，
// 因此该地址可被伪造。它只作为审计的辅助线索，不作为任何授权依据；
// 生产环境必须调用 gin.Engine.SetTrustedProxies 配置可信代理。
func AuditContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		rc := audit.RequestContext{
			RequestID: c.GetString(responses.RequestIDContextKey),
			IPAddress: c.ClientIP(),
			UserAgent: truncate(c.GetHeader("User-Agent"), maxUserAgentLength),
		}
		c.Request = c.Request.WithContext(audit.WithRequestContext(c.Request.Context(), rc))
		c.Next()
	}
}

// truncate 截断字符串到指定长度。
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
