// Package invitation 编排成员邀请用例。
//
// 核心原则：**先认证、后绑定**。邀请只绑定已通过 ZITADEL 认证且邮箱已验证的身份，
// 邮箱不作为身份主键，也绝不因为一个邮箱字符串就创建可登录用户。
//
// 分层约束：本包属于 Application，只依赖 ports 定义的接口。
package invitation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/example/iam-service/internal/domain/identity"
	invitationdomain "github.com/example/iam-service/internal/domain/invitation"
	membershipdomain "github.com/example/iam-service/internal/domain/membership"
	"github.com/example/iam-service/internal/ports"
)

var (
	// ErrInvitationNotFound 表示邀请不存在或不属于指定租户。
	ErrInvitationNotFound = errors.New("邀请不存在")
	// ErrMemberAlreadyExists 表示该邮箱对应的用户已是租户成员。
	ErrMemberAlreadyExists = errors.New("该用户已是租户成员")
	// ErrEmailNotVerified 表示当前身份的邮箱未通过身份平台验证。
	ErrEmailNotVerified = errors.New("邮箱未通过身份平台验证")
	// ErrInvitationEmailMismatch 表示当前身份邮箱与邀请邮箱不一致。
	ErrInvitationEmailMismatch = errors.New("当前身份邮箱与邀请邮箱不一致")
	// ErrInvalidInvitationToken 表示邀请令牌不匹配。
	ErrInvalidInvitationToken = errors.New("邀请令牌无效")
)

// Service 提供成员邀请相关用例。
type Service struct {
	invitations ports.InvitationRepository
	memberships ports.MembershipRepository
	tx          ports.Transactor
	ttl         time.Duration
	// now 注入时钟，便于测试有效期相关分支。
	now func() time.Time
}

// NewService 构造邀请用例服务，ttl 为邀请有效期。
func NewService(
	invitations ports.InvitationRepository,
	memberships ports.MembershipRepository,
	tx ports.Transactor,
	ttl time.Duration,
) *Service {
	return &Service{
		invitations: invitations,
		memberships: memberships,
		tx:          tx,
		ttl:         ttl,
		now:         time.Now,
	}
}

// InviteResult 是发起邀请的结果。
type InviteResult struct {
	// Invitation 是新建或复用的邀请。
	Invitation *invitationdomain.Invitation
	// Token 是明文令牌，仅新建邀请时非空；复用已有邀请时为空串。
	Token string
	// Reused 表示是否复用了已有的待接受邀请。
	Reused bool
}

// Invite 发起邀请，保证幂等。
//
// 幂等规则：同一租户下同一邮箱若已存在未过期的待接受邀请，则刷新有效期并复用，
// 不创建第二条邀请（数据库另有部分唯一索引兜底）。
func (s *Service) Invite(ctx context.Context, invitedBy, tenantID uuid.UUID, email string) (*InviteResult, error) {
	normalized := invitationdomain.NormalizeEmail(email)
	now := s.now()

	pending, err := s.invitations.FindPendingByTenantAndEmail(ctx, tenantID.String(), normalized)
	if err != nil {
		return nil, err
	}
	if pending != nil && !pending.IsExpired(now) {
		// 幂等复用：刷新有效期，不重新下发令牌。
		pending.Refresh(now, s.ttl)
		if err := s.invitations.Update(ctx, pending); err != nil {
			return nil, err
		}
		return &InviteResult{Invitation: pending, Reused: true}, nil
	}

	// 已接受的邀请意味着该邮箱对应用户已成为成员。
	accepted, err := s.findAccepted(ctx, tenantID, normalized)
	if err != nil {
		return nil, err
	}
	if accepted != nil {
		return nil, fmt.Errorf("%w：%s", ErrMemberAlreadyExists, normalized)
	}

	inv, token, err := invitationdomain.NewInvitation(tenantID, email, invitedBy, s.ttl)
	if err != nil {
		return nil, err
	}
	if err := s.invitations.Create(ctx, inv); err != nil {
		return nil, err
	}
	return &InviteResult{Invitation: inv, Token: token}, nil
}

