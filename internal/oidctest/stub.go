// Package oidctest 提供可复用的 OIDC 提供方测试桩。
//
// 该包只被 _test.go 引用，不会编入生产二进制。
package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// DefaultKeyID 是桩使用的默认密钥 ID。
const DefaultKeyID = "test-key-1"

// Stub 是一个最小的 OIDC 提供方桩，支持签发令牌与模拟故障。
type Stub struct {
	server  *httptest.Server
	key     *rsa.PrivateKey
	keyID   string
	jwksReq atomic.Int64

	jwksBroken      atomic.Bool
	discoveryBroken atomic.Bool
}

// New 启动桩服务，返回的 Stub 在测试结束时自动关闭服务。
func New(t testing.TB) *Stub {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成测试密钥失败：%v", err)
	}

	s := &Stub{key: key, keyID: DefaultKeyID}
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	s.server = server

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		if s.discoveryBroken.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			// issuer 必须与构造 Verifier 时传入的地址完全一致，否则 go-oidc 直接报错。
			"issuer":                                server.URL,
			"jwks_uri":                              server.URL + "/jwks",
			"authorization_endpoint":                server.URL + "/authorize",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})

	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		s.jwksReq.Add(1)
		if s.jwksBroken.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(s.jwksJSON()))
	})

	return s
}

// Issuer 返回桩的 issuer 地址。
func (s *Stub) Issuer() string { return s.server.URL }

// JWKSRequests 返回 JWKS 被请求的次数，用于断言缓存行为。
func (s *Stub) JWKSRequests() int64 { return s.jwksReq.Load() }

// BreakJWKS 让 JWKS 端点返回 500，模拟身份平台故障。
func (s *Stub) BreakJWKS() { s.jwksBroken.Store(true) }

// BreakDiscovery 让 Discovery 端点返回 500。
func (s *Stub) BreakDiscovery() { s.discoveryBroken.Store(true) }

// Spec 描述要签发的令牌内容。
type Spec struct {
	Issuer    string
	Subject   string
	Audience  any
	ExpiresAt time.Time
	NotBefore *time.Time
	KeyID     string
	ClientID  string
	Alg       string
}

// DefaultSpec 返回一份合法令牌的参数。
func (s *Stub) DefaultSpec(audience string) Spec {
	return Spec{
		Issuer:    s.Issuer(),
		Subject:   "zitadel-001",
		Audience:  audience,
		ExpiresAt: time.Now().Add(10 * time.Minute),
		KeyID:     s.keyID,
		ClientID:  "client-1",
		Alg:       "RS256",
	}
}

// Sign 使用桩的 RSA 私钥签发令牌。
func (s *Stub) Sign(t testing.TB, spec Spec) string {
	t.Helper()

	claims := jwt.MapClaims{
		"iss":       spec.Issuer,
		"sub":       spec.Subject,
		"aud":       spec.Audience,
		"exp":       spec.ExpiresAt.Unix(),
		"iat":       time.Now().Add(-time.Minute).Unix(),
		"client_id": spec.ClientID,
		"name":      "Alice",
		"picture":   "https://example.com/a.png",
	}
	if spec.NotBefore != nil {
		claims["nbf"] = spec.NotBefore.Unix()
	}

	alg := spec.Alg
	if alg == "" {
		alg = "RS256"
	}
	method := jwt.GetSigningMethod(alg)
	if method == nil {
		t.Fatalf("未知的签名算法：%s", alg)
	}

	token := jwt.NewWithClaims(method, claims)
	token.Header["kid"] = spec.KeyID

	if alg == "none" {
		signed, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
		if err != nil {
			t.Fatalf("签发 none 令牌失败：%v", err)
		}
		return signed
	}

	signed, err := token.SignedString(s.key)
	if err != nil {
		t.Fatalf("签发令牌失败：%v", err)
	}
	return signed
}

// SignAlgConfusion 构造算法混淆攻击令牌：用 RSA 公钥作为 HMAC 密钥签发 HS256。
func (s *Stub) SignAlgConfusion(t testing.TB, spec Spec) string {
	t.Helper()

	pub, ok := s.key.Public().(*rsa.PublicKey)
	if !ok {
		t.Fatal("公钥类型断言失败")
	}
	pubDER := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PUBLIC KEY",
		Bytes: x509.MarshalPKCS1PublicKey(pub),
	})

	claims := jwt.MapClaims{
		"iss": spec.Issuer,
		"sub": spec.Subject,
		"aud": spec.Audience,
		"exp": spec.ExpiresAt.Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token.Header["kid"] = spec.KeyID

	signed, err := token.SignedString(pubDER)
	if err != nil {
		t.Fatalf("签发伪造令牌失败：%v", err)
	}
	return signed
}

// SignWithKey 使用指定的外部私钥签发令牌，用于模拟未知密钥。
func (s *Stub) SignWithKey(t testing.TB, key *rsa.PrivateKey, spec Spec) string {
	t.Helper()

	claims := jwt.MapClaims{
		"iss": spec.Issuer,
		"sub": spec.Subject,
		"aud": spec.Audience,
		"exp": spec.ExpiresAt.Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = spec.KeyID

	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("签发令牌失败：%v", err)
	}
	return signed
}

// jwksJSON 用标准库把 RSA 公钥拼装成 JWKS，避免为测试引入额外依赖。
func (s *Stub) jwksJSON() string {
	pub, ok := s.key.Public().(*rsa.PublicKey)
	if !ok {
		return `{"keys":[]}`
	}

	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	eBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(eBytes, uint32(pub.E))
	// 去掉前导零字节，得到最小长度的大端表示。
	trimmed := eBytes
	for len(trimmed) > 1 && trimmed[0] == 0 {
		trimmed = trimmed[1:]
	}
	e := base64.RawURLEncoding.EncodeToString(trimmed)

	return fmt.Sprintf(
		`{"keys":[{"kty":"RSA","alg":"RS256","use":"sig","kid":%q,"n":%q,"e":%q}]}`,
		s.keyID, n, e)
}
