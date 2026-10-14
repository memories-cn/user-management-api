package ports

import (
	"context"

	"github.com/example/iam-service/internal/domain/invitation"
	"github.com/example/iam-service/internal/domain/membership"
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
	// ListByTenant 分页返回租户成员，必须带 tenant_id 条件。
	ListByTenant(ctx context.Context, tenantID string, limit, offset int) ([]*membership.Membership, error)
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
	// ListByTenant 返回租户的邀请列表。
	ListByTenant(ctx context.Context, tenantID string) ([]*invitation.Invitation, error)
	// ListPendingByEmail 返回指定邮箱的待接受邀请，用于"给我的邀请"。
	ListPendingByEmail(ctx context.Context, email string) ([]*invitation.Invitation, error)
	// Update 更新邀请。
	Update(ctx context.Context, inv *invitation.Invitation) error
}
