package zitadel

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/iam-service/internal/oidctest"
	"github.com/example/iam-service/internal/ports"
)

// 测试常量。
const (
	testAudience = "iam-api-audience"
	testSubject  = "zitadel-001"
)

// TestVerify_ValidToken 校验合法令牌能够提取可信身份与展示资料。
func TestVerify_ValidToken(t *testing.T) {
	stub := oidctest.New(t)
	verifier := newVerifier(t, stub)

	claims, err := verifier.Verify(context.Background(), stub.Sign(t, stub.DefaultSpec(testAudience)))

	require.NoError(t, err)
	assert.Equal(t, testSubject, claims.Subject)
	assert.Equal(t, "client-1", claims.ClientID)
	assert.Equal(t, "Alice", claims.DisplayName)
	assert.Equal(t, "https://example.com/a.png", claims.AvatarURL)
}

// TestVerify_RejectsWrongIssuer 校验签发方不符的令牌被拒绝。
func TestVerify_RejectsWrongIssuer(t *testing.T) {
	stub := oidctest.New(t)
	verifier := newVerifier(t, stub)

	spec := stub.DefaultSpec(testAudience)
	spec.Issuer = "https://evil.example.com"

	_, err := verifier.Verify(context.Background(), stub.Sign(t, spec))

	require.ErrorIs(t, err, ports.ErrInvalidToken)
}

// TestVerify_RejectsWrongAudience 校验受众不符的令牌被拒绝。
func TestVerify_RejectsWrongAudience(t *testing.T) {
	stub := oidctest.New(t)
	verifier := newVerifier(t, stub)

	spec := stub.DefaultSpec(testAudience)
	spec.Audience = "other-api"

	_, err := verifier.Verify(context.Background(), stub.Sign(t, spec))

	require.ErrorIs(t, err, ports.ErrInvalidToken)
}

// TestVerify_AcceptsAudienceList 校验受众为列表且包含预期值时通过。
func TestVerify_AcceptsAudienceList(t *testing.T) {
	stub := oidctest.New(t)
	verifier := newVerifier(t, stub)

	spec := stub.DefaultSpec(testAudience)
	spec.Audience = []string{"project-id", testAudience}

	claims, err := verifier.Verify(context.Background(), stub.Sign(t, spec))

	require.NoError(t, err)
	assert.Equal(t, testSubject, claims.Subject)
}

// TestVerify_RejectsExpiredToken 校验过期令牌被拒绝。
func TestVerify_RejectsExpiredToken(t *testing.T) {
	stub := oidctest.New(t)
	verifier := newVerifier(t, stub)

	spec := stub.DefaultSpec(testAudience)
	spec.ExpiresAt = time.Now().Add(-5 * time.Minute)

	_, err := verifier.Verify(context.Background(), stub.Sign(t, spec))

	require.ErrorIs(t, err, ports.ErrInvalidToken)
}

// TestVerify_RejectsNotBeforeInFuture 校验 nbf 在未来的令牌被拒绝。
func TestVerify_RejectsNotBeforeInFuture(t *testing.T) {
	stub := oidctest.New(t)
	verifier := newVerifier(t, stub)

	spec := stub.DefaultSpec(testAudience)
	future := time.Now().Add(30 * time.Minute)
	spec.NotBefore = &future

	_, err := verifier.Verify(context.Background(), stub.Sign(t, spec))

	require.ErrorIs(t, err, ports.ErrInvalidToken)
}

// TestVerify_RejectsAlgConfusion 校验算法混淆攻击被拒绝：
// 用 RSA 公钥作为 HMAC 密钥签发 HS256 令牌，期望因算法不在白名单而被拒绝。
func TestVerify_RejectsAlgConfusion(t *testing.T) {
	stub := oidctest.New(t)
	verifier := newVerifier(t, stub)

	forged := stub.SignAlgConfusion(t, stub.DefaultSpec(testAudience))

	_, err := verifier.Verify(context.Background(), forged)

	require.ErrorIs(t, err, ports.ErrInvalidToken)
}

