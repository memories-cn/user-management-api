// Package tenancy 编排租户访问校验，是租户隔离的统一入口。
//
// 校验链（见 docs/TDD.md 10.1）：
//
//	Authenticated AND UserActive AND TenantActive AND MembershipActive AND ResourceBelongsToTenant
//
// 分层约束：本包属于 Application，只依赖 ports 定义的接口。
package tenancy

import (
	"context"
	"errors"
	"fmt"

	"github.com/example/iam-service/internal/domain/membership"
	"github.com/example/iam-service/internal/domain/tenant"
	"github.com/example/iam-service/internal/ports"
)

var (
	// ErrTenantNotFound 表示租户不存在，或当前用户不是其成员。
	// 两种情况对外都返回 404，避免通过状态码枚举租户是否存在。
	ErrTenantNotFound = errors.New("租户不存在或当前用户不是其成员")
	// ErrTenantSuspended 表示租户已被停用。
	ErrTenantSuspended = errors.New("租户已停用")
	// ErrMembershipInactive 表示成员关系不是 active。
	ErrMembershipInactive = errors.New("成员关系不可用")
)

// AccessService 校验当前用户能否访问指定租户。
type AccessService struct {
	tenants     ports.TenantRepository
	memberships ports.MembershipRepository
}

// NewAccessService 构造租户访问校验服务。
func NewAccessService(tenants ports.TenantRepository, memberships ports.MembershipRepository) *AccessService {
	return &AccessService{tenants: tenants, memberships: memberships}
}

// Check 校验租户状态与成员资格，通过返回 nil。
//
// 注意：Casbin 的策略匹配不能替代这里的数据库校验（见 docs/TDD.md 7.1）。
func (s *AccessService) Check(ctx context.Context, userID, tenantID string) error {
	tn, err := s.tenants.FindByID(ctx, tenantID)
	if err != nil {
		return err
	}
	if tn == nil {
		return ErrTenantNotFound
	}
	if !tn.IsActive() {
		return ErrTenantSuspended
	}

	m, err := s.memberships.FindByTenantAndUser(ctx, tenantID, userID)
	if err != nil {
		return err
	}
	if m == nil {
		return ErrTenantNotFound
	}
	if !m.IsActive() {
		return fmt.Errorf("%w：%s", ErrMembershipInactive, m.Status)
	}
	return nil
}

// ActiveTenant 返回校验通过的租户，供上层复用查询结果。
func (s *AccessService) ActiveTenant(ctx context.Context, tenantID string) (*tenant.Tenant, error) {
	tn, err := s.tenants.FindByID(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if tn == nil || !tn.IsActive() {
		return nil, ErrTenantNotFound
	}
	return tn, nil
}

// ActiveMemberStatus 返回当前用户在该租户下的成员状态，未加入返回空状态。
func (s *AccessService) ActiveMemberStatus(ctx context.Context, tenantID, userID string) (membership.Status, error) {
	m, err := s.memberships.FindByTenantAndUser(ctx, tenantID, userID)
	if err != nil {
		return "", err
	}
	if m == nil {
		return "", nil
	}
	return m.Status, nil
}
