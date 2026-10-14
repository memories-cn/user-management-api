// Package membership 编排租户成员管理用例。
//
// 分层约束：本包属于 Application，只依赖 ports 定义的接口。
package membership

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	appaudit "github.com/example/iam-service/internal/application/audit"
	"github.com/example/iam-service/internal/domain/audit"
	membershipdomain "github.com/example/iam-service/internal/domain/membership"
	"github.com/example/iam-service/internal/domain/role"
	"github.com/example/iam-service/internal/ports"
)

var (
	// ErrMembershipNotFound 表示成员关系不存在。
	ErrMembershipNotFound = errors.New("成员不存在")
	// ErrInvalidStatus 表示目标状态不被允许。
	ErrInvalidStatus = errors.New("非法的成员状态")
	// ErrCannotRemoveLastMember 表示不能移除租户内最后一个有效成员。
	ErrCannotRemoveLastMember = errors.New("不能移除租户内最后一个有效成员")
	// ErrCannotRemoveLastOwner 表示不能移除租户内最后一个有效所有者。
	ErrCannotRemoveLastOwner = errors.New("不能移除租户内最后一个有效所有者")
)

// 分页默认值与上限。
const (
	DefaultLimit = 50
	MaxLimit     = 100
)

// Service 提供成员管理用例。
type Service struct {
	repo     ports.MembershipRepository
	roles    ports.MembershipRoleRepository
	policies ports.PolicyRepository
	tx       ports.Transactor
	reloader ports.PolicyReloader
	audits   *appaudit.Service
}

// NewService 构造成员管理服务。
func NewService(
	repo ports.MembershipRepository,
	roles ports.MembershipRoleRepository,
	policies ports.PolicyRepository,
	tx ports.Transactor,
	reloader ports.PolicyReloader,
	audits *appaudit.Service,
) *Service {
	return &Service{
		repo: repo, roles: roles, policies: policies,
		tx: tx, reloader: reloader, audits: audits,
	}
}

// ListResult 是成员列表的分页结果。
type ListResult struct {
	// Members 是当页成员。
	Members []*membershipdomain.Membership
	// Total 是满足过滤条件的成员总数。
	Total int64
	// Limit 与 Offset 是实际生效的分页参数。
	Limit  int
	Offset int
}

// List 返回租户成员，分页参数会被约束在合理范围内。
//
// status 为空串表示不过滤；过滤条件下推到仓储层（SQL），不做内存过滤。
func (s *Service) List(ctx context.Context, tenantID, status string, limit, offset int) (*ListResult, error) {
	limit, offset = normalizePaging(limit, offset)

	if status != "" && !isValidStatus(status) {
		return nil, fmt.Errorf("%w：%s", ErrInvalidStatus, status)
	}

	members, err := s.repo.ListByTenant(ctx, tenantID, status, limit, offset)
	if err != nil {
		return nil, err
	}
	total, err := s.repo.CountByTenant(ctx, tenantID, status)
	if err != nil {
		return nil, err
	}
	return &ListResult{Members: members, Total: total, Limit: limit, Offset: offset}, nil
}

// Get 返回指定成员。
func (s *Service) Get(ctx context.Context, tenantID, userID string) (*membershipdomain.Membership, error) {
	m, err := s.repo.FindByTenantAndUser(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, ErrMembershipNotFound
	}
	return m, nil
}

