// Package http 负责 Gin 路由装配与传输层组合，不含业务规则。
package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/example/iam-service/internal/bootstrap"
	"github.com/example/iam-service/internal/transport/http/handlers"
	"github.com/example/iam-service/internal/transport/http/middleware"
	"github.com/example/iam-service/internal/transport/http/responses"
)

// NewRouter 装配全局中间件、健康检查端点与受保护的 v1 接口。
//
// 中间件顺序固定为 RequestID → Logging，保证日志与错误响应都能携带 request_id。
// 健康检查端点不挂载认证中间件，避免容器探针被拦截；
// /api/v1 分组统一挂载认证中间件，避免个别路由漏挂造成越权。
func NewRouter(app *bootstrap.App) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery(), middleware.RequestID(), middleware.Logging())

	health := handlers.NewHealthHandler(app)
	r.GET("/health/live", health.Live)
	r.GET("/health/ready", health.Ready)

	v1 := r.Group("/api/v1")
	v1.Use(middleware.AuthN(app.Verifier, app.UserSync))
	me := handlers.NewMeHandler()
	v1.GET("/me", me.Me)

	// 未匹配路由同样返回统一错误格式，避免泄漏 Gin 默认文本响应。
	r.NoRoute(func(c *gin.Context) {
		responses.Fail(c, http.StatusNotFound, "NOT_FOUND", "resource not found")
	})
	return r
}
