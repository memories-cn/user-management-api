// Package platformadmin 实现平台管理员身份判定。
//
// 设计约束（见 docs/TDD.md 7.6）：
//   - 平台权限必须使用独立的、显式的授权路径，不与租户 RBAC 混用；
//   - 不使用通配租户或空 Tenant ID 隐式提权；
//   - 判定依据**只有**已验证令牌中的 ZITADEL 角色声明，不提供白名单或任何应急通道。
package platformadmin

import (
	"errors"
	"slices"

	"github.com/example/iam-service/internal/domain/identity"
)

// ErrInvalidRoleKey 表示平台管理员角色名配置非法。
var ErrInvalidRoleKey = errors.New("平台管理员角色名不能为空")

// Policy 依据已验证令牌中的角色判定平台管理员。
type Policy struct {
	// RoleKey 是平台管理员对应的角色名，例如 platform_admin。
	RoleKey string
}

// NewPolicy 构造判定策略并校验角色名。
func NewPolicy(roleKey string) (*Policy, error) {
	if roleKey == "" {
		return nil, ErrInvalidRoleKey
	}
	return &Policy{RoleKey: roleKey}, nil
}

// IsPlatformAdmin 判定给定身份是否为平台管理员。
//
// 判定只看已验证令牌携带的角色；角色缺失或不含目标角色一律返回 false（fail closed），
// 绝不回退到其他依据。
func (p *Policy) IsPlatformAdmin(principal identity.Principal) bool {
	if p == nil || p.RoleKey == "" || !principal.Authenticated {
		return false
	}
	return slices.Contains(principal.Roles, p.RoleKey)
}
