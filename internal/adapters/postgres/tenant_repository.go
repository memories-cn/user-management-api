package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/example/iam-service/internal/domain/membership"
	"github.com/example/iam-service/internal/domain/tenant"
	"github.com/example/iam-service/internal/ports"
)

// tenantModel 是 tenants 表的 GORM 映射，仅在适配器内部使用。
type tenantModel struct {
	ID        uuid.UUID `gorm:"column:id;primaryKey"`
	Name      string    `gorm:"column:name"`
	Slug      string    `gorm:"column:slug"`
	Status    string    `gorm:"column:status"`
	CreatedBy uuid.UUID `gorm:"column:created_by"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// TableName 指定表名。
func (tenantModel) TableName() string { return "tenants" }

// TenantRepository 基于 PostgreSQL 实现租户持久化。
type TenantRepository struct {
	db *gorm.DB
}

// 编译期断言：确保实现 ports.TenantRepository。
var _ ports.TenantRepository = (*TenantRepository)(nil)

// NewTenantRepository 构造租户仓储。
func NewTenantRepository(db *gorm.DB) *TenantRepository {
	return &TenantRepository{db: db}
}

// Create 创建租户。
func (r *TenantRepository) Create(ctx context.Context, t *tenant.Tenant) error {
	if t == nil {
		return errors.New("待创建的租户不能为空")
	}
	if err := dbFrom(ctx, r.db).WithContext(ctx).Create(tenantFromDomain(t)).Error; err != nil {
		return fmt.Errorf("创建租户失败：%w", err)
	}
	return nil
}

// FindByID 依据主键查询租户，未命中返回 (nil, nil)。
func (r *TenantRepository) FindByID(ctx context.Context, id string) (*tenant.Tenant, error) {
	var m tenantModel
	err := dbFrom(ctx, r.db).WithContext(ctx).Where("id = ?", id).Take(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询租户失败：%w", err)
	}
	return tenantToDomain(m), nil
}

// FindBySlug 依据标识查询租户，未命中返回 (nil, nil)。
func (r *TenantRepository) FindBySlug(ctx context.Context, slug string) (*tenant.Tenant, error) {
	var m tenantModel
	err := dbFrom(ctx, r.db).WithContext(ctx).
		Where("slug = ?", tenant.NormalizeSlug(slug)).Take(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询租户失败：%w", err)
	}
	return tenantToDomain(m), nil
}

// ListByMemberUserID 返回该用户所属的租户，查询必须带 user_id 条件（租户隔离）。
func (r *TenantRepository) ListByMemberUserID(ctx context.Context, userID string) ([]*tenant.Tenant, error) {
	var rows []tenantModel
	err := dbFrom(ctx, r.db).WithContext(ctx).
		Select("tenants.*").
		Joins("JOIN memberships m ON m.tenant_id = tenants.id").
		Where("m.user_id = ? AND m.status = ?", userID, string(membership.StatusActive)).
		Order("tenants.created_at ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("查询用户所属租户失败：%w", err)
	}

	result := make([]*tenant.Tenant, 0, len(rows))
	for _, row := range rows {
		result = append(result, tenantToDomain(row))
	}
	return result, nil
}

// CountByCreator 统计该用户创建且未删除的租户数。
func (r *TenantRepository) CountByCreator(ctx context.Context, userID string) (int64, error) {
	var count int64
	err := dbFrom(ctx, r.db).WithContext(ctx).Model(&tenantModel{}).
		Where("created_by = ? AND status <> ?", userID, string(tenant.StatusDeleted)).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("统计用户创建的租户数失败：%w", err)
	}
	return count, nil
}

// Update 更新租户。
func (r *TenantRepository) Update(ctx context.Context, t *tenant.Tenant) error {
	if t == nil {
		return errors.New("待更新的租户不能为空")
	}
	if err := dbFrom(ctx, r.db).WithContext(ctx).Save(tenantFromDomain(t)).Error; err != nil {
		return fmt.Errorf("更新租户失败：%w", err)
	}
	return nil
}

// tenantFromDomain 把领域实体转换为 GORM 模型。
func tenantFromDomain(t *tenant.Tenant) *tenantModel {
	return &tenantModel{
		ID:        t.ID,
		Name:      t.Name,
		Slug:      t.Slug,
		Status:    string(t.Status),
		CreatedBy: t.CreatedBy,
		CreatedAt: t.CreatedAt,
		UpdatedAt: t.UpdatedAt,
	}
}

// tenantToDomain 把 GORM 模型转换为领域实体。
func tenantToDomain(m tenantModel) *tenant.Tenant {
	return &tenant.Tenant{
		ID:        m.ID,
		Name:      m.Name,
		Slug:      m.Slug,
		Status:    tenant.Status(m.Status),
		CreatedBy: m.CreatedBy,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}
