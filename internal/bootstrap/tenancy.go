package bootstrap

import (
	"log/slog"

	"gorm.io/gorm"

	"github.com/example/iam-service/internal/adapters/postgres"
	appaudit "github.com/example/iam-service/internal/application/audit"
	appinvitation "github.com/example/iam-service/internal/application/invitation"
	appmembership "github.com/example/iam-service/internal/application/membership"
	"github.com/example/iam-service/internal/application/platformadmin"
	approle "github.com/example/iam-service/internal/application/role"
	"github.com/example/iam-service/internal/application/tenancy"
	apptenant "github.com/example/iam-service/internal/application/tenant"
	"github.com/example/iam-service/internal/config"
	"github.com/example/iam-service/internal/ports"
)

// NewTxManager 构造事务管理器。
func NewTxManager(db *gorm.DB) ports.Transactor {
	return postgres.NewTxManager(db)
}

// NewTenantRepository 构造租户仓储。
func NewTenantRepository(db *gorm.DB) ports.TenantRepository {
	return postgres.NewTenantRepository(db)
}

// NewMembershipRepository 构造成员关系仓储。
func NewMembershipRepository(db *gorm.DB) ports.MembershipRepository {
	return postgres.NewMembershipRepository(db)
}

// NewInvitationRepository 构造邀请仓储。
func NewInvitationRepository(db *gorm.DB) ports.InvitationRepository {
	return postgres.NewInvitationRepository(db)
}

// NewPlatformAdminPolicy 构造平台管理员判定策略。
//
// 启动日志输出生效的角色名，便于确认与 ZITADEL 侧配置一致。
func NewPlatformAdminPolicy(cfg config.Config) (*platformadmin.Policy, error) {
	policy, err := platformadmin.NewPolicy(cfg.PlatformAdminRoleKey)
	if err != nil {
		return nil, err
	}
	slog.Info("platform admin role configured", "role_key", cfg.PlatformAdminRoleKey)
	return policy, nil
}

// TenancyServices 聚合租户域用例，避免装配函数返回过长的裸返回值列表。
type TenancyServices struct {
	// TenantCreator 是平台管理员创建租户用例。
	TenantCreator *apptenant.CreateService
	// MemberService 提供成员管理用例。
	MemberService *appmembership.Service
	// InvitationService 提供邀请用例。
	InvitationService *appinvitation.Service
	// Access 执行租户访问校验。
	Access *tenancy.AccessService
}

// NewTenancyServices 装配邀请、创建租户、成员管理与访问校验用例。
//
// 装配顺序体现依赖方向：邀请依赖角色分配，创建租户依赖邀请与角色，
// 成员管理依赖角色分配与策略投影——因此这些依赖必须先于用例构造。
func NewTenancyServices(
	tenants ports.TenantRepository,
	memberships ports.MembershipRepository,
	invitations ports.InvitationRepository,
	membershipRoles ports.MembershipRoleRepository,
	policies ports.PolicyRepository,
	assignments *approle.AssignmentService,
	roleSvc *approle.Service,
	tx ports.Transactor,
	reloader ports.PolicyReloader,
	audits *appaudit.Service,
	cfg config.Config,
) TenancyServices {
	invitationSvc := appinvitation.NewService(
		invitations, memberships, tx, assignments, cfg.DefaultMemberRoleKey, cfg.InvitationTTL, audits)
	creator := apptenant.NewCreateService(tenants, policies, tx, invitationSvc, roleSvc, reloader, audits)
	members := appmembership.NewService(memberships, membershipRoles, policies, tx, reloader, audits)
	access := tenancy.NewAccessService(tenants, memberships)

	return TenancyServices{
		TenantCreator:     creator,
		MemberService:     members,
		InvitationService: invitationSvc,
		Access:            access,
	}
}

// NewTenantStatusService 构造平台租户状态变更用例。
func NewTenantStatusService(
	tenants ports.TenantRepository,
	tx ports.Transactor,
	audits *appaudit.Service,
) *apptenant.StatusService {
	return apptenant.NewStatusService(tenants, tx, audits)
}