// UpdateStatus 变更成员状态，仅允许在 active 与 suspended 之间切换。
//
// 已移除的成员不在成员列表中，不允许通过本接口复活，须重新走邀请流程。
//
// 注意：停用不改动 Casbin 策略——租户守卫每次请求都会直查数据库校验成员状态，
// 因此停用立即生效，不依赖策略刷新窗口（docs/TDD.md 8.4）。
func (s *Service) UpdateStatus(ctx context.Context, tenantID, userID string, status membershipdomain.Status, actorID uuid.UUID) (*membershipdomain.Membership, error) {
	if status != membershipdomain.StatusActive && status != membershipdomain.StatusSuspended {
		return nil, fmt.Errorf("%w：%s", ErrInvalidStatus, status)
	}

	m, err := s.repo.FindByTenantAndUser(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	if m == nil || m.Status == membershipdomain.StatusRemoved {
		return nil, ErrMembershipNotFound
	}

	before := m.Status
	switch status {
	case membershipdomain.StatusActive:
		m.Activate()
	case membershipdomain.StatusSuspended:
		m.Suspend()
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		return nil, fmt.Errorf("租户标识非法：%w", err)
	}

	err = s.tx.WithinTx(ctx, func(txCtx context.Context) error {
		if err := s.repo.Update(txCtx, m); err != nil {
			return err
		}
		return s.audit(txCtx, tenantUUID, actorID, audit.ActionMemberStatusChange, "member", userID,
			audit.ResultSuccess, map[string]any{
				"from_status": string(before),
				"to_status":   string(status),
			})
	})
	if err != nil {
		return nil, err
	}
	return m, nil
}

// Remove 移除成员（软删除），并立即清除其角色与 Casbin 授权规则。
//
// 撤权必须与状态变更在同一事务内完成：只改 memberships 而不删 g 规则，
// 会在策略刷新之前留下一个"已移除但仍可访问"的窗口。
func (s *Service) Remove(ctx context.Context, tenantID, userID string, actorID uuid.UUID) error {
	m, err := s.repo.FindByTenantAndUser(ctx, tenantID, userID)
	if err != nil {
		return err
	}
	if m == nil || m.Status == membershipdomain.StatusRemoved {
		return ErrMembershipNotFound
	}

	if m.IsActive() {
		active, err := s.repo.CountActiveByTenant(ctx, tenantID)
		if err != nil {
			return err
		}
		if active <= 1 {
			return ErrCannotRemoveLastMember
		}
		if err := s.ensureOwnerRemains(ctx, tenantID, m.UserID.String()); err != nil {
			return err
		}
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		return fmt.Errorf("租户标识非法：%w", err)
	}

	err = s.tx.WithinTx(ctx, func(txCtx context.Context) error {
		m.Remove()
		if err := s.repo.Update(txCtx, m); err != nil {
			return err
		}
		if err := s.roles.DeleteByMembership(txCtx, tenantID, m.ID.String()); err != nil {
			return err
		}
		if err := s.policies.DeleteMemberRules(txCtx, tenantID, m.UserID.String()); err != nil {
			return err
		}
		if _, err := s.policies.BumpVersion(txCtx); err != nil {
			return err
		}
		// 成员移除属高风险操作：撤权与留痕必须在同一事务内完成。
		return s.audit(txCtx, tenantUUID, actorID, audit.ActionMemberRemove, "member", userID,
			audit.ResultSuccess, map[string]any{"to_status": string(membershipdomain.StatusRemoved)})
	})
	if err != nil {
		return err
	}
	return s.reload(ctx)
}

// audit 在业务事务内写入审计留痕，失败即回滚整个操作。
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

// ensureOwnerRemains 校验移除该成员后租户仍有有效所有者。
func (s *Service) ensureOwnerRemains(ctx context.Context, tenantID, userID string) error {
	isOwner, err := s.roles.HasActiveRole(ctx, tenantID, userID, role.KeyOwner)
	if err != nil {
		return err
	}
	if !isOwner {
		return nil
	}

	owners, err := s.roles.CountActiveOwners(ctx, tenantID)
	if err != nil {
		return err
	}
	if owners <= 1 {
		return ErrCannotRemoveLastOwner
	}
	return nil
}

// reload 在策略事务提交后刷新本地内存策略。
func (s *Service) reload(ctx context.Context) error {
	if s.reloader == nil {
		return nil
	}
	return s.reloader.RefreshIfStale(ctx)
}

// isValidStatus 报告成员状态是否合法，用于列表过滤条件校验。
func isValidStatus(status string) bool {
	switch membershipdomain.Status(status) {
	case membershipdomain.StatusActive, membershipdomain.StatusPending,
		membershipdomain.StatusSuspended, membershipdomain.StatusRemoved:
		return true
	}
	return false
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
