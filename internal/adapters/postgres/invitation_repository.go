package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/example/iam-service/internal/domain/invitation"
	"github.com/example/iam-service/internal/ports"
)

// invitationModel 是 invitations 表的 GORM 映射。
type invitationModel struct {
	ID              uuid.UUID  `gorm:"column:id;primaryKey"`
	TenantID        uuid.UUID  `gorm:"column:tenant_id"`
	Email           string     `gorm:"column:email"`
	TokenHash       []byte     `gorm:"column:token_hash"`
	Status          string     `gorm:"column:status"`
	InvitedByUserID uuid.UUID  `gorm:"column:invited_by_user_id"`
	AcceptedUserID  *uuid.UUID `gorm:"column:accepted_user_id"`
	ExpiresAt       time.Time  `gorm:"column:expires_at"`
	AcceptedAt      *time.Time `gorm:"column:accepted_at"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
	UpdatedAt       time.Time  `gorm:"column:updated_at"`
}

// TableName 指定表名。
func (invitationModel) TableName() string { return "invitations" }

// InvitationRepository 基于 PostgreSQL 实现邀请持久化。
type InvitationRepository struct {
	db *gorm.DB
}

// 编译期断言：确保实现 ports.InvitationRepository。
var _ ports.InvitationRepository = (*InvitationRepository)(nil)

// NewInvitationRepository 构造邀请仓储。
func NewInvitationRepository(db *gorm.DB) *InvitationRepository {
	return &InvitationRepository{db: db}
}

// Create 创建邀请。
func (r *InvitationRepository) Create(ctx context.Context, inv *invitation.Invitation) error {
	if inv == nil {
		return errors.New("待创建的邀请不能为空")
	}
	if err := dbFrom(ctx, r.db).WithContext(ctx).Create(invitationFromDomain(inv)).Error; err != nil {
		return fmt.Errorf("创建邀请失败：%w", err)
	}
	return nil
}

// FindByID 依据主键查询邀请，未命中返回 (nil, nil)。
func (r *InvitationRepository) FindByID(ctx context.Context, id string) (*invitation.Invitation, error) {
	var m invitationModel
	err := dbFrom(ctx, r.db).WithContext(ctx).Where("id = ?", id).Take(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询邀请失败：%w", err)
	}
	return invitationToDomain(m), nil
}

// FindPendingByTenantAndEmail 查询租户下指定邮箱的待接受邀请，未命中返回 (nil, nil)。
func (r *InvitationRepository) FindPendingByTenantAndEmail(ctx context.Context, tenantID, email string) (*invitation.Invitation, error) {
	var m invitationModel
	err := dbFrom(ctx, r.db).WithContext(ctx).
		Where("tenant_id = ? AND lower(email) = ? AND status = ?",
			tenantID, invitation.NormalizeEmail(email), string(invitation.StatusPending)).
		Take(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询待接受邀请失败：%w", err)
	}
	return invitationToDomain(m), nil
}

// ListByTenant 返回租户的邀请列表，查询必须带 tenant_id 条件。
func (r *InvitationRepository) ListByTenant(ctx context.Context, tenantID string) ([]*invitation.Invitation, error) {
	var rows []invitationModel
	err := dbFrom(ctx, r.db).WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("created_at DESC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("查询租户邀请失败：%w", err)
	}
	return toInvitations(rows), nil
}

// ListPendingByEmail 返回指定邮箱的待接受邀请。
func (r *InvitationRepository) ListPendingByEmail(ctx context.Context, email string) ([]*invitation.Invitation, error) {
	var rows []invitationModel
	err := dbFrom(ctx, r.db).WithContext(ctx).
		Where("lower(email) = ? AND status = ?",
			invitation.NormalizeEmail(email), string(invitation.StatusPending)).
		Order("created_at DESC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("查询待接受邀请失败：%w", err)
	}
	return toInvitations(rows), nil
}

// Update 更新邀请。
func (r *InvitationRepository) Update(ctx context.Context, inv *invitation.Invitation) error {
	if inv == nil {
		return errors.New("待更新的邀请不能为空")
	}
	if err := dbFrom(ctx, r.db).WithContext(ctx).Save(invitationFromDomain(inv)).Error; err != nil {
		return fmt.Errorf("更新邀请失败：%w", err)
	}
	return nil
}

// toInvitations 批量转换 GORM 模型。
func toInvitations(rows []invitationModel) []*invitation.Invitation {
	result := make([]*invitation.Invitation, 0, len(rows))
	for _, row := range rows {
		result = append(result, invitationToDomain(row))
	}
	return result
}

// invitationFromDomain 把领域实体转换为 GORM 模型。
func invitationFromDomain(inv *invitation.Invitation) *invitationModel {
	return &invitationModel{
		ID:              inv.ID,
		TenantID:        inv.TenantID,
		Email:           inv.Email,
		TokenHash:       inv.TokenHash,
		Status:          string(inv.Status),
		InvitedByUserID: inv.InvitedBy,
		AcceptedUserID:  inv.AcceptedUserID,
		ExpiresAt:       inv.ExpiresAt,
		AcceptedAt:      inv.AcceptedAt,
		CreatedAt:       inv.CreatedAt,
		UpdatedAt:       inv.UpdatedAt,
	}
}

// invitationToDomain 把 GORM 模型转换为领域实体。
func invitationToDomain(m invitationModel) *invitation.Invitation {
	return &invitation.Invitation{
		ID:             m.ID,
		TenantID:       m.TenantID,
		Email:          m.Email,
		TokenHash:      m.TokenHash,
		Status:         invitation.Status(m.Status),
		InvitedBy:      m.InvitedByUserID,
		AcceptedUserID: m.AcceptedUserID,
		ExpiresAt:      m.ExpiresAt,
		AcceptedAt:     m.AcceptedAt,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
	}
}
