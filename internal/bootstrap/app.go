package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/casbin/casbin/v3"
	"gorm.io/gorm"

	casbinadapter "github.com/example/iam-service/internal/adapters/casbin"
	appaudit "github.com/example/iam-service/internal/application/audit"
	"github.com/example/iam-service/internal/application/authorization"
	appinvitation "github.com/example/iam-service/internal/application/invitation"
	appmembership "github.com/example/iam-service/internal/application/membership"
	"github.com/example/iam-service/internal/application/platformadmin"
	approle "github.com/example/iam-service/internal/application/role"
	"github.com/example/iam-service/internal/application/tenancy"
	apptenant "github.com/example/iam-service/internal/application/tenant"
	appuser "github.com/example/iam-service/internal/application/user"
	"github.com/example/iam-service/internal/config"
	"github.com/example/iam-service/internal/ports"
)

// App 聚合进程运行期依赖，提供统一的就绪检查入口。
type App struct {
	// DB 是业务与策略持久化的数据库句柄。
	DB *gorm.DB
	// Enforcer 是已加载策略的授权引擎（并发安全）。
	Enforcer *casbin.SyncedEnforcer
	// Authorizer 执行统一授权判定与策略失效刷新。
	Authorizer *casbinadapter.Authorizer
	// Verifier 校验访问令牌。
	Verifier ports.TokenVerifier
	// Users 是用户仓储。
	Users ports.UserRepository
	// UserSync 执行用户按需同步。
	UserSync *appuser.SyncService

	// Tx 提供事务边界。
	Tx ports.Transactor
	// Tenants、Memberships、Invitations 是租户域仓储。
	Tenants     ports.TenantRepository
	Memberships ports.MembershipRepository
	Invitations ports.InvitationRepository
	// Permissions、Roles、MembershipRoles、Policies 是授权域仓储。
	Permissions     ports.PermissionRepository
	Roles           ports.RoleRepository
	MembershipRoles ports.MembershipRoleRepository
	Policies        ports.PolicyRepository
	// Audits 是审计仓储，AuditService 提供审计写入与查询用例。
	Audits       ports.AuditRepository
	AuditService *appaudit.Service

	// TenantCreator 是平台管理员创建租户用例。
	TenantCreator *apptenant.CreateService
	// TenantStatus 是平台管理员变更租户状态的用例。
	TenantStatus *apptenant.StatusService
	// MemberService 提供成员管理用例。
	MemberService *appmembership.Service
	// InvitationService 提供邀请用例。
	InvitationService *appinvitation.Service
	// Access 执行租户访问校验。
	Access *tenancy.AccessService
	// RoleService 提供角色与权限用例。
	RoleService *approle.Service
	// AssignmentService 提供成员角色分配用例。
	AssignmentService *approle.AssignmentService
	// Authorization 提供统一授权判定用例。
	Authorization *authorization.Service
	// PlatformAdmin 判定平台管理员。
	PlatformAdmin *platformadmin.Policy
}

// New 按 配置 → 数据库 → 迁移校验 → 授权引擎 → 身份与租户链路 的顺序装配依赖。
// 任一步失败即返回错误，由调用方决定是否退出进程（快速失败）。
func New(cfg config.Config) (*App, error) {
	db, err := OpenDB(cfg)
	if err != nil {
		return nil, err
	}

	if err := VerifyMigrations(context.Background(), db); err != nil {
		closeDB(db)
		return nil, err
	}

	enforcer, err := NewEnforcer(db, cfg.CasbinModelPath)
	if err != nil {
		closeDB(db)
		return nil, err
	}

	verifier, err := NewTokenVerifier(context.Background(), cfg)
	if err != nil {
		closeDB(db)
		return nil, err
	}

	platformAdmin, err := NewPlatformAdminPolicy(cfg)
	if err != nil {
		closeDB(db)
		return nil, err
	}

	users := NewUserRepository(db)
	tenants := NewTenantRepository(db)
	memberships := NewMembershipRepository(db)
	invitations := NewInvitationRepository(db)
	permissions := NewPermissionRepository(db)
	roles := NewRoleRepository(db)
	membershipRoles := NewMembershipRoleRepository(db)
	policies := NewPolicyRepository(db)
	audits := NewAuditRepository(db)
	tx := NewTxManager(db)

	// 审计用例必须先于业务用例构造：业务用例要在事务内写入留痕。
	auditSvc := appaudit.NewService(audits)

	// 授权器必须在用例之前构造：用例在策略提交后要靠它刷新本地内存策略。
	authorizer, err := NewAuthorizer(enforcer, policies, cfg.PolicyRefreshInterval)
	if err != nil {
		closeDB(db)
		return nil, err
	}

	roleSvc := NewRoleService(roles, permissions, policies, tx, authorizer, auditSvc)
	assignmentSvc := NewAssignmentService(memberships, roles, membershipRoles, policies, tx, authorizer, auditSvc)

	services := NewTenancyServices(
		tenants, memberships, invitations, membershipRoles, policies,
		assignmentSvc, roleSvc, tx, authorizer, auditSvc, cfg,
	)

	return &App{
		DB:                db,
		Enforcer:          enforcer,
		Authorizer:        authorizer,
		Verifier:          verifier,
		Users:             users,
		UserSync:          NewUserSync(users),
		Tx:                tx,
		Tenants:           tenants,
		Memberships:       memberships,
		Invitations:       invitations,
		Permissions:       permissions,
		Roles:             roles,
		MembershipRoles:   membershipRoles,
		Policies:          policies,
		Audits:            audits,
		AuditService:      auditSvc,
		TenantCreator:     services.TenantCreator,
		TenantStatus:      NewTenantStatusService(tenants, tx, auditSvc),
		MemberService:     services.MemberService,
		InvitationService: services.InvitationService,
		Access:            services.Access,
		RoleService:       roleSvc,
		AssignmentService: assignmentSvc,
		Authorization:     NewAuthorizationService(services.Access, authorizer),
		PlatformAdmin:     platformAdmin,
	}, nil
}

// Check 实现 handlers.ReadinessProbe：校验数据库连通性、迁移状态、策略加载与刷新情况。
// 任一依赖不可用都返回错误，调用方应返回 503 而不是放行（fail closed）。
func (a *App) Check(ctx context.Context) error {
	sqlDB, err := a.DB.DB()
	if err != nil {
		return fmt.Errorf("获取底层 sql.DB 失败：%w", err)
	}
	if err := ping(ctx, sqlDB); err != nil {
		return err
	}
	if err := VerifyMigrations(ctx, a.DB); err != nil {
		return err
	}
	if a.Enforcer == nil || a.Enforcer.GetModel() == nil {
		return errors.New("授权引擎策略未加载")
	}
	// 策略刷新失败意味着内存策略可能已过期，此时必须摘除流量而不是继续判定。
	if err := a.Authorizer.LastError(); err != nil {
		return fmt.Errorf("授权策略刷新失败：%w", err)
	}
	return nil
}

// Close 释放数据库连接。
func (a *App) Close() error {
	return closeDB(a.DB)
}

// closeDB 关闭底层连接并忽略已关闭等无害错误。
func closeDB(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	if err := sqlDB.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
		return fmt.Errorf("关闭数据库连接失败：%w", err)
	}
	return nil
}
