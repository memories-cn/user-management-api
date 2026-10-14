// Package role 编排租户内角色与权限用例。
//
// 分层约束：本包属于 Application，只依赖 ports 定义的接口。
package role

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	appaudit "github.com/example/iam-service/internal/application/audit"
	"github.com/example/iam-service/internal/domain/audit"
	"github.com/example/iam-service/internal/domain/permission"
	"github.com/example/iam-service/internal/domain/role"
	"github.com/example/iam-service/internal/ports"
)

var (
	// ErrRoleNotFound 表示角色不存在，或不属于指定租户。
	// 两种情况对外都返回 404，避免通过状态码枚举角色是否存在。
	ErrRoleNotFound = errors.New("角色不存在")
	// ErrRoleKeyConflict 表示租户内已存在同名角色。
	ErrRoleKeyConflict = errors.New("租户内已存在同名角色")
	// ErrRoleInUse 表示角色仍被成员使用，不能删除。
	ErrRoleInUse = errors.New("角色仍被成员使用，不能删除")
	// ErrPermissionNotFound 表示权限不在系统权限目录中。
	ErrPermissionNotFound = errors.New("权限不在系统权限目录中")
	// ErrInvalidRoleKey 表示角色标识非法。
	ErrInvalidRoleKey = errors.New("角色标识非法")
)

// 分页默认值与上限，与其他管理类列表接口保持一致。
const (
	DefaultLimit = 50
	MaxLimit     = 100
)

// Service 提供租户内角色与角色权限用例。
type Service struct {
	roles       ports.RoleRepository
	permissions ports.PermissionRepository
	policies    ports.PolicyRepository
	tx          ports.Transactor
	reloader    ports.PolicyReloader
	audits      *appaudit.Service
}

// NewService 构造角色用例服务。
func NewService(
	roles ports.RoleRepository,
	permissions ports.PermissionRepository,
	policies ports.PolicyRepository,
	tx ports.Transactor,
	reloader ports.PolicyReloader,
	audits *appaudit.Service,
) *Service {
	return &Service{
		roles:       roles,
		permissions: permissions,
		policies:    policies,
		tx:          tx,
		reloader:    reloader,
		audits:      audits,
	}
}

// RoleListResult 是角色列表的分页结果。
type RoleListResult struct {
	// Roles 是当页角色。
	Roles []*role.Role
	// Total 是租户角色总数。
	Total int64
	// Limit 与 Offset 是实际生效的分页参数。
	Limit  int
	Offset int
}

// ListRoles 分页返回租户角色。
func (s *Service) ListRoles(ctx context.Context, tenantID string, limit, offset int) (*RoleListResult, error) {
	limit, offset = normalizePaging(limit, offset)

	roles, err := s.roles.ListByTenant(ctx, tenantID, limit, offset)
	if err != nil {
		return nil, err
	}
	total, err := s.roles.CountByTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return &RoleListResult{Roles: roles, Total: total, Limit: limit, Offset: offset}, nil
}

// GetRole 返回指定租户下的角色，不存在或跨租户都返回 ErrRoleNotFound。
func (s *Service) GetRole(ctx context.Context, tenantID, roleID string) (*role.Role, error) {
	rl, err := s.roles.FindByID(ctx, tenantID, roleID)
	if err != nil {
		return nil, err
	}
	if rl == nil {
		return nil, ErrRoleNotFound
	}
	return rl, nil
}

// CreateRole 创建租户自定义角色。
//
// 自定义角色的 key 不允许命中系统预置角色集合，否则会绕过系统角色的不可变保护。
func (s *Service) CreateRole(ctx context.Context, tenantID, key, name, description string, actorID uuid.UUID) (*role.Role, error) {
	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		return nil, fmt.Errorf("租户标识非法：%w", err)
	}
	if role.IsSystemKey(key) {
		return nil, fmt.Errorf("%w：%s 是系统预置角色", ErrInvalidRoleKey, key)
	}

	rl, err := role.NewRole(tenantUUID, key, name, description)
	if err != nil {
		return nil, err
	}

	existing, err := s.roles.FindByKey(ctx, tenantID, rl.Key)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("%w：%s", ErrRoleKeyConflict, rl.Key)
	}

	err = s.tx.WithinTx(ctx, func(txCtx context.Context) error {
		if err := s.roles.Create(txCtx, rl); err != nil {
			return err
		}
		return s.audit(txCtx, tenantUUID, actorID, audit.ActionRoleCreate, "role", rl.ID.String(),
			audit.ResultSuccess, map[string]any{"role_key": rl.Key})
	})
	if err != nil {
		return nil, err
	}
	return rl, nil
}

// UpdateRole 修改角色展示信息。
//
// 系统角色也允许改 name/description，但 key 不可变（由领域层保证）。
func (s *Service) UpdateRole(ctx context.Context, tenantID, roleID, name, description string, actorID uuid.UUID) (*role.Role, error) {
	rl, err := s.GetRole(ctx, tenantID, roleID)
	if err != nil {
		return nil, err
	}

	rl.Rename(name)
	rl.SetDescription(description)

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		return nil, fmt.Errorf("租户标识非法：%w", err)
	}

	err = s.tx.WithinTx(ctx, func(txCtx context.Context) error {
		if err := s.roles.Update(txCtx, rl); err != nil {
			return err
		}
		return s.audit(txCtx, tenantUUID, actorID, audit.ActionRoleUpdate, "role", rl.ID.String(),
			audit.ResultSuccess, map[string]any{"role_key": rl.Key})
	})
	if err != nil {
		return nil, err
	}
	return rl, nil
}

