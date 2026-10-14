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

	appaudit "github.com/example/iam-service/internal/application/audit"
	approle "github.com/example/iam-service/internal/application/role"
	"github.com/example/iam-service/internal/domain/audit"
	"github.com/example/iam-service/internal/domain/identity"
	invitationdomain "github.com/example/iam-service/internal/domain/invitation"
	membershipdomain "github.com/example/iam-service/internal/domain/membership"
	"github.com/example/iam-service/internal/domain/role"
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
	// ErrInvalidInvitationStatus 表示查询条件中的邀请状态非法。
	ErrInvalidInvitationStatus = errors.New("非法的邀请状态")
)

// Service 提供成员邀请相关用例。
type Service struct {
	invitations ports.InvitationRepository
	memberships ports.MembershipRepository
	tx          ports.Transactor
	roles       *approle.AssignmentService
	// defaultRoleKey 是非首个成员加入时绑定的角色标识。
	defaultRoleKey string
	ttl            time.Duration
	// audits 为邀请动作落审计；未装配时跳过留痕。
	audits *appaudit.Service
	// now 注入时钟，便于测试有效期相关分支。
	now func() time.Time
}

// NewService 构造邀请用例服务，ttl 为邀请有效期。
//
// defaultRoleKey 必须能在租户的系统角色中找到，否则接受邀请会失败（快速暴露配置错误）。
func NewService(
	invitations ports.InvitationRepository,
	memberships ports.MembershipRepository,
	tx ports.Transactor,
	roles *approle.AssignmentService,
	defaultRoleKey string,
	ttl time.Duration,
	audits *appaudit.Service,
) *Service {
	return &Service{
		invitations:    invitations,
		memberships:    memberships,
		tx:             tx,
		roles:          roles,
		defaultRoleKey: defaultRoleKey,
		ttl:            ttl,
		audits:         audits,
		now:            time.Now,
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

	// 邀请与审计留痕同事务：复用外层事务（如"创建租户"）或在本层开启。
	err = s.tx.WithinTx(ctx, func(txCtx context.Context) error {
		if err := s.invitations.Create(txCtx, inv); err != nil {
			return err
		}
		return s.audit(txCtx, tenantID, invitedBy, audit.ActionMemberInvite, "invitation", inv.ID.String(),
			audit.ResultSuccess, map[string]any{"reused": false})
	})
	if err != nil {
		return nil, err
	}
	return &InviteResult{Invitation: inv, Token: token}, nil
}

// audit 写入审计留痕；未装配审计服务时跳过。
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

		// 绑定角色：首个加入的成员成为 owner，其余成员按默认角色加入；
		// 已有角色的成员不会被覆盖，保证重复接受邀请的幂等性。
		roleKey := s.defaultRoleKey
		active, err := s.memberships.CountActiveByTenant(txCtx, inv.TenantID.String())
		if err != nil {
			return err
		}
		if active <= 1 {
			roleKey = role.KeyOwner
		}
		if err := s.roles.AssignDefault(txCtx, inv.TenantID.String(), userID.String(), roleKey, userID); err != nil {
			return err
		}
		if _, err := s.roles.BumpVersion(txCtx); err != nil {
			return err
		}

		if err := inv.Accept(userID, s.now()); err != nil {
			return err
		}
		return s.invitations.Update(txCtx, inv)
	})
	if err != nil {
		return nil, err
	}

	// 提交后再刷新本地内存策略，保证新成员的权限立即生效。
	if err := s.roles.Reload(ctx); err != nil {
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

// ListResult 是邀请列表的分页结果。
type ListResult struct {
	// Invitations 是当页邀请。
	Invitations []*invitationdomain.Invitation
	// Total 是满足过滤条件的邀请总数。
	Total int64
	// Limit 与 Offset 是实际生效的分页参数。
	Limit  int
	Offset int
}

// ListByTenant 分页返回租户的邀请列表；status 为空串表示不过滤。
func (s *Service) ListByTenant(ctx context.Context, tenantID uuid.UUID, status string, limit, offset int) (*ListResult, error) {
	limit, offset = normalizePaging(limit, offset)

	if status != "" && !isValidStatus(status) {
		return nil, fmt.Errorf("%w：%s", ErrInvalidInvitationStatus, status)
	}

	list, err := s.invitations.ListByTenant(ctx, tenantID.String(), status, limit, offset)
	if err != nil {
		return nil, err
	}
	total, err := s.invitations.CountByTenant(ctx, tenantID.String(), status)
	if err != nil {
		return nil, err
	}
	return &ListResult{Invitations: list, Total: total, Limit: limit, Offset: offset}, nil
}

// 分页默认值与上限，与管理类列表接口保持一致。
const (
	DefaultLimit = 50
	MaxLimit     = 100
)

// normalizePaging 约束分页参数。
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

// ListPendingForEmail 返回指定邮箱的待接受邀请。
//
// 只有已验证邮箱才有意义；调用方应保证传入的是已验证邮箱。
func (s *Service) ListPendingForEmail(ctx context.Context, email string) ([]*invitationdomain.Invitation, error) {
	return s.invitations.ListPendingByEmail(ctx, email)
}

// findAccepted 查找该邮箱是否已有被接受的邀请。
func (s *Service) findAccepted(ctx context.Context, tenantID uuid.UUID, email string) (*invitationdomain.Invitation, error) {
	list, err := s.invitations.ListByTenant(ctx, tenantID.String(), string(invitationdomain.StatusAccepted), MaxLimit, 0)
	if err != nil {
		return nil, err
	}
	for _, inv := range list {
		if inv.Email == email {
			return inv, nil
		}
	}
	return nil, nil
}

// isValidStatus 报告邀请状态是否合法，用于列表过滤条件校验。
func isValidStatus(status string) bool {
	switch invitationdomain.Status(status) {
	case invitationdomain.StatusPending, invitationdomain.StatusAccepted,
		invitationdomain.StatusCancelled, invitationdomain.StatusExpired:
		return true
	}
	return false
}
