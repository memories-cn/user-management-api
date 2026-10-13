package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	appuser "github.com/example/iam-service/internal/application/user"
	"github.com/example/iam-service/internal/domain/identity"
	"github.com/example/iam-service/internal/domain/user"
	"github.com/example/iam-service/internal/ports"
	"github.com/example/iam-service/internal/transport/http/responses"
)

// fakeVerifier 是 ports.TokenVerifier 的测试替身。
type fakeVerifier struct {
	claims *ports.Claims
	err    error
	// seenToken 记录实际传入中间件的令牌，用于断言只读取 Authorization 头。
	seenToken string
}

func (f *fakeVerifier) Verify(_ context.Context, rawToken string) (*ports.Claims, error) {
	f.seenToken = rawToken
	return f.claims, f.err
}

// fakeEnsurer 是 UserEnsurer 的测试替身。
type fakeEnsurer struct {
	u   *user.User
	err error
}

func (f *fakeEnsurer) Ensure(context.Context, *ports.Claims) (*user.User, error) {
	return f.u, f.err
}

// newAuthNRouter 构建装配 RequestID 与 AuthN 的测试路由，
// /secure 端点回显从上下文中取到的身份。
func newAuthNRouter(verifier ports.TokenVerifier, ensurer UserEnsurer) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestID(), AuthN(verifier, ensurer))
	r.GET("/secure", func(c *gin.Context) {
		p, ok := identity.FromContext(c.Request.Context())
		if !ok {
			responses.Fail(c, http.StatusUnauthorized, "UNAUTHENTICATED", "missing or invalid credentials")
			return
		}
		responses.OK(c, map[string]string{"user_id": p.UserID, "subject": p.Subject})
	})
	return r
}

func doGetWithAuth(t *testing.T, router *gin.Engine, header string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	// 安全回归：客户端伪造的身份头不得影响真实身份。
	req.Header.Set("X-User-ID", "attacker-id")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// activeUser 返回一个可用用户。
func activeUser(t *testing.T) *user.User {
	t.Helper()

	u, err := user.NewUser("zitadel-001")
	require.NoError(t, err)
	return u
}

// TestAuthN_MissingHeader 校验缺少 Authorization 头时返回 401。
func TestAuthN_MissingHeader(t *testing.T) {
	rec := doGetWithAuth(t, newAuthNRouter(&fakeVerifier{}, &fakeEnsurer{}), "")

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "UNAUTHENTICATED")
}

// TestAuthN_WrongScheme 校验非 Bearer 方案时返回 401。
func TestAuthN_WrongScheme(t *testing.T) {
	rec := doGetWithAuth(t, newAuthNRouter(&fakeVerifier{}, &fakeEnsurer{}), "Basic abc")

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestAuthN_InvalidToken 校验无效凭证返回 401。
func TestAuthN_InvalidToken(t *testing.T) {
	verifier := &fakeVerifier{err: ports.ErrInvalidToken}

	rec := doGetWithAuth(t, newAuthNRouter(verifier, &fakeEnsurer{}), "Bearer bad-token")

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "UNAUTHENTICATED")
}

// TestAuthN_IdentityProviderUnavailable 校验身份平台不可用返回 503，且不放行。
func TestAuthN_IdentityProviderUnavailable(t *testing.T) {
	verifier := &fakeVerifier{err: ports.ErrIdentityProviderUnavailable}

	rec := doGetWithAuth(t, newAuthNRouter(verifier, &fakeEnsurer{}), "Bearer token")

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "DEPENDENCY_UNAVAILABLE")
}

// TestAuthN_DisabledUser 校验已停用用户返回 403。
func TestAuthN_DisabledUser(t *testing.T) {
	u := activeUser(t)
	u.Disable()
	router := newAuthNRouter(
		&fakeVerifier{claims: &ports.Claims{Subject: u.ZitadelUserID}},
		&fakeEnsurer{err: appuser.ErrUserDisabled},
	)

	rec := doGetWithAuth(t, router, "Bearer token")

	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "USER_DISABLED")
}

// TestAuthN_RepositoryUnavailable 校验用户仓储不可用返回 503（fail closed）。
func TestAuthN_RepositoryUnavailable(t *testing.T) {
	router := newAuthNRouter(
		&fakeVerifier{claims: &ports.Claims{Subject: "zitadel-001"}},
		&fakeEnsurer{err: errors.New("database down")},
	)

	rec := doGetWithAuth(t, router, "Bearer token")

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "DEPENDENCY_UNAVAILABLE")
}

// TestAuthN_Success 校验合法凭证可以访问受保护端点，且身份来自 Token 而非请求头。
func TestAuthN_Success(t *testing.T) {
	u := activeUser(t)
	verifier := &fakeVerifier{claims: &ports.Claims{Subject: u.ZitadelUserID}}
	router := newAuthNRouter(verifier, &fakeEnsurer{u: u})

	rec := doGetWithAuth(t, router, "Bearer valid-token")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "valid-token", verifier.seenToken)
	assert.Contains(t, rec.Body.String(), u.ID.String())
	assert.NotContains(t, rec.Body.String(), "attacker-id", "伪造的 X-User-ID 不得影响身份")
}

// TestAuthN_RequestIDConsistent 校验失败响应中的 request_id 与响应头一致。
func TestAuthN_RequestIDConsistent(t *testing.T) {
	router := newAuthNRouter(&fakeVerifier{err: ports.ErrInvalidToken}, &fakeEnsurer{})

	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	req.Header.Set("Authorization", "Bearer bad")
	req.Header.Set(responses.HeaderRequestID, "req-auth-1")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, "req-auth-1", rec.Header().Get(responses.HeaderRequestID))
	assert.Contains(t, rec.Body.String(), `"request_id":"req-auth-1"`)
}