// DeleteRole 删除租户自定义角色。
//
// 保护规则（docs/TDD.md 10.2）：系统角色不可删除；仍被成员使用的角色不可删除。
// 业务关联、策略投影与版本号在同一事务内更新。
func (s *Service) DeleteRole(ctx context.Context, tenantID, roleID string, actorID uuid.UUID) error {
	rl, err := s.GetRole(ctx, tenantID, roleID)
	if err != nil {
		return err
	}
	if err := rl.EnsureDeletable(); err != nil {
		return err
	}

	used, err := s.roles.CountAssignments(ctx, tenantID, roleID)
	if err != nil {
		return err
	}
	if used > 0 {
		return fmt.Errorf("%w：%s（%d 个成员仍在使用）", ErrRoleInUse, rl.Key, used)
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		return fmt.Errorf("租户标识非法：%w", err)
	}

	err = s.tx.WithinTx(ctx, func(txCtx context.Context) error {
		if err := s.policies.DeleteRoleRules(txCtx, tenantID, roleID); err != nil {
			return err
		}
		if err := s.roles.Delete(txCtx, tenantID, roleID); err != nil {
			return err
		}
		if _, err := s.policies.BumpVersion(txCtx); err != nil {
			return err
		}
		return s.audit(txCtx, tenantUUID, actorID, audit.ActionRoleDelete, "role", rl.ID.String(),
			audit.ResultSuccess, map[string]any{"role_key": rl.Key})
	})
	if err != nil {
		return err
	}
	return s.reload(ctx)
}

// SetRolePermissions 用给定权限代码整体替换角色的权限集合。
//
// 不允许部分应用：任一代码不在系统目录中，整次变更都会被拒绝（事务回滚），
// 避免角色权限与请求体产生偏差。
func (s *Service) SetRolePermissions(ctx context.Context, tenantID, roleID string, codes []string, actorID uuid.UUID) ([]*permission.Permission, error) {
	rl, err := s.GetRole(ctx, tenantID, roleID)
	if err != nil {
		return nil, err
	}

	if err := permission.ValidateCodes(codes); err != nil {
		return nil, err
	}

	normalized := normalizeCodes(codes)
	perms, err := s.permissions.FindByCodes(ctx, normalized)
	if err != nil {
		return nil, err
	}
	if len(perms) != len(normalized) {
		return nil, ErrPermissionNotFound
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		return nil, fmt.Errorf("租户标识非法：%w", err)
	}

	err = s.tx.WithinTx(ctx, func(txCtx context.Context) error {
		if err := s.applyPermissions(txCtx, tenantID, roleID, perms); err != nil {
			return err
		}
		if _, err := s.policies.BumpVersion(txCtx); err != nil {
			return err
		}
		return s.audit(txCtx, tenantUUID, actorID, audit.ActionRolePermissionUpdate, "role", rl.ID.String(),
			audit.ResultSuccess, map[string]any{"role_key": rl.Key, "permission_count": len(perms)})
	})
	if err != nil {
		return nil, err
	}
	if err := s.reload(ctx); err != nil {
		return nil, err
	}
	return perms, nil
}

// normalizePaging 约束分页参数，避免超大 limit 拖垮查询。
func normalizePaging(limit, offset int) (int, int) {
	switch {
	case limit <= 0:
		limit = DefaultLimit
	case limit > MaxLimit:
		limit = MaxLimit
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// audit 在业务事务内写入审计留痕。
//
// 审计写入失败会让整个事务回滚（fail closed）：
// "改了权限但没留痕"比"操作失败"更不可接受。
func (s *Service) audit(
	ctx context.Context,
	tenantID uuid.UUID,
	actorID uuid.UUID,
	action audit.Action,
	resourceType, resourceID string,
	result audit.Result,
	metadata map[string]any,
) error {
	if s.audits == nil {
		return nil
	}
	return s.audits.RecordAction(ctx, &tenantID, actorID, action, resourceType, resourceID, result, metadata)
}

// ListRolePermissions 返回角色当前的权限集合。
func (s *Service) ListRolePermissions(ctx context.Context, tenantID, roleID string) ([]*permission.Permission, error) {
	if _, err := s.GetRole(ctx, tenantID, roleID); err != nil {
		return nil, err
	}
	return s.roles.ListPermissions(ctx, tenantID, roleID)
}

// ListPermissions 返回系统权限目录。
//
// 目录由系统迁移维护，租户只能在其中组合角色，不能新增可执行权限。
func (s *Service) ListPermissions(ctx context.Context) ([]*permission.Permission, error) {
	return s.permissions.ListAll(ctx)
}

// reload 在策略事务提交后刷新本地内存策略。
//
// 刷新失败必须暴露给调用方：已提交的数据与内存策略不一致时继续服务，
// 会让"撤权"看起来生效而实际仍被放行。
func (s *Service) reload(ctx context.Context) error {
	if s.reloader == nil {
		return nil
	}
	return s.reloader.RefreshIfStale(ctx)
}