// Accept 接受邀请并绑定当前已认证身份。
//
// 校验顺序固定：状态 → 有效期 → 令牌 → 邮箱验证 → 邮箱匹配，
// 避免在通过全部校验前泄漏邀请详情。
func (s *Service) Accept(ctx context.Context, principal identity.Principal, invitationID uuid.UUID, token string) (*membershipdomain.Membership, error) {
	if !principal.Authenticated {
		return nil, ports.ErrInvalidToken
	}
	userID, err := uuid.Parse(principal.UserID)
	if err != nil {
		return nil, fmt.Errorf("身份中的用户标识非法：%w", err)
	}

	inv, err := s.invitations.FindByID(ctx, invitationID.String())
	if err != nil {
		return nil, err
	}
	if inv == nil {
		return nil, ErrInvitationNotFound
	}

	now := s.now()

	// 幂等：同一用户重复接受同一邀请时直接返回已有成员关系。
	if inv.Status == invitationdomain.StatusAccepted && inv.AcceptedUserID != nil && *inv.AcceptedUserID == userID {
		existing, err := s.memberships.FindByTenantAndUser(ctx, inv.TenantID.String(), userID.String())
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return existing, nil
		}
	}

	// 先判定状态与有效期，避免对已失效邀请继续校验令牌。
	if !inv.IsPending(now) {
		return nil, fmt.Errorf("%w：%s", invitationdomain.ErrNotPending, invitationID)
	}
	if inv.IsExpired(now) {
		// 过期状态落库，便于运营与审计观察。
		_ = inv.Accept(userID, now)
		if updateErr := s.invitations.Update(ctx, inv); updateErr != nil {
			return nil, updateErr
		}
		return nil, fmt.Errorf("%w：%s", invitationdomain.ErrExpired, invitationID)
	}
	if !inv.Matches(token) {
		return nil, ErrInvalidInvitationToken
	}

	// 先认证、后绑定：邮箱必须已验证且与邀请一致。
	if !principal.EmailVerified {
		return nil, ErrEmailNotVerified
	}
	if invitationdomain.NormalizeEmail(principal.Email) != inv.Email {
		return nil, ErrInvitationEmailMismatch
	}

	var result *membershipdomain.Membership
	err = s.tx.WithinTx(ctx, func(txCtx context.Context) error {
		existing, err := s.memberships.FindByTenantAndUser(txCtx, inv.TenantID.String(), userID.String())
		if err != nil {
			return err
		}

		switch {
		case existing == nil:
			created, err := membershipdomain.NewMembership(inv.TenantID, userID)
			if err != nil {
				return err
			}
			if err := s.memberships.Create(txCtx, created); err != nil {
				return err
			}
			result = created
		case existing.IsActive():
			// 幂等：已绑定成功则直接返回。
			result = existing
		default:
			// 被移除或停用的成员重新加入。
			existing.Rejoin(s.now())
			if err := s.memberships.Update(txCtx, existing); err != nil {
				return err
			}
			result = existing
		}

		if err := inv.Accept(userID, s.now()); err != nil {
			return err
		}
		return s.invitations.Update(txCtx, inv)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Cancel 撤销邀请，仅待接受状态可撤销，且必须属于指定租户。
func (s *Service) Cancel(ctx context.Context, tenantID, invitationID uuid.UUID) error {
	inv, err := s.invitations.FindByID(ctx, invitationID.String())
	if err != nil {
		return err
	}
	if inv == nil || inv.TenantID != tenantID {
		return ErrInvitationNotFound
	}
	if err := inv.Cancel(); err != nil {
		return err
	}
	return s.invitations.Update(ctx, inv)
}

// ListByTenant 返回租户的邀请列表。
func (s *Service) ListByTenant(ctx context.Context, tenantID uuid.UUID) ([]*invitationdomain.Invitation, error) {
	return s.invitations.ListByTenant(ctx, tenantID.String())
}

// ListPendingForEmail 返回指定邮箱的待接受邀请。
//
// 只有已验证邮箱才有意义；调用方应保证传入的是已验证邮箱。
func (s *Service) ListPendingForEmail(ctx context.Context, email string) ([]*invitationdomain.Invitation, error) {
	return s.invitations.ListPendingByEmail(ctx, email)
}

// findAccepted 查找该邮箱是否已有被接受的邀请。
func (s *Service) findAccepted(ctx context.Context, tenantID uuid.UUID, email string) (*invitationdomain.Invitation, error) {
	list, err := s.invitations.ListByTenant(ctx, tenantID.String())
	if err != nil {
		return nil, err
	}
	for _, inv := range list {
		if inv.Email == email && inv.Status == invitationdomain.StatusAccepted {
			return inv, nil
		}
	}
	return nil, nil
}
