// Package tenant 编排租户相关用例。
//
// 分层约束：本包属于 Application，只依赖 ports 定义的接口。
package tenant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	appinvitation "github.com/example/iam-service/internal/application/invitation"
	invitationdomain "github.com/example/iam-service/internal/domain/invitation"
	tenantdomain "github.com/example/iam-service/internal/domain/tenant"
	"github.com/example/iam-service/internal/ports"
)

// ErrTenantSlugConflict 表示租户标识已存在。
var ErrTenantSlugConflict = errors.New("租户标识已存在")

// CreateResult 是创建租户的结果。
type CreateResult struct {
	// Tenant 是新创建的租户。
	Tenant *tenantdomain.Tenant
	// Invitation 是为 owner 邮箱创建的邀请；Reused 时表示复用了已有邀请。
	Invitation *invitationdomain.Invitation
	// Token 是邀请的明文令牌，仅新建邀请时非空。
	Token string
	// Reused 表示是否复用了已有的待接受邀请。
	Reused bool
}

// CreateService 提供"平台管理员创建租户"用例。
//
// 职责分离：平台管理员创建租户后**不会**成为该租户成员，
// 成员关系只能通过邀请并由被邀请人完成认证后建立。
type CreateService struct {
	tenants     ports.TenantRepository
	tx          ports.Transactor
	invitations *appinvitation.Service
}

// NewCreateService 构造创建租户用例。
func NewCreateService(tenants ports.TenantRepository, tx ports.Transactor, invitations *appinvitation.Service) *CreateService {
	return &CreateService{tenants: tenants, tx: tx, invitations: invitations}
}

// Create 创建租户并为 owner 邮箱创建邀请。
//
// 租户与邀请在同一事务内创建，避免出现"无 owner 邀请的孤儿租户"。
func (s *CreateService) Create(ctx context.Context, actor uuid.UUID, name, slug, ownerEmail string) (*CreateResult, error) {
	if actor == uuid.Nil {
		return nil, errors.New("创建者不能为空")
	}

	existing, err := s.tenants.FindBySlug(ctx, slug)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("%w：%s", ErrTenantSlugConflict, slug)
	}

	tn, err := tenantdomain.NewTenant(name, slug, actor)
	if err != nil {
		return nil, err
	}

	var result CreateResult
	err = s.tx.WithinTx(ctx, func(txCtx context.Context) error {
		if err := s.tenants.Create(txCtx, tn); err != nil {
			return err
		}

		invited, err := s.invitations.Invite(txCtx, actor, tn.ID, ownerEmail)
		if err != nil {
			return err
		}

		result = CreateResult{
			Tenant:     tn,
			Invitation: invited.Invitation,
			Token:      invited.Token,
			Reused:     invited.Reused,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// 平台管理操作留痕：审计表在 Milestone 5 落地，此处先记结构化日志。
	slog.InfoContext(ctx, "platform admin created tenant",
		"actor_user_id", actor.String(),
		"tenant_id", tn.ID.String(),
		"tenant_slug", tn.Slug,
		"owner_email", result.Invitation.Email,
		"invitation_reused", result.Reused)

	return &result, nil
}
