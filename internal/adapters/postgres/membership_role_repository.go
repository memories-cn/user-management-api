package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/example/iam-service/internal/domain/membership"
	"github.com/example/iam-service/internal/domain/role"
	"github.com/example/iam-service/internal/ports"
)

// membershipRoleModel 是 membership_roles 表的 GORM 映射。
type membershipRoleModel struct {
	MembershipID uuid.UUID `gorm:"column:membership_id;primaryKey"`
	RoleID       uuid.UUID `gorm:"column:role_id;primaryKey"`
	TenantID     uuid.UUID `gorm:"column:tenant_id"`
	AssignedBy   uuid.UUID `gorm:"column:assigned_by"`
	CreatedAt    time.Time `gorm:"column:created_at"`
}

// TableName 指定表名。
func (membershipRoleModel) TableName() string { return "membership_roles" }

// MembershipRoleRepository 基于 PostgreSQL 实现成员角色分配持久化。
type MembershipRoleRepository struct {
	db *gorm.DB
}

// 编译期断言：确保实现 ports.MembershipRoleRepository。
var _ ports.MembershipRoleRepository = (*MembershipRoleRepository)(nil)

// NewMembershipRoleRepository 构造成员角色分配仓储。
func NewMembershipRoleRepository(db *gorm.DB) *MembershipRoleRepository {
	return &MembershipRoleRepository{db: db}
}

// Replace 用给定分配集合整体替换成员的角色分配。
//
// 采用整体替换而非差异比对，保证 membership_roles 与调用方的意图完全一致，
// 不会残留被移除的角色。
func (r *MembershipRoleRepository) Replace(ctx context.Context, tenantID, membershipID string, assignments []*role.Assignment) error {
	if tenantID == "" || membershipID == "" {
		return errors.New("替换成员角色缺少租户或成员关系标识")
	}

	handle := dbFrom(ctx, r.db).WithContext(ctx)
	if err := handle.Where("tenant_id = ? AND membership_id = ?", tenantID, membershipID).
		Delete(&membershipRoleModel{}).Error; err != nil {
		return fmt.Errorf("清除成员角色分配失败：%w", err)
	}
	if len(assignments) == 0 {
		return nil
	}

	rows := make([]membershipRoleModel, 0, len(assignments))
	seen := make(map[uuid.UUID]struct{}, len(assignments))
	for _, a := range assignments {
		if a == nil {
			continue
		}
		if _, dup := seen[a.RoleID]; dup {
			continue
		}
		seen[a.RoleID] = struct{}{}
		rows = append(rows, membershipRoleModel{
			MembershipID: a.MembershipID,
			RoleID:       a.RoleID,
			TenantID:     a.TenantID,
			AssignedBy:   a.AssignedBy,
			CreatedAt:    a.CreatedAt,
		})
	}
	if len(rows) == 0 {
		return nil
	}
	if err := handle.Create(&rows).Error; err != nil {
		return fmt.Errorf("写入成员角色分配失败：%w", err)
	}
	return nil
}

// ListRoles 返回成员在指定租户下被授予的角色。
func (r *MembershipRoleRepository) ListRoles(ctx context.Context, tenantID, membershipID string) ([]*role.Role, error) {
	var rows []roleModel
	err := dbFrom(ctx, r.db).WithContext(ctx).
		Joins("JOIN membership_roles mr ON mr.role_id = roles.id").
		Where("mr.tenant_id = ? AND mr.membership_id = ?", tenantID, membershipID).
		Order("roles.is_system DESC, roles.key ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("查询成员角色失败：%w", err)
	}

	result := make([]*role.Role, 0, len(rows))
	for _, row := range rows {
		result = append(result, roleToDomain(row))
	}
	return result, nil
}

// DeleteByMembership 清除成员的全部角色分配（撤权）。
func (r *MembershipRoleRepository) DeleteByMembership(ctx context.Context, tenantID, membershipID string) error {
	if tenantID == "" || membershipID == "" {
		return errors.New("清除成员角色缺少租户或成员关系标识")
	}
	if err := dbFrom(ctx, r.db).WithContext(ctx).
		Where("tenant_id = ? AND membership_id = ?", tenantID, membershipID).
		Delete(&membershipRoleModel{}).Error; err != nil {
		return fmt.Errorf("清除成员角色分配失败：%w", err)
	}
	return nil
}

// CountActiveOwners 统计租户内 active 成员中被授予 owner 角色的人数。
//
// 三个条件缺一不可：membership 必须是 active（被移除/停用的成员不算有效 Owner）、
// 角色必须是本租户的 owner、两者必须通过 tenant_id 串起来。
func (r *MembershipRoleRepository) CountActiveOwners(ctx context.Context, tenantID string) (int64, error) {
	var count int64
	err := dbFrom(ctx, r.db).WithContext(ctx).Model(&membershipRoleModel{}).
		Joins("JOIN memberships m ON m.id = membership_roles.membership_id AND m.tenant_id = membership_roles.tenant_id").
		Joins("JOIN roles r ON r.id = membership_roles.role_id AND r.tenant_id = membership_roles.tenant_id").
		Where("membership_roles.tenant_id = ? AND m.status = ? AND r.key = ?",
			tenantID, string(membership.StatusActive), role.KeyOwner).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("统计有效所有者人数失败：%w", err)
	}
	return count, nil
}

// HasActiveRole 报告指定 active 成员是否被授予该租户的某个角色。
func (r *MembershipRoleRepository) HasActiveRole(ctx context.Context, tenantID, userID, roleKey string) (bool, error) {
	var count int64
	err := dbFrom(ctx, r.db).WithContext(ctx).Model(&membershipRoleModel{}).
		Joins("JOIN memberships m ON m.id = membership_roles.membership_id AND m.tenant_id = membership_roles.tenant_id").
		Joins("JOIN roles r ON r.id = membership_roles.role_id AND r.tenant_id = membership_roles.tenant_id").
		Where("membership_roles.tenant_id = ? AND m.user_id = ? AND m.status = ? AND r.key = ?",
			tenantID, userID, string(membership.StatusActive), role.NormalizeKey(roleKey)).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("检查成员角色失败：%w", err)
	}
	return count > 0, nil
}
