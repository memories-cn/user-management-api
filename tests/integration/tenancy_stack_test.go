package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	appaudit "github.com/example/iam-service/internal/application/audit"
	"github.com/example/iam-service/internal/bootstrap"
	"github.com/example/iam-service/internal/config"
	tenantdomain "github.com/example/iam-service/internal/domain/tenant"
	"github.com/example/iam-service/internal/oidctest"
	iamhttp "github.com/example/iam-service/internal/transport/http"
)

// 集成测试常量。
const (
	tenancyAudience      = "iam-api-audience"
	platformAdminRole    = "platform_admin"
	tenancyModelPath     = "../../casbin/model.conf"
	tenancyInvitationTTL = 7 * 24 * time.Hour
	// tenancyPolicyRefresh 是策略失效检查间隔，集成测试用短间隔减少等待。
	tenancyPolicyRefresh = 50 * time.Millisecond
)

// tenancyStack 是在真实 PostgreSQL 与 OIDC 桩之上装配的完整服务栈。
type tenancyStack struct {
	t        *testing.T
	router   *gin.Engine
	stub     *oidctest.Stub
	db       *gorm.DB
	app      *bootstrap.App
	audience string
}

// newTenancyStack 装配与生产一致的路由，用于端到端验证。
//
// 装配顺序与 bootstrap.New 一致：策略仓储 → 授权器 → 角色用例 → 租户域用例，
// 保证集成测试覆盖的依赖图与生产完全相同。
func newTenancyStack(t *testing.T) *tenancyStack {
	t.Helper()

	dsn := newDatabase(t)
	require.NoError(t, migrateUp(dsn))

	db, err := bootstrap.OpenDB(configFor(dsn))
	require.NoError(t, err)
	t.Cleanup(func() { closeGorm(t, db) })

	enforcer, err := bootstrap.NewEnforcer(db, tenancyModelPath)
	require.NoError(t, err)

	stub := oidctest.New(t)
	cfg := config.Config{
		DatabaseDSN:           dsn,
		ZitadelIssuer:         stub.Issuer(),
		ZitadelAPIAudience:    tenancyAudience,
		PlatformAdminRoleKey:  platformAdminRole,
		InvitationTTL:         tenancyInvitationTTL,
		PolicyRefreshInterval: tenancyPolicyRefresh,
		DefaultMemberRoleKey:  "member",
	}

	verifier, err := bootstrap.NewTokenVerifier(context.Background(), cfg)
	require.NoError(t, err)

	policy, err := bootstrap.NewPlatformAdminPolicy(cfg)
	require.NoError(t, err)

	users := bootstrap.NewUserRepository(db)
	tenants := bootstrap.NewTenantRepository(db)
	memberships := bootstrap.NewMembershipRepository(db)
	invitations := bootstrap.NewInvitationRepository(db)
	permissions := bootstrap.NewPermissionRepository(db)
	roles := bootstrap.NewRoleRepository(db)
	membershipRoles := bootstrap.NewMembershipRoleRepository(db)
	policies := bootstrap.NewPolicyRepository(db)
	audits := bootstrap.NewAuditRepository(db)
	tx := bootstrap.NewTxManager(db)

	// 审计用例必须先于业务用例装配，顺序与 bootstrap.New 保持一致。
	auditSvc := appaudit.NewService(audits)

	authorizer, err := bootstrap.NewAuthorizer(enforcer, policies, cfg.PolicyRefreshInterval)
	require.NoError(t, err)

	roleSvc := bootstrap.NewRoleService(roles, permissions, policies, tx, authorizer, auditSvc)
	assignmentSvc := bootstrap.NewAssignmentService(
		memberships, roles, membershipRoles, policies, tx, authorizer, auditSvc)

	services := bootstrap.NewTenancyServices(
		tenants, memberships, invitations, membershipRoles, policies,
		assignmentSvc, roleSvc, tx, authorizer, auditSvc, cfg)

	app := &bootstrap.App{
		DB:                db,
		Enforcer:          enforcer,
		Authorizer:        authorizer,
		Verifier:          verifier,
		Users:             users,
		UserSync:          bootstrap.NewUserSync(users),
		Tx:                tx,
		Tenants:           tenants,
		Memberships:       memberships,
		Invitations:       invitations,
		Permissions:       permissions,
		Roles:             roles,
		MembershipRoles:   membershipRoles,
		Policies:          policies,
		Audits:            audits,
		AuditService:      auditSvc,
		TenantCreator:     services.TenantCreator,
		TenantStatus:      bootstrap.NewTenantStatusService(tenants, tx, auditSvc),
		MemberService:     services.MemberService,
		InvitationService: services.InvitationService,
		Access:            services.Access,
		RoleService:       roleSvc,
		AssignmentService: assignmentSvc,
		Authorization:     bootstrap.NewAuthorizationService(services.Access, authorizer),
		PlatformAdmin:     policy,
	}

	return &tenancyStack{
		t:        t,
		router:   iamhttp.NewRouter(app),
		stub:     stub,
		db:       db,
		app:      app,
		audience: tenancyAudience,
	}
}

