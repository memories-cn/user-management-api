package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/example/iam-service/internal/domain/permission"
	"github.com/example/iam-service/internal/ports"
)

// permissionModel 是 permissions 表的 GORM 映射。
type permissionModel struct {
	ID          uuid.UUID `gorm:"column:id;primaryKey"`
	Code        string    `gorm:"column:code"`
	Resource    string    `gorm:"column:resource"`
	Action      string    `gorm:"column:action"`
	Description string    `gorm:"column:description"`
	CreatedAt   time.Time `gorm:"column:created_at"`
}

// TableName 指定表名。
func (permissionModel) TableName() string { return "permissions" }

// PermissionRepository 基于 PostgreSQL 实现系统权限目录访问。
type PermissionRepository struct {
	db *gorm.DB
}

// 编译期断言：确保实现 ports.PermissionRepository。
var _ ports.PermissionRepository = (*PermissionRepository)(nil)

// NewPermissionRepository 构造权限目录仓储。
func NewPermissionRepository(db *gorm.DB) *PermissionRepository {
	return &PermissionRepository{db: db}
}

// ListAll 返回系统权限目录，按资源与操作排序保证顺序稳定。
func (r *PermissionRepository) ListAll(ctx context.Context) ([]*permission.Permission, error) {
	var rows []permissionModel
	err := dbFrom(ctx, r.db).WithContext(ctx).
		Order("resource ASC, action ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("查询权限目录失败：%w", err)
	}

	result := make([]*permission.Permission, 0, len(rows))
	for _, row := range rows {
		result = append(result, permissionToDomain(row))
	}
	return result, nil
}

// FindByCode 依据权限代码查询，未命中返回 (nil, nil)。
func (r *PermissionRepository) FindByCode(ctx context.Context, code string) (*permission.Permission, error) {
	var row permissionModel
	err := dbFrom(ctx, r.db).WithContext(ctx).
		Where("code = ?", code).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询权限失败：%w", err)
	}
	return permissionToDomain(row), nil
}

// FindByCodes 批量查询权限，返回顺序与入参一致，缺失的代码直接跳过。
//
// 由调用方比对返回数量与请求数量来判断是否存在目录外的权限：
// 这样"不存在"的语义由用例层决定，仓储只负责查询。
func (r *PermissionRepository) FindByCodes(ctx context.Context, codes []string) ([]*permission.Permission, error) {
	if len(codes) == 0 {
		return nil, nil
	}

	var rows []permissionModel
	err := dbFrom(ctx, r.db).WithContext(ctx).
		Where("code IN ?", codes).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("批量查询权限失败：%w", err)
	}

	byCode := make(map[string]*permission.Permission, len(rows))
	for _, row := range rows {
		byCode[row.Code] = permissionToDomain(row)
	}

	result := make([]*permission.Permission, 0, len(codes))
	for _, code := range codes {
		if found, ok := byCode[code]; ok {
			result = append(result, found)
		}
	}
	return result, nil
}

// permissionToDomain 把 GORM 模型转换为领域实体。
func permissionToDomain(row permissionModel) *permission.Permission {
	return &permission.Permission{
		ID:          row.ID,
		Code:        row.Code,
		Resource:    row.Resource,
		Action:      row.Action,
		Description: row.Description,
		CreatedAt:   row.CreatedAt,
	}
}
