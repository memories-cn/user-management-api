package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/example/iam-service/internal/bootstrap"
	"github.com/example/iam-service/internal/config"
	"github.com/example/iam-service/internal/oidctest"
	"github.com/example/iam-service/internal/ports"
	"github.com/example/iam-service/internal/transport/http/handlers"
	"github.com/example/iam-service/internal/transport/http/middleware"
	"github.com/example/iam-service/internal/transport/http/responses"
)

// 集成测试使用的受众与身份标识。
const (
	integrationAudience = "iam-api-audience"
	integrationSubject  = "zitadel-001"
)

// newIdentityStack 在真实数据库与 OIDC 桩之上装配认证链路，并返回可直接请求的路由。
func newIdentityStack(t *testing.T) (*gin.Engine, *oidctest.Stub, *gorm.DB) {
	t.Helper()

	dsn := newDatabase(t)
	require.NoError(t, migrateUp(dsn))

	db, err := bootstrap.OpenDB(configFor(dsn))
	require.NoError(t, err)
	t.Cleanup(func() { closeGorm(t, db) })

	stub := oidctest.New(t)
	cfg := config.Config{
		DatabaseDSN:        dsn,
		ZitadelIssuer:      stub.Issuer(),
		ZitadelAPIAudience: integrationAudience,
	}
	verifier, err := bootstrap.NewTokenVerifier(context.Background(), cfg)
	require.NoError(t, err)

	repo := bootstrap.NewUserRepository(db)
	sync := bootstrap.NewUserSync(repo)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.RequestID(), middleware.AuthN(verifier, sync))
	r.GET("/api/v1/me", handlers.NewMeHandler().Me)

	return r, stub, db
}

// doRequest 向指定路径发起 GET 请求，headers 为可选的额外请求头。
func doRequest(t *testing.T, router *gin.Engine, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// TestUserRepository_UniqueConstraint 校验唯一约束由数据库保证。
func TestUserRepository_UniqueConstraint(t *testing.T) {
	dsn := newDatabase(t)
	require.NoError(t, migrateUp(dsn))

	db, err := bootstrap.OpenDB(configFor(dsn))
	require.NoError(t, err)
	defer closeGorm(t, db)
	repo := bootstrap.NewUserRepository(db)

	u, err := bootstrap.NewUserSync(repo).Ensure(context.Background(),
		&ports.Claims{Subject: integrationSubject})
	require.NoError(t, err)

	// 绕过同步逻辑直接重复插入，验证数据库层约束而非应用层判断。
	dup := *u
	err = repo.Create(context.Background(), &dup)

	require.Error(t, err, "同一 zitadel_user_id 必须被数据库唯一约束拒绝")
}

// TestUserSync_PersistsAndUpdates 校验用户被持久化且展示资料按需更新。
func TestUserSync_PersistsAndUpdates(t *testing.T) {
	dsn := newDatabase(t)
	require.NoError(t, migrateUp(dsn))

	db, err := bootstrap.OpenDB(configFor(dsn))
	require.NoError(t, err)
	defer closeGorm(t, db)

	repo := bootstrap.NewUserRepository(db)
	sync := bootstrap.NewUserSync(repo)
	ctx := context.Background()

	created, err := sync.Ensure(ctx, &ports.Claims{Subject: integrationSubject, DisplayName: "Alice"})
	require.NoError(t, err)

	stored, err := repo.FindByZitadelUserID(ctx, integrationSubject)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, created.ID, stored.ID)
	assert.Equal(t, "Alice", stored.DisplayName)

	// 重复同步不得创建新用户。
	again, err := sync.Ensure(ctx, &ports.Claims{Subject: integrationSubject, DisplayName: "Alice"})
	require.NoError(t, err)
	assert.Equal(t, created.ID, again.ID)

	// 展示资料变化时更新。
	_, err = sync.Ensure(ctx, &ports.Claims{Subject: integrationSubject, DisplayName: "Bob"})
	require.NoError(t, err)

	updated, err := repo.FindByZitadelUserID(ctx, integrationSubject)
	require.NoError(t, err)
	assert.Equal(t, "Bob", updated.DisplayName)
}

