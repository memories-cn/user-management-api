package bootstrap

import (
	"log/slog"

	"gorm.io/gorm"

	"github.com/example/iam-service/internal/adapters/postgres"
	appinvitation "github.com/example/iam-service/internal/application/invitation"
	appmembership "github.com/example/iam-service/internal/application/membership"
	"github.com/example/iam-service/internal/application/platformadmin"
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

// NewTenancyServices 装配邀请、创建租户、成员管理与访问校验用例。
func NewTenancyServices(
	tenants ports.TenantRepository,
	memberships ports.MembershipRepository,
	invitations ports.InvitationRepository,
	tx ports.Transactor,
	cfg config.Config,
) (*apptenant.CreateService, *appmembership.Service, *appinvitation.Service, *tenancy.AccessService) {
	invitationSvc := appinvitation.NewService(invitations, memberships, tx, cfg.InvitationTTL)
	creator := apptenant.NewCreateService(tenants, tx, invitationSvc)
	members := appmembership.NewService(memberships)
	access := tenancy.NewAccessService(tenants, memberships)
	return creator, members, invitationSvc, access
}
