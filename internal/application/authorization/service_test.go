package authorization

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/iam-service/internal/application/tenancy"
	"github.com/example/iam-service/internal/domain/identity"
	"github.com/example/iam-service/internal/domain/membership"
	"github.com/example/iam-service/internal/domain/tenant"
	"github.com/example/iam-service/internal/ports"
)

// errDependency 模拟授权依赖不可用。
var errDependency = errors.New("授权依赖不可用")

// fakeAuthorizer 是授权引擎替身。
type fakeAuthorizer struct {
	allowed bool
	err     error
	// calls 记录最近一次判定的入参，便于断言判定链顺序。
	calls []string
}

func (f *fakeAuthorizer) Enforce(_ context.Context, subject, tenantID, resource, action string) (bool, error) {
	f.calls = []string{subject, tenantID, resource, action}
	if f.err != nil {
		return false, f.err
	}
	return f.allowed, nil
}

// fakeTenants 是租户仓储替身。
type fakeTenants struct {
	item *tenant.Tenant
	err  error
}

func (f *fakeTenants) Create(context.Context, *tenant.Tenant) error { return nil }
func (f *fakeTenants) FindByID(context.Context, string) (*tenant.Tenant, error) {
	return f.item, f.err
}
func (f *fakeTenants) FindBySlug(context.Context, string) (*tenant.Tenant, error) { return nil, nil }
func (f *fakeTenants) ListByMemberUserID(context.Context, string) ([]*tenant.Tenant, error) {
	return nil, nil
}
func (f *fakeTenants) CountByCreator(context.Context, string) (int64, error) { return 0, nil }
func (f *fakeTenants) Update(context.Context, *tenant.Tenant) error          { return nil }

// fakeMemberships 是成员关系仓储替身。
type fakeMemberships struct {
	item *membership.Membership
	err  error
}

func (f *fakeMemberships) Create(context.Context, *membership.Membership) error { return nil }
func (f *fakeMemberships) FindByTenantAndUser(context.Context, string, string) (*membership.Membership, error) {
	return f.item, f.err
}
func (f *fakeMemberships) ListByTenant(context.Context, string, string, int, int) ([]*membership.Membership, error) {
	return nil, nil
}
func (f *fakeMemberships) CountByTenant(context.Context, string, string) (int64, error) {
	return 0, nil
}
func (f *fakeMemberships) CountActiveByTenant(context.Context, string) (int64, error) { return 0, nil }
func (f *fakeMemberships) Update(context.Context, *membership.Membership) error       { return nil }

// newActiveAccess 构造一个"租户与成员均有效"的访问校验服务。
func newActiveAccess(t *testing.T) *tenancy.AccessService {
	t.Helper()

	tn, err := tenant.NewTenant("Acme", "acme", uuid.MustParse("00000000-0000-0000-0000-000000000001"))
	require.NoError(t, err)
	m, err := membership.NewMembership(tn.ID, uuid.MustParse("00000000-0000-0000-0000-000000000002"))
	require.NoError(t, err)

	return tenancy.NewAccessService(&fakeTenants{item: tn}, &fakeMemberships{item: m})
}

// 编译期断言：确保替身实现接口。
var (
	_ ports.TenantRepository     = (*fakeTenants)(nil)
	_ ports.MembershipRepository = (*fakeMemberships)(nil)
	_ ports.Authorizer           = (*fakeAuthorizer)(nil)
)

// principal 构造已认证身份。
func principal(userID string) identity.Principal {
	return identity.Principal{UserID: userID, Authenticated: true}
}

func TestAuthorize_AllowsWhenEnginePermits(t *testing.T) {
	authorizer := &fakeAuthorizer{allowed: true}
	svc := NewService(newActiveAccess(t), authorizer)

	decision, err := svc.Authorize(context.Background(), principal("user-1"), "tenant-1", "member", "invite")

	require.NoError(t, err)
	assert.True(t, decision.Allowed)
	assert.Equal(t, []string{"user-1", "tenant-1", "member", "invite"}, authorizer.calls)
}