// token 签发指定身份的令牌。
func (s *tenancyStack) token(subject, email string, verified bool, roles []string) string {
	s.t.Helper()

	spec := s.stub.DefaultSpec(s.audience)
	spec.Subject = subject
	spec.Email = email
	spec.EmailVerified = verified
	spec.Roles = roles
	return s.stub.Sign(s.t, spec)
}

// adminToken 签发平台管理员令牌。
func (s *tenancyStack) adminToken(subject string) string {
	return s.token(subject, "admin@example.com", true, []string{platformAdminRole})
}

// userToken 签发普通用户令牌。
func (s *tenancyStack) userToken(subject, email string) string {
	return s.token(subject, email, true, nil)
}

// do 发起 HTTP 请求，token 为空表示不携带凭证。
func (s *tenancyStack) do(method, path, token string, body any) *httptest.ResponseRecorder {
	s.t.Helper()

	req := newJSONRequest(s.t, method, path, body)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	return rec
}

// createTenantResponse 是创建租户接口的响应体。
type createTenantResponse struct {
	Data struct {
		Tenant struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Slug   string `json:"slug"`
			Status string `json:"status"`
		} `json:"tenant"`
		Invitation struct {
			ID        string `json:"id"`
			Email     string `json:"email"`
			Status    string `json:"status"`
			ExpiresAt string `json:"expires_at"`
		} `json:"invitation"`
		Token string `json:"token"`
	} `json:"data"`
}

// createTenant 以平台管理员身份创建租户并返回响应。
func (s *tenancyStack) createTenant(adminSubject, name, slug, ownerEmail string) (*httptest.ResponseRecorder, createTenantResponse) {
	s.t.Helper()

	rec := s.do(http.MethodPost, "/api/v1/platform/tenants", s.adminToken(adminSubject), map[string]string{
		"name":        name,
		"slug":        slug,
		"owner_email": ownerEmail,
	})

	var parsed createTenantResponse
	if rec.Code == http.StatusOK {
		require.NoError(s.t, json.Unmarshal(rec.Body.Bytes(), &parsed))
	}
	return rec, parsed
}

// acceptInvitation 以指定身份接受邀请。
func (s *tenancyStack) acceptInvitation(invitationID, userSubject, email string, verified bool, token string) *httptest.ResponseRecorder {
	s.t.Helper()

	return s.do(http.MethodPost, "/api/v1/invitations/"+invitationID+"/accept",
		s.token(userSubject, email, verified, nil), map[string]string{"token": token})
}

// suspendTenant 直接把租户置为停用状态，模拟平台管理操作。
func (s *tenancyStack) suspendTenant(tenantID string) {
	s.t.Helper()

	err := s.db.WithContext(context.Background()).
		Exec("UPDATE tenants SET status = ?, updated_at = now() WHERE id = ?",
			string(tenantdomain.StatusSuspended), tenantID).Error
	require.NoError(s.t, err)
}

// newJSONRequest 构造带 JSON 请求体的测试请求。
func newJSONRequest(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(raw)
	}

	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	return req
}
