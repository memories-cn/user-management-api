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

// NewRouter 装配中间件与健康检查、平台管理、租户、成员、邀请路由。
//
// 中间件顺序固定为 RequestID → Logging，保证日志与错误响应都能携带 request_id。
// 健康检查不挂载认证中间件，避免容器探针被拦截；
// /api/v1 分组统一挂载认证；租户级路由统一挂载租户守卫；
// /api/v1/platform 分组额外要求平台管理员角色，形成独立的显式授权路径。
func NewRouter(app *bootstrap.App) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery(), middleware.RequestID(), middleware.Logging())

	health := handlers.NewHealthHandler(app)
	r.GET("/health/live", health.Live)
	r.GET("/health/ready", health.Ready)

	v1 := r.Group("/api/v1")
	v1.Use(middleware.AuthN(app.Verifier, app.UserSync))
	registerPlatformRoutes(v1, app)
	registerTenantRoutes(v1, app)
	registerInvitationSelfRoutes(v1, app)

	// 未匹配路由同样返回统一错误格式，避免泄漏 Gin 默认文本响应。
	r.NoRoute(func(c *gin.Context) {
		responses.Fail(c, http.StatusNotFound, "NOT_FOUND", "resource not found")
	})
	return r
}

// registerPlatformRoutes 注册平台管理路由（仅平台管理员）。
func registerPlatformRoutes(v1 *gin.RouterGroup, app *bootstrap.App) {
	platform := v1.Group("/platform")
	platform.Use(middleware.RequirePlatformAdmin(app.PlatformAdmin))

	handler := handlers.NewPlatformHandler(app.TenantCreator)
	platform.POST("/tenants", handler.CreateTenant)
}

// registerTenantRoutes 注册租户与成员路由（租户成员可见）。
func registerTenantRoutes(v1 *gin.RouterGroup, app *bootstrap.App) {
	tenants := handlers.NewTenantHandler(app.Tenants, app.Access)
	members := handlers.NewMemberHandler(app.MemberService)
	invitations := handlers.NewInvitationHandler(app.InvitationService)

	v1.GET("/tenants", tenants.ListMyTenants)

	scoped := v1.Group("/tenants/:tenantID")
	scoped.Use(middleware.TenantGuard(app.Access))
	{
		scoped.GET("", tenants.GetTenant)
		scoped.PATCH("", tenants.UpdateTenant)

		scoped.GET("/members", members.ListMembers)
		scoped.GET("/members/:userID", members.GetMember)
		scoped.PATCH("/members/:userID", members.UpdateMemberStatus)
		scoped.DELETE("/members/:userID", members.RemoveMember)

		scoped.POST("/invitations", invitations.CreateInvitation)
		scoped.GET("/invitations", invitations.ListInvitations)
		scoped.DELETE("/invitations/:invitationID", invitations.CancelInvitation)
	}
}

// registerInvitationSelfRoutes 注册与当前用户相关的邀请路由。
func registerInvitationSelfRoutes(v1 *gin.RouterGroup, app *bootstrap.App) {
	invitations := handlers.NewInvitationHandler(app.InvitationService)

	v1.GET("/invitations/pending", invitations.ListPendingForMe)
	v1.POST("/invitations/:invitationID/accept", invitations.AcceptInvitation)
}
