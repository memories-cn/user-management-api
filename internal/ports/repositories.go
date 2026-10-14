package ports

import (
	"context"

	"github.com/google/uuid"

	"github.com/example/iam-service/internal/domain/invitation"
	"github.com/example/iam-service/internal/domain/membership"
	"github.com/example/iam-service/internal/domain/permission"
	"github.com/example/iam-service/internal/domain/role"
	"github.com/example/iam-service/internal/domain/tenant"
	"github.com/example/iam-service/internal/domain/user"
)

// UserRepository 提供业务用户的持久化能力。
//
// 约定：查询未命中时返回 (nil, nil)，未命中不是错误，避免把首次访问当成故障。
type UserRepository interface {
	// FindByZitadelUserID 依据外部身份标识查找用户，未命中返回 (nil, nil)。
	FindByZitadelUserID(ctx context.Context, zitadelUserID string) (*user.User, error)
	// Create 创建用户，唯一约束冲突应返回可被识别为冲突的错误。
	Create(ctx context.Context, u *user.User) error
	// Update 更新用户。
	Update(ctx context.Context, u *user.User) error
}

// TenantRepository 提供租户持久化能力。
type TenantRepository interface {
	// Create 创建租户。
	Create(ctx context.Context, t *tenant.Tenant) error
	// FindByID 依据主键查询租户，未命中返回 (nil, nil)。
	FindByID(ctx context.Context, id string) (*tenant.Tenant, error)
	// FindBySlug 依据标识查询租户，未命中返回 (nil, nil)。
	FindBySlug(ctx context.Context, slug string) (*tenant.Tenant, error)
	// ListByMemberUserID 返回该用户所属的全部租户，必须带 user_id 条件。
	ListByMemberUserID(ctx context.Context, userID string) ([]*tenant.Tenant, error)
	// CountByCreator 统计该用户创建的、未删除的租户数，用于配额等控制。
	CountByCreator(ctx context.Context, userID string) (int64, error)
	// Update 更新租户。
	Update(ctx context.Context, t *tenant.Tenant) error
}

// MembershipRepository 提供成员关系持久化能力。
type MembershipRepository interface {
	// Create 创建成员关系。
	Create(ctx context.Context, m *membership.Membership) error
	// FindByTenantAndUser 查询指定租户下的指定用户成员关系，未命中返回 (nil, nil)。
	FindByTenantAndUser(ctx context.Context, tenantID, userID string) (*membership.Membership, error)
	// ListByTenant 分页返回租户成员，必须带 tenant_id 条件；
	// status 为空串表示不过滤。
	ListByTenant(ctx context.Context, tenantID, status string, limit, offset int) ([]*membership.Membership, error)
	// CountByTenant 统计满足状态过滤的成员数，用于分页信封的 total。
	CountByTenant(ctx context.Context, tenantID, status string) (int64, error)
	// CountActiveByTenant 统计租户内的 active 成员数。
	CountActiveByTenant(ctx context.Context, tenantID string) (int64, error)
	// Update 更新成员关系。
	Update(ctx context.Context, m *membership.Membership) error
}

// InvitationRepository 提供邀请持久化能力。
type InvitationRepository interface {
	// Create 创建邀请。
	Create(ctx context.Context, inv *invitation.Invitation) error
	// FindByID 依据主键查询邀请，未命中返回 (nil, nil)。
	FindByID(ctx context.Context, id string) (*invitation.Invitation, error)
	// FindPendingByTenantAndEmail 查询租户下指定邮箱的待接受邀请，未命中返回 (nil, nil)。
	FindPendingByTenantAndEmail(ctx context.Context, tenantID, email string) (*invitation.Invitation, error)
	// ListByTenant 分页返回租户的邀请列表；status 为空串表示不过滤。
	ListByTenant(ctx context.Context, tenantID, status string, limit, offset int) ([]*invitation.Invitation, error)
	// CountByTenant 统计满足状态过滤的邀请数，用于分页信封的 total。
	CountByTenant(ctx context.Context, tenantID, status string) (int64, error)
	// ListPendingByEmail 返回指定邮箱的待接受邀请，用于"给我的邀请"。
	ListPendingByEmail(ctx context.Context, email string) ([]*invitation.Invitation, error)
	// Update 更新邀请。
	Update(ctx context.Context, inv *invitation.Invitation) error
}

// PermissionRepository 提供系统权限目录的只读访问。
//
// 权限集合由系统迁移维护，因此本接口不提供写入能力：租户只能在既有集合内组合角色。
type PermissionRepository interface {
	// ListAll 返回系统权限目录。
	ListAll(ctx context.Context) ([]*permission.Permission, error)
	// FindByCode 依据权限代码查询，未命中返回 (nil, nil)。
	FindByCode(ctx context.Context, code string) (*permission.Permission, error)
	// FindByCodes 批量查询权限，返回顺序与入参一致；任一代码不存在即返回错误，
	// 避免把不存在的权限静默写入角色。
	FindByCodes(ctx context.Context, codes []string) ([]*permission.Permission, error)
}