func TestAuthorize_DeniesWithoutError(t *testing.T) {
	svc := NewService(newActiveAccess(t), &fakeAuthorizer{allowed: false})

	decision, err := svc.Authorize(context.Background(), principal("user-1"), "tenant-1", "member", "invite")

	require.NoError(t, err, "明确拒绝不得返回错误")
	assert.False(t, decision.Allowed)
}

func TestAuthorize_RejectsUnauthenticated(t *testing.T) {
	svc := NewService(newActiveAccess(t), &fakeAuthorizer{allowed: true})

	_, err := svc.Authorize(context.Background(), identity.Principal{UserID: "user-1"}, "tenant-1", "member", "read")

	require.ErrorIs(t, err, ports.ErrInvalidToken)
}

func TestAuthorize_DeniesWhenTenantOrMembershipInvalid(t *testing.T) {
	cases := map[string]*tenancy.AccessService{
		"租户不存在":        tenancy.NewAccessService(&fakeTenants{}, &fakeMemberships{}),
		"租户已停用":        tenancy.NewAccessService(suspendedTenant(t), &fakeMemberships{}),
		"成员关系非 active": tenancy.NewAccessService(activeTenant(t), inactiveMembership(t)),
	}
	for name, access := range cases {
		t.Run(name, func(t *testing.T) {
			authorizer := &fakeAuthorizer{allowed: true}
			svc := NewService(access, authorizer)

			decision, err := svc.Authorize(context.Background(), principal("user-1"), "tenant-1", "member", "read")

			require.NoError(t, err, "业务性拒绝必须表现为 allowed=false 而非错误")
			assert.False(t, decision.Allowed)
			assert.Empty(t, authorizer.calls, "未通过数据库校验时不得进入 Casbin 判定")
		})
	}
}

// suspendedTenant 返回状态为 suspended 的租户仓储替身。
func suspendedTenant(t *testing.T) *fakeTenants {
	t.Helper()
	tn, err := tenant.NewTenant("Acme", "acme", uuid.MustParse("00000000-0000-0000-0000-000000000001"))
	require.NoError(t, err)
	tn.Suspend()
	return &fakeTenants{item: tn}
}

// activeTenant 返回状态为 active 的租户仓储替身。
func activeTenant(t *testing.T) *fakeTenants {
	t.Helper()
	tn, err := tenant.NewTenant("Acme", "acme", uuid.MustParse("00000000-0000-0000-0000-000000000001"))
	require.NoError(t, err)
	return &fakeTenants{item: tn}
}

// inactiveMembership 返回非 active 的成员关系仓储替身。
func inactiveMembership(t *testing.T) *fakeMemberships {
	t.Helper()
	m, err := membership.NewMembership(uuid.MustParse("00000000-0000-0000-0000-000000000001"), uuid.MustParse("00000000-0000-0000-0000-000000000002"))
	require.NoError(t, err)
	m.Suspend()
	return &fakeMemberships{item: m}
}

func TestAuthorize_FailsClosedOnDatabaseError(t *testing.T) {
	// 数据库故障不是业务性拒绝，必须向上冒泡，由传输层返回 503。
	access := tenancy.NewAccessService(&fakeTenants{err: errDependency}, &fakeMemberships{})
	svc := NewService(access, &fakeAuthorizer{allowed: true})

	_, err := svc.Authorize(context.Background(), principal("user-1"), "tenant-1", "member", "read")

	require.ErrorIs(t, err, errDependency, "依赖故障不得被当成普通拒绝")
}

func TestAuthorize_FailsClosedOnEngineError(t *testing.T) {
	svc := NewService(newActiveAccess(t), &fakeAuthorizer{err: errDependency})

	decision, err := svc.Authorize(context.Background(), principal("user-1"), "tenant-1", "member", "invite")

	require.ErrorIs(t, err, errDependency, "引擎故障必须原样上抛")
	assert.Nil(t, decision)
}

func TestAuthorizeForSubject_RequiresSubject(t *testing.T) {
	svc := NewService(newActiveAccess(t), &fakeAuthorizer{allowed: true})

	_, err := svc.AuthorizeForSubject(context.Background(), "", "tenant-1", "member", "read")

	require.ErrorIs(t, err, ports.ErrInvalidToken)
}
