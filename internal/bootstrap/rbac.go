package bootstrap

import (
	"log/slog"
	"time"

	"github.com/casbin/casbin/v3"
	"gorm.io/gorm"

	casbinadapter "github.com/example/iam-service/internal/adapters/casbin"
	"github.com/example/iam-service/internal/adapters/postgres"
	"github.com/example/iam-service/internal/application/authorization"
	approle "github.com/example/iam-service/internal/application/role"
	"github.com/example/iam-service/internal/application/tenancy"
	"github.com/example/iam-service/internal/ports"
)

// NewPermissionRepository 构造系统权限目录仓储。
func NewPermissionRepository(db *gorm.DB) ports.PermissionRepository {
	return postgres.NewPermissionRepository(db)
}

// NewRoleRepository 构造角色仓储。
func NewRoleRepository(db *gorm.DB) ports.RoleRepository {
	return postgres.NewRoleRepository(db)
}

// NewMembershipRoleRepository 构造成员角色分配仓储。
func NewMembershipRoleRepository(db *gorm.DB) ports.MembershipRoleRepository {
	return postgres.NewMembershipRoleRepository(db)
}

// NewPolicyRepository 构造策略投影仓储。
func NewPolicyRepository(db *gorm.DB) ports.PolicyRepository {
	return postgres.NewPolicyRepository(db)
}

// NewAuthorizer 构造授权器。
//
// 授权器同时承担判定（ports.Authorizer）与失效刷新（ports.PolicyReloader）：
// 策略投影写入事务提交后，必须由它刷新本地内存策略。
func NewAuthorizer(
	enforcer *casbin.SyncedEnforcer,
	policies ports.PolicyRepository,
	interval time.Duration,
) (*casbinadapter.Authorizer, error) {
	authorizer, err := casbinadapter.NewAuthorizer(enforcer, policies, interval)
	if err != nil {
		return nil, err
	}
	slog.Info("授权引擎已就绪", "policy_refresh_interval", interval.String())
	return authorizer, nil
}

// NewRoleService 构造角色与权限用例。
func NewRoleService(
	roles ports.RoleRepository,
	permissions ports.PermissionRepository,
	policies ports.PolicyRepository,
	tx ports.Transactor,
	reloader ports.PolicyReloader,
) *approle.Service {
	return approle.NewService(roles, permissions, policies, tx, reloader)
}

// NewAssignmentService 构造成员角色分配用例。
func NewAssignmentService(
	memberships ports.MembershipRepository,
	roles ports.RoleRepository,
	assignments ports.MembershipRoleRepository,
	policies ports.PolicyRepository,
	tx ports.Transactor,
	reloader ports.PolicyReloader,
) *approle.AssignmentService {
	return approle.NewAssignmentService(memberships, roles, assignments, policies, tx, reloader)
}

// NewAuthorizationService 构造统一授权判定用例。
func NewAuthorizationService(access *tenancy.AccessService, authorizer ports.Authorizer) *authorization.Service {
	return authorization.NewService(access, authorizer)
}
