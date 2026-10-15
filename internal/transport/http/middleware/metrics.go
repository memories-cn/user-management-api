package middleware

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/example/iam-service/internal/ports"
)

// Metrics 采集每个请求的计数与耗时。
//
// 必须挂在 RequestID 与 Logging 之后、业务处理器之前，
// 且要包裹住后续全部中间件（包括权限守卫与限流），
// 否则被守卫拒绝的请求不会出现在指标里——而那恰恰是最需要观测的部分。
func Metrics(recorder ports.MetricsRecorder) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		// c.FullPath() 返回路由模板（/tenants/:tenantID），
		// 未匹配到路由时返回空串，统一归入 "unmatched"。
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}

		recorder.ObserveRequest(c.Request.Method, route, c.Writer.Status(), time.Since(start))
	}
}
