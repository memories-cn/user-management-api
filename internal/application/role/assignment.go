package role

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"

	appaudit "github.com/example/iam-service/internal/application/audit"
	"github.com/example/iam-service/internal/domain/audit"
	"github.com/example/iam-service/internal/domain/role"
	"github.com/example/iam-service/internal/ports"
)

var (
	// ErrMembershipNotFound 表示成员关系不存在。
	ErrMembershipNotFound = errors.New("成员不存在")
	// ErrMembershipInactive 表示成员关系不是 active，不能分配角色。
	ErrMembershipInactive = errors.New("成员关系不可用")
	// ErrCannotRemoveLastOwner 表示不能移除租户内最后一个有效所有者。
	ErrCannotRemoveLastOwner = errors.New("不能移除租户内最后一个有效所有者")
)

// AssignmentService 编排成员角色分配用例。
type AssignmentService struct {
	memberships ports.MembershipRepository
	roles       ports.RoleRepository
	assignments ports.MembershipRoleRepository
	policies    ports.PolicyRepository
	tx          ports.Transactor
	reloader    ports.PolicyReloader
	audits      *appaudit.Service
}

// NewAssignmentService 构造成员角色分配用例。
func NewAssignmentService(
	memberships ports.MembershipRepository,
	roles ports.RoleRepository,
	assignments ports.MembershipRoleRepository,
	policies ports.PolicyRepository,
	tx ports.Transactor,
	reloader ports.PolicyReloader,
	audits *appaudit.Service,
) *AssignmentService {
	return &AssignmentService{
		memberships: memberships,
		roles:       roles,
		assignments: assignments,
		policies:    policies,
		tx:          tx,
		reloader:    reloader,
		audits:      audits,
	}
}

