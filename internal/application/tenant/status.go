package tenant

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	appaudit "github.com/example/iam-service/internal/application/audit"
	auditdomain "github.com/example/iam-service/internal/domain/audit"
	tenantdomain "github.com/example/iam-service/internal/domain/tenant"
	"github.com/example/iam-service/internal/ports"
)

var (
	// ErrTenantNotFound 表示租户不存在。
	// 平台路径不做防枚举：平台管理员是全知的，返回 404 只为表达"没有这个租户"。
	ErrTenantNotFound = errors.New("租户不存在")
	// ErrInvalidStatus 表示目标状态不被允许。
	ErrInvalidStatus = errors.New("非法的租户状态")
)

// StatusService 提供平台管理员变更租户状态的用例。
//
// 边界：本用例只处理 active 与 suspended 的切换，**不提供删除**——
// deleted 涉及数据清理与备份策略，尚未定义，宁可留空也不实现无法撤销的操作。
type StatusService struct {
	tenants ports.TenantRepository
	tx      ports.Transactor
	audits  *appaudit.Service
}

// NewStatusService 构造租户状态变更用例。
func NewStatusService(
	tenants ports.TenantRepository,
	tx ports.Transactor,
	audits *appaudit.Service,
) *StatusService {
	return &StatusService{tenants: tenants, tx: tx, audits: audits}
}

// SetStatus 停用或恢复租户，并在同一事务内留痕。
//
// 停用与恢复都是高风险操作：前者会立刻掐断整个租户的访问，
// 后者会让此前被拦截的成员重新获得能力，两者都必须可追溯。
func (s *StatusService) SetStatus(ctx context.Context, actor, tenantID uuid.UUID, status tenantdomain.Status) (*tenantdomain.Tenant, error) {
	if actor == uuid.Nil {
		return nil, errors.New("操作者不能为空")
	}
	if status != tenantdomain.StatusActive && status != tenantdomain.StatusSuspended {
		return nil, fmt.Errorf("%w：%s", ErrInvalidStatus, status)
	}

	tn, err := s.tenants.FindByID(ctx, tenantID.String())
	if err != nil {
		return nil, err
	}
	if tn == nil {
		return nil, ErrTenantNotFound
	}

	before := tn.Status
	// 幂等：状态未变化时不产生写操作，也不重复留痕。
	if before == status {
		return tn, nil
	}

	var action auditdomain.Action
	switch status {
	case tenantdomain.StatusActive:
		action = auditdomain.ActionTenantActivate
		tn.Status = status
	case tenantdomain.StatusSuspended:
		action = auditdomain.ActionTenantSuspend
		tn.Suspend()
	}

	err = s.tx.WithinTx(ctx, func(txCtx context.Context) error {
		if err := s.tenants.Update(txCtx, tn); err != nil {
			return err
		}
		if s.audits == nil {
			return nil
		}
		// 停用/恢复针对的是具体租户，因此带上 tenant_id：
		// 否则租户管理员在自己的审计日志里看不到"我的租户为什么被停用"。
		// 只有真正与租户无关的平台操作（如创建租户）才使用空 tenant_id，
		// 且空值只用于审计归属，绝不作为隐式提权通道。
		return s.audits.RecordAction(txCtx, &tn.ID, actor, action, "tenant", tn.ID.String(),
			auditdomain.ResultSuccess, map[string]any{
				"tenant_slug": tn.Slug,
				"from_status": string(before),
				"to_status":   string(status),
			})
	})
	if err != nil {
		return nil, err
	}
	return tn, nil
}