// TestAPI_Me_ValidToken 校验有效令牌可以访问受保护端点并创建本地用户映射。
func TestAPI_Me_ValidToken(t *testing.T) {
	router, stub, db := newIdentityStack(t)

	rec := doRequest(t, router, "/api/v1/me", map[string]string{
		"Authorization": "Bearer " + stub.Sign(t, stub.DefaultSpec(integrationAudience)),
	})

	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Data struct {
			UserID        string `json:"user_id"`
			ZitadelUserID string `json:"zitadel_user_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, integrationSubject, body.Data.ZitadelUserID)
	assert.NotEmpty(t, body.Data.UserID)

	// 访问后本地用户映射应已创建。
	stored, err := bootstrap.NewUserRepository(db).FindByZitadelUserID(context.Background(), integrationSubject)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, body.Data.UserID, stored.ID.String())
}

// TestAPI_Me_RepeatedAccessDoesNotDuplicate 校验同一用户重复访问不会重复创建。
func TestAPI_Me_RepeatedAccessDoesNotDuplicate(t *testing.T) {
	router, stub, db := newIdentityStack(t)

	for i := 0; i < 3; i++ {
		rec := doRequest(t, router, "/api/v1/me", map[string]string{
			"Authorization": "Bearer " + stub.Sign(t, stub.DefaultSpec(integrationAudience)),
		})
		require.Equal(t, http.StatusOK, rec.Code)
	}

	var count int64
	err := db.WithContext(context.Background()).
		Table("users").
		Where("zitadel_user_id = ?", integrationSubject).
		Count(&count).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "重复访问不得产生重复用户")
}

// TestAPI_Me_RejectsForgedIdentityHeaders 校验伪造身份头无效，身份只来自令牌。
func TestAPI_Me_RejectsForgedIdentityHeaders(t *testing.T) {
	router, stub, _ := newIdentityStack(t)

	rec := doRequest(t, router, "/api/v1/me", map[string]string{
		"Authorization": "Bearer " + stub.Sign(t, stub.DefaultSpec(integrationAudience)),
		"X-User-ID":     "attacker-id",
	})

	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "attacker-id")
	assert.Contains(t, rec.Body.String(), integrationSubject)
}

// TestAPI_Me_RejectsTamperedToken 校验签名被篡改的令牌返回 401。
func TestAPI_Me_RejectsTamperedToken(t *testing.T) {
	router, stub, _ := newIdentityStack(t)

	valid := stub.Sign(t, stub.DefaultSpec(integrationAudience))
	tampered := valid[:strings.LastIndex(valid, ".")+1] + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	rec := doRequest(t, router, "/api/v1/me", map[string]string{"Authorization": "Bearer " + tampered})

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "UNAUTHENTICATED")
}

// TestAPI_Me_RejectsMissingToken 校验缺少凭证返回 401。
func TestAPI_Me_RejectsMissingToken(t *testing.T) {
	router, _, _ := newIdentityStack(t)

	rec := doRequest(t, router, "/api/v1/me", nil)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestAPI_Me_RejectsWrongAudience 校验受众不符返回 401。
func TestAPI_Me_RejectsWrongAudience(t *testing.T) {
	router, stub, _ := newIdentityStack(t)

	spec := stub.DefaultSpec("other-api")

	rec := doRequest(t, router, "/api/v1/me", map[string]string{
		"Authorization": "Bearer " + stub.Sign(t, spec),
	})

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestAPI_Me_DisabledUserForbidden 校验已停用用户返回 403。
func TestAPI_Me_DisabledUserForbidden(t *testing.T) {
	router, stub, db := newIdentityStack(t)
	ctx := context.Background()
	repo := bootstrap.NewUserRepository(db)

	// 先通过一次合法访问创建用户。
	rec := doRequest(t, router, "/api/v1/me", map[string]string{
		"Authorization": "Bearer " + stub.Sign(t, stub.DefaultSpec(integrationAudience)),
	})
	require.Equal(t, http.StatusOK, rec.Code)

	stored, err := repo.FindByZitadelUserID(ctx, integrationSubject)
	require.NoError(t, err)
	stored.Disable()
	require.NoError(t, repo.Update(ctx, stored))

	rec = doRequest(t, router, "/api/v1/me", map[string]string{
		"Authorization": "Bearer " + stub.Sign(t, stub.DefaultSpec(integrationAudience)),
	})

	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "USER_DISABLED")
}

// TestAPI_Me_DependencyUnavailable 校验 JWKS 不可用时返回 503，绝不放行。
func TestAPI_Me_DependencyUnavailable(t *testing.T) {
	router, stub, _ := newIdentityStack(t)
	stub.BreakJWKS()

	rec := doRequest(t, router, "/api/v1/me", map[string]string{
		"Authorization": "Bearer " + stub.Sign(t, stub.DefaultSpec(integrationAudience)),
	})

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "DEPENDENCY_UNAVAILABLE")
}

// TestAPI_Me_RequestIDConsistent 校验失败响应中的 request_id 与响应头一致。
func TestAPI_Me_RequestIDConsistent(t *testing.T) {
	router, _, _ := newIdentityStack(t)

	rec := doRequest(t, router, "/api/v1/me", map[string]string{
		responses.HeaderRequestID: "req-integration-1",
	})

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, "req-integration-1", rec.Header().Get(responses.HeaderRequestID))
	assert.Contains(t, rec.Body.String(), `"request_id":"req-integration-1"`)
}
