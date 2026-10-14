package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/example/iam-service/internal/domain/membership"
	"github.com/example/iam-service/internal/ports"
)

// membershipModel 是 memberships 表的 GORM 映射。
type membershipModel struct {
	ID        uuid.UUID  `gorm:"column:id;primaryKey"`
	TenantID  uuid.UUID  `gorm:"column:tenant_id"`
	UserID    uuid.UUID  `gorm:"column:user_id"`
	Status    string     `gorm:"column:status"`
	JoinedAt  *time.Time `gorm:"column:joined_at"`
	CreatedAt time.Time  `gorm:"column:created_at"`
	UpdatedAt time.Time  `gorm:"column:updated_at"`
}

// TableName 指定表名。
func (membershipModel) TableName() string { return "memberships" }

// MembershipRepository 基于 PostgreSQL 实现成员关系持久化。
type MembershipRepository struct {
	db *gorm.DB
}

// 编译期断言：确保实现 ports.MembershipRepository。
var _ ports.MembershipRepository = (*MembershipRepository)(nil)

// NewMembershipRepository 构造成员关系仓储。
func NewMembershipRepository(db *gorm.DB) *MembershipRepository {
	return &MembershipRepository{db: db}
}

// Create 创建成员关系。
func (r *MembershipRepository) Create(ctx context.Context, m *membership.Membership) error {
	if m == nil {
		return errors.New("待创建的成员关系不能为空")
	}
	if err := dbFrom(ctx, r.db).WithContext(ctx).Create(membershipFromDomain(m)).Error; err != nil {
		return fmt.Errorf("创建成员关系失败：%w", err)
	}
	return nil
}

// FindByTenantAndUser 查询指定租户下的指定用户成员关系，未命中返回 (nil, nil)。
func (r *MembershipRepository) FindByTenantAndUser(ctx context.Context, tenantID, userID string) (*membership.Membership, error) {
	var m membershipModel
	err := dbFrom(ctx, r.db).WithContext(ctx).
		Where("tenant_id = ? AND user_id = ?", tenantID, userID).Take(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询成员关系失败：%w", err)
	}
	return membershipToDomain(m), nil
}

// ListByTenant 分页返回租户成员，查询必须带 tenant_id 条件（租户隔离）。
func (r *MembershipRepository) ListByTenant(ctx context.Context, tenantID string, limit, offset int) ([]*membership.Membership, error) {
	var rows []membershipModel
	err := dbFrom(ctx, r.db).WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("created_at ASC").
		Limit(limit).Offset(offset).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("查询租户成员失败：%w", err)
	}

	result := make([]*membership.Membership, 0, len(rows))
	for _, row := range rows {
		result = append(result, membershipToDomain(row))
	}
	return result, nil
}

// CountActiveByTenant 统计租户内的 active 成员数。
func (r *MembershipRepository) CountActiveByTenant(ctx context.Context, tenantID string) (int64, error) {
	var count int64
	err := dbFrom(ctx, r.db).WithContext(ctx).Model(&membershipModel{}).
		Where("tenant_id = ? AND status = ?", tenantID, string(membership.StatusActive)).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("统计租户活跃成员数失败：%w", err)
	}
	return count, nil
}

// Update 更新成员关系。
func (r *MembershipRepository) Update(ctx context.Context, m *membership.Membership) error {
	if m == nil {
		return errors.New("待更新的成员关系不能为空")
	}
	if err := dbFrom(ctx, r.db).WithContext(ctx).Save(membershipFromDomain(m)).Error; err != nil {
		return fmt.Errorf("更新成员关系失败：%w", err)
	}
	return nil
}

// membershipFromDomain 把领域实体转换为 GORM 模型。
func membershipFromDomain(m *membership.Membership) *membershipModel {
	return &membershipModel{
		ID:        m.ID,
		TenantID:  m.TenantID,
		UserID:    m.UserID,
		Status:    string(m.Status),
		JoinedAt:  m.JoinedAt,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

// membershipToDomain 把 GORM 模型转换为领域实体。
func membershipToDomain(m membershipModel) *membership.Membership {
	return &membership.Membership{
		ID:        m.ID,
		TenantID:  m.TenantID,
		UserID:    m.UserID,
		Status:    membership.Status(m.Status),
		JoinedAt:  m.JoinedAt,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}
