package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/example/iam-service/internal/domain/permission"
	"github.com/example/iam-service/internal/domain/role"
	"github.com/example/iam-service/internal/ports"
)

// roleModel 是 roles 表的 GORM 映射。
type roleModel struct {
	ID          uuid.UUID `gorm:"column:id;primaryKey"`
	TenantID    uuid.UUID `gorm:"column:tenant_id"`
	Key         string    `gorm:"column:key"`
	Name        string    `gorm:"column:name"`
	Description string    `gorm:"column:description"`
	IsSystem    bool      `gorm:"column:is_system"`
	CreatedAt   time.Time `gorm:"column:created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
}

// TableName 指定表名。
func (roleModel) TableName() string { return "roles" }

// rolePermissionModel 是 role_permissions 表的 GORM 映射。
type rolePermissionModel struct {
	RoleID       uuid.UUID `gorm:"column:role_id;primaryKey"`
	PermissionID uuid.UUID `gorm:"column:permission_id;primaryKey"`
	TenantID     uuid.UUID `gorm:"column:tenant_id"`
	CreatedAt    time.Time `gorm:"column:created_at"`
}

// TableName 指定表名。
func (rolePermissionModel) TableName() string { return "role_permissions" }

// RoleRepository 基于 PostgreSQL 实现角色与角色权限持久化。
//
// 所有查询都带 tenant_id 条件：角色不得跨租户使用。
type RoleRepository struct {
	db *gorm.DB
}

// 编译期断言：确保实现 ports.RoleRepository。
var _ ports.RoleRepository = (*RoleRepository)(nil)

// NewRoleRepository 构造角色仓储。
func NewRoleRepository(db *gorm.DB) *RoleRepository {
	return &RoleRepository{db: db}
}

// Create 创建角色。
func (r *RoleRepository) Create(ctx context.Context, rl *role.Role) error {
	if rl == nil {
		return errors.New("待创建的角色不能为空")
	}
	if err := dbFrom(ctx, r.db).WithContext(ctx).Create(roleFromDomain(rl)).Error; err != nil {
		return fmt.Errorf("创建角色失败：%w", err)
	}
	return nil
}

// FindByID 查询指定租户下的角色，未命中返回 (nil, nil)。
func (r *RoleRepository) FindByID(ctx context.Context, tenantID, roleID string) (*role.Role, error) {
	var row roleModel
	err := dbFrom(ctx, r.db).WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, roleID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询角色失败：%w", err)
	}
	return roleToDomain(row), nil
}

// FindByKey 查询指定租户下指定标识的角色，未命中返回 (nil, nil)。
func (r *RoleRepository) FindByKey(ctx context.Context, tenantID, key string) (*role.Role, error) {
	var row roleModel
	err := dbFrom(ctx, r.db).WithContext(ctx).
		Where("tenant_id = ? AND key = ?", tenantID, role.NormalizeKey(key)).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询角色失败：%w", err)
	}
	return roleToDomain(row), nil
}

// ListByTenant 返回租户的全部角色，系统角色排在前面便于管理后台展示。
func (r *RoleRepository) ListByTenant(ctx context.Context, tenantID string) ([]*role.Role, error) {
	var rows []roleModel
	err := dbFrom(ctx, r.db).WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("is_system DESC, key ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("查询租户角色失败：%w", err)
	}

	result := make([]*role.Role, 0, len(rows))
	for _, row := range rows {
		result = append(result, roleToDomain(row))
	}
	return result, nil
}

// Update 更新角色。
func (r *RoleRepository) Update(ctx context.Context, rl *role.Role) error {
	if rl == nil {
		return errors.New("待更新的角色不能为空")
	}
	if err := dbFrom(ctx, r.db).WithContext(ctx).Save(roleFromDomain(rl)).Error; err != nil {
		return fmt.Errorf("更新角色失败：%w", err)
	}
	return nil
}

// Delete 删除角色，数据库级联会清除 role_permissions 与 membership_roles。
func (r *RoleRepository) Delete(ctx context.Context, tenantID, roleID string) error {
	if err := dbFrom(ctx, r.db).WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, roleID).
		Delete(&roleModel{}).Error; err != nil {
		return fmt.Errorf("删除角色失败：%w", err)
	}
	return nil
}

// ReplacePermissions 用给定权限集合整体替换角色的权限关联。
func (r *RoleRepository) ReplacePermissions(ctx context.Context, tenantID, roleID string, permissionIDs []uuid.UUID) error {
	if tenantID == "" || roleID == "" {
		return errors.New("替换角色权限缺少租户或角色标识")
	}

	handle := dbFrom(ctx, r.db).WithContext(ctx)
	if err := handle.Where("tenant_id = ? AND role_id = ?", tenantID, roleID).
		Delete(&rolePermissionModel{}).Error; err != nil {
		return fmt.Errorf("清除角色权限关联失败：%w", err)
	}
	if len(permissionIDs) == 0 {
		return nil
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		return fmt.Errorf("租户标识非法：%w", err)
	}
	roleUUID, err := uuid.Parse(roleID)
	if err != nil {
		return fmt.Errorf("角色标识非法：%w", err)
	}

	rows := make([]rolePermissionModel, 0, len(permissionIDs))
	seen := make(map[uuid.UUID]struct{}, len(permissionIDs))
	for _, permissionID := range permissionIDs {
		if _, dup := seen[permissionID]; dup {
			continue
		}
		seen[permissionID] = struct{}{}
		rows = append(rows, rolePermissionModel{
			RoleID:       roleUUID,
			PermissionID: permissionID,
			TenantID:     tenantUUID,
			CreatedAt:    time.Now(),
		})
	}
	if err := handle.Create(&rows).Error; err != nil {
		return fmt.Errorf("写入角色权限关联失败：%w", err)
	}
	return nil
}

// ListPermissionIDs 返回角色当前关联的权限 ID。
func (r *RoleRepository) ListPermissionIDs(ctx context.Context, tenantID, roleID string) ([]uuid.UUID, error) {
	var rows []rolePermissionModel
	err := dbFrom(ctx, r.db).WithContext(ctx).
		Where("tenant_id = ? AND role_id = ?", tenantID, roleID).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("查询角色权限关联失败：%w", err)
	}

	result := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		result = append(result, row.PermissionID)
	}
	return result, nil
}

// ListPermissions 返回角色当前关联的权限。
func (r *RoleRepository) ListPermissions(ctx context.Context, tenantID, roleID string) ([]*permission.Permission, error) {
	var rows []permissionModel
	err := dbFrom(ctx, r.db).WithContext(ctx).
		Joins("JOIN role_permissions rp ON rp.permission_id = permissions.id").
		Where("rp.tenant_id = ? AND rp.role_id = ?", tenantID, roleID).
		Order("permissions.resource ASC, permissions.action ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("查询角色权限失败：%w", err)
	}

	result := make([]*permission.Permission, 0, len(rows))
	for _, row := range rows {
		result = append(result, permissionToDomain(row))
	}
	return result, nil
}

// CountAssignments 统计仍在使用该角色的成员数，用于删除前的占用检查。
func (r *RoleRepository) CountAssignments(ctx context.Context, tenantID, roleID string) (int64, error) {
	var count int64
	err := dbFrom(ctx, r.db).WithContext(ctx).Model(&membershipRoleModel{}).
		Where("tenant_id = ? AND role_id = ?", tenantID, roleID).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("统计角色使用数失败：%w", err)
	}
	return count, nil
}

// roleFromDomain 把领域实体转换为 GORM 模型。
func roleFromDomain(rl *role.Role) *roleModel {
	return &roleModel{
		ID:          rl.ID,
		TenantID:    rl.TenantID,
		Key:         rl.Key,
		Name:        rl.Name,
		Description: rl.Description,
		IsSystem:    rl.IsSystem,
		CreatedAt:   rl.CreatedAt,
		UpdatedAt:   rl.UpdatedAt,
	}
}

// roleToDomain 把 GORM 模型转换为领域实体。
func roleToDomain(row roleModel) *role.Role {
	return &role.Role{
		ID:          row.ID,
		TenantID:    row.TenantID,
		Key:         row.Key,
		Name:        row.Name,
		Description: row.Description,
		IsSystem:    row.IsSystem,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
}
