// Package zitadel 实现与 ZITADEL 交互的适配器。
package zitadel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/example/iam-service/internal/ports"
)

// supportedSigningAlgs 是允许的算法白名单。
//
// 必须显式声明：禁止出现 none 算法，也要防止用 RSA 公钥当 HMAC 密钥的算法混淆攻击。
var supportedSigningAlgs = []string{
	"RS256", "RS384", "RS512",
	"ES256", "ES384", "ES512",
	"EdDSA",
}

// Verifier 通过 OIDC Discovery 与 JWKS 校验 ZITADEL 签发的 JWT 访问令牌。
//
// go-oidc 负责：签名与算法白名单、iss 精确匹配、aud 包含预期受众、exp 有效期、
// 以及 nbf 校验（含 5 分钟时钟偏移）。本适配器补充 sub 非空校验与展示资料提取。
type Verifier struct {
	verifier *oidc.IDTokenVerifier
	issuer   string
	audience string
}

// 编译期断言：确保实现 ports.TokenVerifier。
var _ ports.TokenVerifier = (*Verifier)(nil)

// tokenClaims 是需要从已验证 Token 中额外提取的声明。
type tokenClaims struct {
	// ClientID 来自 ZITADEL 访问令牌的 client_id 声明。
	ClientID string `json:"client_id"`
	// Name 与 Nickname 用于展示名称，Picture 用于头像。
	Name     string `json:"name"`
	Nickname string `json:"nickname"`
	Picture  string `json:"picture"`
	// Email 与 EmailVerified 是成员邀请"先认证、后绑定"的依据。
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	// RawRoles 对应 ZITADEL 的角色声明，取值形态通常为 {"<role>":{"<org>":"<orgID>"}}，
	// 这里只保留原始对象，由 roleKeys 取其键集合。
	RawRoles map[string]json.RawMessage `json:"urn:zitadel:iam:user:roles"`
}

// displayName 按优先级选择展示名称。
func (c tokenClaims) displayName() string {
	if c.Name != "" {
		return c.Name
	}
	return c.Nickname
}

// NewTokenVerifier 完成 OIDC Discovery 并构造验证器。
//
// Discovery 失败意味着身份平台不可用，直接返回 ErrIdentityProviderUnavailable，
// 由调用方在启动阶段快速失败（见 docs/TDD.md 13.2）。
func NewTokenVerifier(ctx context.Context, issuer, audience string) (*Verifier, error) {
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("%w：获取 OIDC Discovery 失败：%v", ports.ErrIdentityProviderUnavailable, err)
	}

	return &Verifier{
		verifier: provider.Verifier(&oidc.Config{
			// ClientID 在此处即期望的 aud（ZITADEL_API_AUDIENCE）。
			ClientID:             audience,
			SupportedSigningAlgs: supportedSigningAlgs,
		}),
		issuer:   issuer,
		audience: audience,
	}, nil
}

// Verify 校验访问令牌并提取可信声明。
func (v *Verifier) Verify(ctx context.Context, rawToken string) (*ports.Claims, error) {
	token, err := v.verifier.Verify(ctx, rawToken)
	if err != nil {
		return nil, classify(err)
	}
	// go-oidc 已完成签名、iss、aud、exp、nbf 校验，此处补充协议必需的 sub。
	if token.Subject == "" {
		return nil, fmt.Errorf("%w：缺少 sub 声明", ports.ErrInvalidToken)
	}

	var extra tokenClaims
	if err := token.Claims(&extra); err != nil {
		// 展示资料解析失败不影响已验证的身份，降级为空并记日志。
		slog.WarnContext(ctx, "failed to extract profile claims", "error", err)
	}

	return &ports.Claims{
		Subject:       token.Subject,
		ClientID:      extra.ClientID,
		DisplayName:   extra.displayName(),
		AvatarURL:     extra.Picture,
		Email:         strings.ToLower(strings.TrimSpace(extra.Email)),
		EmailVerified: extra.EmailVerified,
		Roles:         roleKeys(extra.RawRoles),
	}, nil
}

// roleKeys 从角色声明对象中取出角色名并排序，保证结果稳定。
//
// 角色声明不可用或形态异常时返回 nil，由调用方按"无角色"处理，
// 绝不因解析问题放行平台权限。
func roleKeys(raw map[string]json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// classify 区分凭证无效与身份平台不可用。
//
// 依赖不可用必须失败关闭（503），绝不能被当成"无效凭证"放行或反之掩盖故障。
func classify(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("%w：%v", ports.ErrIdentityProviderUnavailable, err)
	}
	// go-oidc 拉取 JWKS 失败时的错误文本（含 HTTP 非 2xx 场景）。
	if strings.Contains(err.Error(), "get keys failed") {
		return fmt.Errorf("%w：%v", ports.ErrIdentityProviderUnavailable, err)
	}
	return fmt.Errorf("%w：%v", ports.ErrInvalidToken, err)
}