// RoleRepository 提供租户角色与角色权限的持久化能力。
//
// 所有方法都必须带 tenant_id 条件：角色不得跨租户使用。
type RoleRepository interface {
	// Create 创建角色。
	Create(ctx context.Context, r *role.Role) error
	// FindByID 查询指定租户下的角色，未命中返回 (nil, nil)。
	FindByID(ctx context.Context, tenantID, roleID string) (*role.Role, error)
	// FindByKey 查询指定租户下指定标识的角色，未命中返回 (nil, nil)。
	FindByKey(ctx context.Context, tenantID, key string) (*role.Role, error)
	// ListByTenant 分页返回租户的角色，必须带 tenant_id 条件。
	ListByTenant(ctx context.Context, tenantID string, limit, offset int) ([]*role.Role, error)
	// CountByTenant 统计租户角色数，用于分页信封的 total。
	CountByTenant(ctx context.Context, tenantID string) (int64, error)
	// Update 更新角色。
	Update(ctx context.Context, r *role.Role) error
	// Delete 删除角色；数据库级联会清除 role_permissions 与 membership_roles。
	Delete(ctx context.Context, tenantID, roleID string) error
	// ReplacePermissions 用给定权限集合整体替换角色的权限关联。
	ReplacePermissions(ctx context.Context, tenantID, roleID string, permissionIDs []uuid.UUID) error
	// ListPermissionIDs 返回角色当前关联的权限 ID。
	ListPermissionIDs(ctx context.Context, tenantID, roleID string) ([]uuid.UUID, error)
	// ListPermissions 返回角色当前关联的权限。
	ListPermissions(ctx context.Context, tenantID, roleID string) ([]*permission.Permission, error)
	// CountAssignments 统计仍在使用该角色的成员数，用于删除前的占用检查。
	CountAssignments(ctx context.Context, tenantID, roleID string) (int64, error)
}

// MembershipRoleRepository 提供成员角色分配的持久化能力。
type MembershipRoleRepository interface {
	// Replace 用给定分配集合整体替换成员的角色的分配。
	Replace(ctx context.Context, tenantID, membershipID string, assignments []*role.Assignment) error
	// ListRoles 返回成员在指定租户下被授予的角色。
	ListRoles(ctx context.Context, tenantID, membershipID string) ([]*role.Role, error)
	// DeleteByMembership 清除成员的全部角色分配（撤权）。
	DeleteByMembership(ctx context.Context, tenantID, membershipID string) error
	// CountActiveOwners 统计租户内 active 成员中被授予 owner 角色的人数，
	// 用于"不能移除最后一个有效 Owner"的保护。
	CountActiveOwners(ctx context.Context, tenantID string) (int64, error)
	// HasActiveRole 报告指定 active 成员是否被授予该租户的某个角色。
	HasActiveRole(ctx context.Context, tenantID, userID, roleKey string) (bool, error)
}

// PolicyPermission 是生成 Casbin p 规则所需的权限二元组。
type PolicyPermission struct {
	// Resource 是受控资源，例如 member。
	Resource string
	// Action 是资源上的操作，例如 invite。
	Action string
}

// PolicyRepository 维护 Casbin 策略在数据库中的投影（casbin_rule 表）与全局版本号。
//
// 关键约定：本接口的写入**只能**发生在业务事务内（实现必须走 dbFrom(ctx)），
// 从而保证"业务关系表 + 策略投影 + 版本号"的原子性（见 docs/TDD.md 8.3）。
// 绝不能在事务外调用 enforcer 的写入接口，否则事务回滚后策略会残留。
type PolicyRepository interface {
	// ReplaceRoleRules 用给定权限集合整体替换某角色在本租户的 p 规则。
	ReplaceRoleRules(ctx context.Context, tenantID, roleID string, perms []PolicyPermission) error
	// ReplaceMemberRules 用给定角色集合整体替换某成员在本租户的 g 规则。
	ReplaceMemberRules(ctx context.Context, tenantID, userID string, roleIDs []string) error
	// DeleteRoleRules 删除某角色的 p 规则以及所有引用它的 g 规则。
	DeleteRoleRules(ctx context.Context, tenantID, roleID string) error
	// DeleteMemberRules 删除某成员在本租户的全部 g 规则（撤权）。
	DeleteMemberRules(ctx context.Context, tenantID, userID string) error
	// DeleteTenantRules 删除租户的全部 p/g 规则。
	DeleteTenantRules(ctx context.Context, tenantID string) error
	// BumpVersion 在同一事务内递增全局策略版本号并返回新版本。
	BumpVersion(ctx context.Context) (int64, error)
	// CurrentVersion 读取全局策略版本号，用于跨实例失效比对。
	CurrentVersion(ctx context.Context) (int64, error)
}
