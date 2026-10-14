// Package ports 定义依赖倒置所需的接口，供 Application 层访问外部能力。
package ports

import (
	"context"
	"errors"
)

var (
	// ErrInvalidToken 表示凭证无效（签名、签发方、受众、有效期任一不合法）。
	// 调用方应映射为 401。
	ErrInvalidToken = errors.New("invalid access token")
	// ErrIdentityProviderUnavailable 表示身份平台或 JWKS 不可用，决策无法完成。
	// 调用方必须失败关闭（503），不得放行（见 docs/TDD.md 7.5）。
	ErrIdentityProviderUnavailable = errors.New("identity provider unavailable")
)

// Claims 是从已验证 Token 中提取的可信身份信息。
//
// 只有签名与受众校验通过后的声明才能填入本结构，禁止把未经验证的请求数据当作身份。
type Claims struct {
	// Subject 是 ZITADEL 用户标识，作为外部身份唯一关联键。
	Subject string
	// ClientID 仅在受众校验通过后提取。
	ClientID string
	// DisplayName 与 AvatarURL 是可选的展示资料。
	DisplayName string
	AvatarURL   string
	// Email 与 EmailVerified 来自已验证令牌，是"先认证、后绑定"的唯一依据。
	// 缺失或 EmailVerified 为 false 时一律视为未验证。
	Email         string
	EmailVerified bool
	// Roles 来自已验证令牌中的角色声明（如 ZITADEL 的 urn:zitadel:iam:user:roles）。
	// 仅用于平台管理员等独立于租户 RBAC 的授权路径，不参与租户内权限判定。
	Roles []string
}

// TokenVerifier 校验访问令牌并提取可信声明。
//
// 约定：
//   - 凭证无效时返回包裹 ErrInvalidToken 的错误；
//   - 依赖不可用（无法获取 Discovery 或 JWKS）时返回包裹 ErrIdentityProviderUnavailable 的错误。
type TokenVerifier interface {
	Verify(ctx context.Context, rawToken string) (*Claims, error)
}