// TestVerify_RejectsNoneAlgorithm 校验 alg=none 的令牌被拒绝。
func TestVerify_RejectsNoneAlgorithm(t *testing.T) {
	stub := oidctest.New(t)
	verifier := newVerifier(t, stub)

	spec := stub.DefaultSpec(testAudience)
	spec.Alg = "none"

	_, err := verifier.Verify(context.Background(), stub.Sign(t, spec))

	require.ErrorIs(t, err, ports.ErrInvalidToken)
}

// TestVerify_RejectsUnknownKeyID 校验使用未知密钥签发的令牌被拒绝。
func TestVerify_RejectsUnknownKeyID(t *testing.T) {
	stub := oidctest.New(t)
	verifier := newVerifier(t, stub)

	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	spec := stub.DefaultSpec(testAudience)
	spec.KeyID = "unknown-key"

	_, err = verifier.Verify(context.Background(), stub.SignWithKey(t, otherKey, spec))

	require.ErrorIs(t, err, ports.ErrInvalidToken)
}

// TestVerify_RejectsMalformedToken 校验无法解析的令牌被拒绝而不是放行。
func TestVerify_RejectsMalformedToken(t *testing.T) {
	stub := oidctest.New(t)
	verifier := newVerifier(t, stub)

	_, err := verifier.Verify(context.Background(), "not-a-jwt")

	require.ErrorIs(t, err, ports.ErrInvalidToken)
}

// TestVerify_JWKSUnavailable 校验 JWKS 不可用时报依赖不可用，必须失败关闭。
func TestVerify_JWKSUnavailable(t *testing.T) {
	stub := oidctest.New(t)
	verifier := newVerifier(t, stub)
	stub.BreakJWKS()

	_, err := verifier.Verify(context.Background(), stub.Sign(t, stub.DefaultSpec(testAudience)))

	require.ErrorIs(t, err, ports.ErrIdentityProviderUnavailable, "JWKS 不可用必须报依赖不可用，不能当成无效凭证")
}

// TestNewTokenVerifier_DiscoveryUnavailable 校验 Discovery 不可用时构造失败。
func TestNewTokenVerifier_DiscoveryUnavailable(t *testing.T) {
	stub := oidctest.New(t)
	stub.BreakDiscovery()

	_, err := NewTokenVerifier(context.Background(), stub.Issuer(), testAudience)

	require.ErrorIs(t, err, ports.ErrIdentityProviderUnavailable)
}

// TestNewTokenVerifier_DiscoveryUnreachable 校验 Discovery 地址不可达时构造失败。
func TestNewTokenVerifier_DiscoveryUnreachable(t *testing.T) {
	_, err := NewTokenVerifier(context.Background(), "http://127.0.0.1:1", testAudience)

	require.ErrorIs(t, err, ports.ErrIdentityProviderUnavailable)
}

// TestVerify_ReusesCachedJWKS 校验密钥被缓存后不会每次请求都拉取 JWKS。
func TestVerify_ReusesCachedJWKS(t *testing.T) {
	stub := oidctest.New(t)
	verifier := newVerifier(t, stub)

	_, err := verifier.Verify(context.Background(), stub.Sign(t, stub.DefaultSpec(testAudience)))
	require.NoError(t, err)
	first := stub.JWKSRequests()

	_, err = verifier.Verify(context.Background(), stub.Sign(t, stub.DefaultSpec(testAudience)))
	require.NoError(t, err)

	assert.Equal(t, first, stub.JWKSRequests(), "密钥已缓存时不应重复拉取 JWKS")
}

// newVerifier 基于桩服务构造验证器。
func newVerifier(t *testing.T, stub *oidctest.Stub) *Verifier {
	t.Helper()

	v, err := NewTokenVerifier(context.Background(), stub.Issuer(), testAudience)
	require.NoError(t, err)
	return v
}