// ListMemberRoles 返回成员在租户内被授予的角色。
func (s *AssignmentService) ListMemberRoles(ctx context.Context, tenantID, userID string) ([]*role.Role, error) {
	membership, err := s.activeMembership(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	return s.assignments.ListRoles(ctx, tenantID, membership.ID.String())
}

// SetMemberRoles 用给定角色标识整体替换成员的角色。
//
// 保护规则（docs/TDD.md 10.2）：移除最后一个有效 Owner 会被拒绝。
// 业务关联、策略投影与版本号在同一事务内更新，提交后立即刷新内存策略。
func (s *AssignmentService) SetMemberRoles(ctx context.Context, tenantID, userID string, roleKeys []string, actorID uuid.UUID) ([]*role.Role, error) {
	membership, err := s.activeMembership(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}

	roles, err := s.resolveRoles(ctx, tenantID, roleKeys)
	if err != nil {
		return nil, err
	}

	if err := s.ensureOwnerRemains(ctx, tenantID, userID, roleKeys); err != nil {
		return nil, err
	}

	assignments := make([]*role.Assignment, 0, len(roles))
	for _, rl := range roles {
		assignment, err := role.NewAssignment(membership.TenantID, membership.ID, rl.ID, actorID)
		if err != nil {
			return nil, err
		}
		assignments = append(assignments, assignment)
	}

	roleIDs := make([]string, 0, len(roles))
	for _, rl := range roles {
		roleIDs = append(roleIDs, rl.ID.String())
	}

	err = s.tx.WithinTx(ctx, func(txCtx context.Context) error {
		if err := s.assignments.Replace(txCtx, tenantID, membership.ID.String(), assignments); err != nil {
			return err
		}
		if err := s.policies.ReplaceMemberRules(txCtx, tenantID, userID, roleIDs); err != nil {
			return err
		}
		if _, err := s.policies.BumpVersion(txCtx); err != nil {
			return err
		}
		// 角色分配属高风险操作：必须与业务写入同事务留痕。
		return s.audit(txCtx, membership.TenantID, actorID, audit.ActionRoleAssign, "member", userID,
			audit.ResultSuccess, map[string]any{"role_count": len(roles)})
	})
	if err != nil {
		return nil, err
	}
	if err := s.reload(ctx); err != nil {
		return nil, err
	}
	return roles, nil
}

// audit 在业务事务内写入审计留痕，失败即回滚整个操作。
func (s *AssignmentService) audit(
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

// AssignDefault 在成员尚无角色时为其分配指定角色，已有角色则不做变更。
//
// 供"接受邀请"流程使用：首个加入的成员成为 owner，其余成员按默认角色加入。
// 幂等——重复接受邀请不会覆盖管理员已调整过的角色。
//
// 注意：本方法**不开启事务、不递增版本号、不刷新内存策略**，
// 以便调用方把它与"接受邀请"纳入同一个事务。
func (s *AssignmentService) AssignDefault(ctx context.Context, tenantID, userID, roleKey string, actorID uuid.UUID) error {
	membership, err := s.activeMembership(ctx, tenantID, userID)
	if err != nil {
		return err
	}

	current, err := s.assignments.ListRoles(ctx, tenantID, membership.ID.String())
	if err != nil {
		return err
	}
	if len(current) > 0 {
		return nil
	}

	rl, err := s.roles.FindByKey(ctx, tenantID, roleKey)
	if err != nil {
		return err
	}
	if rl == nil {
		return fmt.Errorf("%w：%s", ErrRoleNotFound, roleKey)
	}

	assignment, err := role.NewAssignment(membership.TenantID, membership.ID, rl.ID, actorID)
	if err != nil {
		return err
	}

	if err := s.assignments.Replace(ctx, tenantID, membership.ID.String(), []*role.Assignment{assignment}); err != nil {
		return err
	}
	return s.policies.ReplaceMemberRules(ctx, tenantID, userID, []string{rl.ID.String()})
}

// Reload 刷新本地内存策略，供外部用例在策略事务提交后调用。
func (s *AssignmentService) Reload(ctx context.Context) error {
	return s.reload(ctx)
}

// BumpVersion 递增全局策略版本号，供外部用例在同一事务内调用。
func (s *AssignmentService) BumpVersion(ctx context.Context) (int64, error) {
	return s.policies.BumpVersion(ctx)
}

// RevokeAll 清除成员的全部角色与对应的 Casbin g 规则，即立即撤权。
//
// 供"移除成员"使用；必须在同一事务内执行，避免出现"成员已移除但仍能访问"的窗口。
func (s *AssignmentService) RevokeAll(ctx context.Context, tenantID, membershipID, userID string) error {
	if err := s.assignments.DeleteByMembership(ctx, tenantID, membershipID); err != nil {
		return err
	}
	return s.policies.DeleteMemberRules(ctx, tenantID, userID)
}

// IsActiveOwner 报告指定用户是否为该租户的有效所有者（active 成员 + owner 角色）。
func (s *AssignmentService) IsActiveOwner(ctx context.Context, tenantID, userID string) (bool, error) {
	return s.assignments.HasActiveRole(ctx, tenantID, userID, role.KeyOwner)
}

// CountActiveOwners 统计租户内有效所有者人数。
func (s *AssignmentService) CountActiveOwners(ctx context.Context, tenantID string) (int64, error) {
	return s.assignments.CountActiveOwners(ctx, tenantID)
}

// activeMembership 查询并要求成员关系处于 active 状态。
func (s *AssignmentService) activeMembership(ctx context.Context, tenantID, userID string) (*membershipRef, error) {
	m, err := s.memberships.FindByTenantAndUser(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, ErrMembershipNotFound
	}
	if !m.IsActive() {
		return nil, ErrMembershipInactive
	}
	return &membershipRef{ID: m.ID, TenantID: m.TenantID}, nil
}

// membershipRef 是分配所需的最小成员信息。
type membershipRef struct {
	ID       uuid.UUID
	TenantID uuid.UUID
}

// resolveRoles 按标识逐个解析角色，角色必须属于同一租户。
//
// 跨租户角色在此处就被拒绝（FindByKey 带 tenant_id 条件），
// 数据库复合外键是最后一道防线。
func (s *AssignmentService) resolveRoles(ctx context.Context, tenantID string, roleKeys []string) ([]*role.Role, error) {
	roles := make([]*role.Role, 0, len(roleKeys))
	seen := make(map[string]struct{}, len(roleKeys))
	for _, key := range roleKeys {
		normalized := role.NormalizeKey(key)
		if normalized == "" {
			return nil, fmt.Errorf("%w：标识为空", ErrInvalidRoleKey)
		}
		if _, dup := seen[normalized]; dup {
			continue
		}
		seen[normalized] = struct{}{}

		rl, err := s.roles.FindByKey(ctx, tenantID, normalized)
		if err != nil {
			return nil, err
		}
		if rl == nil {
			return nil, fmt.Errorf("%w：%s", ErrRoleNotFound, normalized)
		}
		roles = append(roles, rl)
	}
	return roles, nil
}

// ensureOwnerRemains 校验本次变更不会移除租户内最后一个有效所有者。
func (s *AssignmentService) ensureOwnerRemains(ctx context.Context, tenantID, userID string, roleKeys []string) error {
	normalized := make([]string, 0, len(roleKeys))
	for _, key := range roleKeys {
		normalized = append(normalized, role.NormalizeKey(key))
	}

	wasOwner, err := s.assignments.HasActiveRole(ctx, tenantID, userID, role.KeyOwner)
	if err != nil {
		return err
	}
	// 原本不是 owner，或本次仍保留 owner，都不会减少 owner 数量。
	if !wasOwner || slices.Contains(normalized, role.KeyOwner) {
		return nil
	}

	owners, err := s.assignments.CountActiveOwners(ctx, tenantID)
	if err != nil {
		return err
	}
	if owners <= 1 {
		return ErrCannotRemoveLastOwner
	}
	return nil
}

// reload 在策略事务提交后刷新本地内存策略。
func (s *AssignmentService) reload(ctx context.Context) error {
	if s.reloader == nil {
		return nil
	}
	return s.reloader.RefreshIfStale(ctx)
}
