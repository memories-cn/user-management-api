// Package authorization 编排统一授权判定用例。
//
// 判定链（docs/TDD.md 7.1、10.1）：
//
//	Authenticated AND UserActive AND TenantActive AND MembershipActive AND PermissionGranted
//
// Casbin 的策略匹配不能替代数据库校验：本用例先完成用户、租户与成员关系校验，
// 再交给授权引擎做权限决策。
//
// 分层约束：本包属于 Application，只依赖 ports 定义的接口。
package authorization

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/example/iam-service/internal/application/tenancy"
	"github.com/example/iam-service/internal/domain/identity"
	"github.com/example/iam-service/internal/ports"
)

// Service 提供统一授权判定用例。
type Service struct {
	access     *tenancy.AccessService
	authorizer ports.Authorizer
}

// NewService 构造授权判定用例。
func NewService(access *tenancy.AccessService, authorizer ports.Authorizer) *Service {
	return &Service{access: access, authorizer: authorizer}
}

// Decision 是授权判定结果。
type Decision struct {
	// Allowed 表示是否放行。
	Allowed bool
	// TenantID 是被判定的租户，便于调用方核对。
	TenantID string
	// Resource 是被判定的资源。
	Resource string
	// Action 是被判定的操作。
	Action string
}

// Authorize 判定当前身份能否在指定租户下对资源执行操作。
//
// 返回值语义（docs/TDD.md 7.5）：
//   - (false, nil)：业务性拒绝——租户不存在、非成员、成员非 active、无权限；
//     这些情况对外都表现为 allowed=false，避免通过响应枚举租户是否存在。
//   - (_, err)：依赖不可用或决策无法完成，调用方必须失败关闭，绝不能当作"拒绝后放行"。
func (s *Service) Authorize(ctx context.Context, principal identity.Principal, tenantID, resource, action string) (*Decision, error) {
	if !principal.Authenticated || principal.UserID == "" {
		return nil, ports.ErrInvalidToken
	}

	decision := &Decision{TenantID: tenantID, Resource: resource, Action: action}

	// 先做数据库校验：用户、租户、成员关系必须全部有效。
	if err := s.access.Check(ctx, principal.UserID, tenantID); err != nil {
		if isBusinessDenial(err) {
			slog.InfoContext(ctx, "授权判定被拒绝",
				"user_id", principal.UserID, "tenant_id", tenantID,
				"resource", resource, "action", action, "reason", err.Error())
			return decision, nil
		}
		return nil, err
	}

	allowed, err := s.authorizer.Enforce(ctx, principal.UserID, tenantID, resource, action)
	if err != nil {
		return nil, err
	}
	decision.Allowed = allowed

	slog.DebugContext(ctx, "授权判定完成",
		"user_id", principal.UserID, "tenant_id", tenantID,
		"resource", resource, "action", action, "allowed", allowed)
	return decision, nil
}

// isBusinessDenial 判定错误是否属于"业务性拒绝"而非依赖故障。
//
// 区分二者是 fail closed 的关键：把依赖故障当成普通拒绝，
// 会让调用方在数据库不可用期间继续按"无权限"处理，掩盖真实的可用性问题。
func isBusinessDenial(err error) bool {
	return errors.Is(err, tenancy.ErrTenantNotFound) ||
		errors.Is(err, tenancy.ErrTenantSuspended) ||
		errors.Is(err, tenancy.ErrMembershipInactive)
}

// AuthorizeForSubject 供可信的服务间调用使用：显式传入已验证的主体标识。
//
// 当前仅用于测试与未来的服务身份链路；HTTP 层不得从请求体读取主体标识
// （docs/TDD.md 9.5）。
func (s *Service) AuthorizeForSubject(ctx context.Context, subject, tenantID, resource, action string) (*Decision, error) {
	if subject == "" {
		return nil, fmt.Errorf("授权主体不能为空：%w", ports.ErrInvalidToken)
	}
	return s.Authorize(ctx, identity.Principal{UserID: subject, Authenticated: true}, tenantID, resource, action)
}
