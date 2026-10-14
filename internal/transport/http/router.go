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

// NewRouter 装配中间件与健康检查、平台管理、租户、成员、邀请、角色路由。
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
	// AuditContext 必须在 AuthN 之后：审计需要的 request_id 由 RequestID 中间件生成，
	// 用例层据此把审计记录与请求日志关联起来。
	v1.Use(middleware.AuthN(app.Verifier, app.UserSync), middleware.AuditContext())
	registerPlatformRoutes(v1, app)
	registerTenantRoutes(v1, app)
	registerInvitationSelfRoutes(v1, app)
	registerCatalogRoutes(v1, app)

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

	handler := handlers.NewPlatformHandler(app.TenantCreator, app.TenantStatus)
	platform.POST("/tenants", handler.CreateTenant)
	// 停用与恢复都是高风险操作，必须留痕（审计由用例在同事务内写入）。
	platform.PATCH("/tenants/:tenantID/status", handler.SetTenantStatus)
}

// registerCatalogRoutes 注册当前用户与系统级只读目录路由。
func registerCatalogRoutes(v1 *gin.RouterGroup, app *bootstrap.App) {
	permissions := handlers.NewPermissionHandler(app.RoleService)
	authorize := handlers.NewAuthorizeHandler(app.Authorization)

	v1.GET("/me", handlers.NewMeHandler().Me)
	v1.GET("/permissions", permissions.ListPermissions)
	// 统一授权判定：身份只来自已验证令牌，请求体只能声明要判断什么。
	v1.POST("/authorize", authorize.Authorize)
}

// registerTenantRoutes 注册租户、成员与角色路由。
//
// **权限必须逐条显式挂载**：若对整个分组挂一个宽松权限，viewer 之类的只读角色
// 会拿到写权限。每条路由都挂 TenantGuard + RequirePermission 两道中间件，
// 前者校验成员资格与租户状态，后者校验细粒度权限。
func registerTenantRoutes(v1 *gin.RouterGroup, app *bootstrap.App) {
	tenants := handlers.NewTenantHandler(app.Tenants, app.Access)
	members := handlers.NewMemberHandler(app.MemberService, app.AssignmentService)
	invitations := handlers.NewInvitationHandler(app.InvitationService)
	roles := handlers.NewRoleHandler(app.RoleService)
	audits := handlers.NewAuditHandler(app.AuditService)

	v1.GET("/tenants", tenants.ListMyTenants)

	scoped := v1.Group("/tenants/:tenantID")
	scoped.Use(middleware.TenantGuard(app.Access))
	{
		scoped.GET("", guard(app, "tenant", "read"), tenants.GetTenant)
		scoped.PATCH("", guard(app, "tenant", "update"), tenants.UpdateTenant)

		scoped.GET("/members", guard(app, "member", "read"), members.ListMembers)
		scoped.GET("/members/:userID", guard(app, "member", "read"), members.GetMember)
		scoped.PATCH("/members/:userID", guard(app, "member", "update"), members.UpdateMemberStatus)
		scoped.DELETE("/members/:userID", guard(app, "member", "remove"), members.RemoveMember)

		scoped.GET("/members/:userID/roles", guard(app, "role", "read"), members.GetMemberRoles)
		scoped.PUT("/members/:userID/roles", guard(app, "role", "assign"), members.SetMemberRoles)

		scoped.POST("/invitations", guard(app, "member", "invite"), invitations.CreateInvitation)
		scoped.GET("/invitations", guard(app, "member", "read"), invitations.ListInvitations)
		scoped.DELETE("/invitations/:invitationID", guard(app, "member", "invite"), invitations.CancelInvitation)

		scoped.GET("/roles", guard(app, "role", "read"), roles.ListRoles)
		scoped.POST("/roles", guard(app, "role", "create"), roles.CreateRole)
		scoped.GET("/roles/:roleID", guard(app, "role", "read"), roles.GetRole)
		scoped.PATCH("/roles/:roleID", guard(app, "role", "update"), roles.UpdateRole)
		scoped.DELETE("/roles/:roleID", guard(app, "role", "delete"), roles.DeleteRole)
		scoped.PUT("/roles/:roleID/permissions", guard(app, "permission", "assign"), roles.SetRolePermissions)

		// 审计日志受独立权限保护：审计覆盖整个租户的操作留痕，
		// 不应因为"能管理成员"就自动获得读取能力（docs/TDD.md 10.4）。
		scoped.GET("/audit-logs", guard(app, "audit", "read"), audits.ListAuditLogs)
	}
}

// registerInvitationSelfRoutes 注册与当前用户相关的邀请路由。
func registerInvitationSelfRoutes(v1 *gin.RouterGroup, app *bootstrap.App) {
	invitations := handlers.NewInvitationHandler(app.InvitationService)

	v1.GET("/invitations/pending", invitations.ListPendingForMe)
	v1.POST("/invitations/:invitationID/accept", invitations.AcceptInvitation)
}

// guard 为单条路由挂载权限校验中间件。
//
// 独立成函数是为了让"资源 + 操作"在路由表里一目了然，
// 也便于通过测试枚举已注册路由，发现漏挂权限的路由。
func guard(app *bootstrap.App, resource, action string) gin.HandlerFunc {
	return middleware.RequirePermission(app.Authorization, resource, action)
}
