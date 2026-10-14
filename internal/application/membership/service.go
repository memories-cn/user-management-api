// Package membership 编排租户成员管理用例。
//
// 分层约束：本包属于 Application，只依赖 ports 定义的接口。
package membership

import (
	"context"
	"errors"
	"fmt"

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
}

// NewService 构造成员管理服务。
func NewService(
	repo ports.MembershipRepository,
	roles ports.MembershipRoleRepository,
	policies ports.PolicyRepository,
	tx ports.Transactor,
	reloader ports.PolicyReloader,
) *Service {
	return &Service{repo: repo, roles: roles, policies: policies, tx: tx, reloader: reloader}
}

// List 返回租户成员，分页参数会被约束在合理范围内。
func (s *Service) List(ctx context.Context, tenantID string, limit, offset int) ([]*membershipdomain.Membership, error) {
	limit, offset = normalizePaging(limit, offset)
	return s.repo.ListByTenant(ctx, tenantID, limit, offset)
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
func (s *Service) UpdateStatus(ctx context.Context, tenantID, userID string, status membershipdomain.Status) (*membershipdomain.Membership, error) {
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

	switch status {
	case membershipdomain.StatusActive:
		m.Activate()
	case membershipdomain.StatusSuspended:
		m.Suspend()
	}
	if err := s.repo.Update(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}

// Remove 移除成员（软删除），并立即清除其角色与 Casbin 授权规则。
//
// 撤权必须与状态变更在同一事务内完成：只改 memberships 而不删 g 规则，
// 会在策略刷新之前留下一个"已移除但仍可访问"的窗口。
func (s *Service) Remove(ctx context.Context, tenantID, userID string) error {
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
		_, err := s.policies.BumpVersion(txCtx)
		return err
	})
	if err != nil {
		return err
	}
	return s.reload(ctx)
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
