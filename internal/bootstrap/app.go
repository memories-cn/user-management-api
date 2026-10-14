package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/casbin/casbin/v3"
	"gorm.io/gorm"

	appinvitation "github.com/example/iam-service/internal/application/invitation"
	appmembership "github.com/example/iam-service/internal/application/membership"
	"github.com/example/iam-service/internal/application/platformadmin"
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
	// Enforcer 是已加载策略的授权引擎，本阶段仅校验其可用，尚未接入业务判定。
	Enforcer *casbin.Enforcer
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

	// TenantCreator 是平台管理员创建租户用例。
	TenantCreator *apptenant.CreateService
	// MemberService 提供成员管理用例。
	MemberService *appmembership.Service
	// InvitationService 提供邀请用例。
	InvitationService *appinvitation.Service
	// Access 执行租户访问校验。
	Access *tenancy.AccessService
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
	tx := NewTxManager(db)

	creator, members, invitationSvc, access := NewTenancyServices(tenants, memberships, invitations, tx, cfg)

	return &App{
		DB:                db,
		Enforcer:          enforcer,
		Verifier:          verifier,
		Users:             users,
		UserSync:          NewUserSync(users),
		Tx:                tx,
		Tenants:           tenants,
		Memberships:       memberships,
		Invitations:       invitations,
		TenantCreator:     creator,
		MemberService:     members,
		InvitationService: invitationSvc,
		Access:            access,
		PlatformAdmin:     platformAdmin,
	}, nil
}

// Check 实现 handlers.ReadinessProbe：校验数据库连通性、迁移状态与策略加载情况。
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
