// Package identity 定义经过认证的可信身份对象。
//
// 分层约束：本包属于 Domain，不得依赖 Gin、GORM、Redis 或 Casbin。
package identity

import "context"

// Principal 是认证中间件向应用层提供的统一身份。
//
// Authenticated 只允许由认证中间件设置，任何请求参数都不得覆盖（见 docs/TDD.md 6.3）。
type Principal struct {
	// Subject 是 Token 中的 ZITADEL 用户标识。
	Subject string
	// UserID 是由可信的本地映射查询得到的内部用户 UUID。
	UserID string
	// ClientID 仅在受众校验通过后从 Token 中提取。
	ClientID string
	// Authenticated 表示身份已经过验证。
	Authenticated bool
	// Email 与 EmailVerified 用于"先认证、后绑定"的成员邀请流程。
	Email         string
	EmailVerified bool
	// Roles 是已验证令牌中的角色声明，仅供平台管理员等独立授权路径使用。
	Roles []string
}

// principalKey 使用私有类型，避免与其他包的上下文键冲突。
type principalKey struct{}

// WithPrincipal 把身份写入上下文。
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// FromContext 读取上下文中的身份，未认证时返回 false。
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	if !ok || !p.Authenticated {
		return Principal{}, false
	}
	return p, true
}
