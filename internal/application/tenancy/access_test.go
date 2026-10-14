package tenancy

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/iam-service/internal/domain/membership"
	"github.com/example/iam-service/internal/domain/tenant"
)

// fakeTenants 是内存实现的租户仓储。
type fakeTenants struct{ items map[string]*tenant.Tenant }

func (f *fakeTenants) Create(_ context.Context, t *tenant.Tenant) error {
	f.items[t.ID.String()] = t
	return nil
}
func (f *fakeTenants) FindByID(_ context.Context, id string) (*tenant.Tenant, error) {
	return f.items[id], nil
}
func (f *fakeTenants) FindBySlug(_ context.Context, slug string) (*tenant.Tenant, error) {
	for _, t := range f.items {
		if t.Slug == slug {
			return t, nil
		}
	}
	return nil, nil
}
func (f *fakeTenants) ListByMemberUserID(context.Context, string) ([]*tenant.Tenant, error) {
	return nil, nil
}
func (f *fakeTenants) CountByCreator(context.Context, string) (int64, error) { return 0, nil }
func (f *fakeTenants) Update(_ context.Context, t *tenant.Tenant) error {
	f.items[t.ID.String()] = t
	return nil
}

// fakeMemberships 是内存实现的成员关系仓储。
type fakeMemberships struct {
	items map[string]*membership.Membership
}

func (f *fakeMemberships) Create(_ context.Context, m *membership.Membership) error {
	f.items[m.TenantID.String()+"|"+m.UserID.String()] = m
	return nil
}
func (f *fakeMemberships) FindByTenantAndUser(_ context.Context, tenantID, userID string) (*membership.Membership, error) {
	return f.items[tenantID+"|"+userID], nil
}
func (f *fakeMemberships) ListByTenant(context.Context, string, int, int) ([]*membership.Membership, error) {
	return nil, nil
}
func (f *fakeMemberships) CountActiveByTenant(context.Context, string) (int64, error) { return 0, nil }
func (f *fakeMemberships) Update(_ context.Context, m *membership.Membership) error {
	f.items[m.TenantID.String()+"|"+m.UserID.String()] = m
	return nil
}

// setup 构造一个 active 租户及其 active 成员。
func setup(t *testing.T) (*AccessService, *tenant.Tenant, string, string) {
	t.Helper()

	tn, err := tenant.NewTenant("Acme", "acme", uuid.New())
	require.NoError(t, err)
	userID := uuid.NewString()

	m, err := membership.NewMembership(tn.ID, uuid.MustParse(userID))
	require.NoError(t, err)

	tenants := &fakeTenants{items: map[string]*tenant.Tenant{tn.ID.String(): tn}}
	memberships := &fakeMemberships{items: map[string]*membership.Membership{
		tn.ID.String() + "|" + userID: m,
	}}
	return NewAccessService(tenants, memberships), tn, tn.ID.String(), userID
}

func TestCheck_AllowsActiveMember(t *testing.T) {
	svc, _, tenantID, userID := setup(t)

	require.NoError(t, svc.Check(context.Background(), userID, tenantID))
}

func TestCheck_RejectsUnknownTenant(t *testing.T) {
	svc, _, _, userID := setup(t)

	err := svc.Check(context.Background(), userID, uuid.NewString())

	require.ErrorIs(t, err, ErrTenantNotFound)
}

func TestCheck_RejectsNonMember(t *testing.T) {
	svc, _, tenantID, _ := setup(t)

	err := svc.Check(context.Background(), uuid.NewString(), tenantID)

	require.ErrorIs(t, err, ErrTenantNotFound, "非成员必须与租户不存在返回同一错误，避免枚举")
}

func TestCheck_RejectsSuspendedTenant(t *testing.T) {
	svc, tn, tenantID, userID := setup(t)
	tn.Suspend()

	err := svc.Check(context.Background(), userID, tenantID)

	require.ErrorIs(t, err, ErrTenantSuspended)
}

func TestCheck_RejectsInactiveMembership(t *testing.T) {
	svc, _, tenantID, userID := setup(t)

	// 字段是接口类型，需断言回测试替身后改写成员状态。
	svc.memberships.(*fakeMemberships).items[tenantID+"|"+userID].Remove()

	err := svc.Check(context.Background(), userID, tenantID)

	require.ErrorIs(t, err, ErrMembershipInactive)
}

func TestActiveTenant_ReturnsTenant(t *testing.T) {
	svc, tn, tenantID, _ := setup(t)

	got, err := svc.ActiveTenant(context.Background(), tenantID)

	require.NoError(t, err)
	assert.Equal(t, tn.ID, got.ID)
}

func TestActiveTenant_RejectsSuspended(t *testing.T) {
	svc, tn, tenantID, _ := setup(t)
	tn.Suspend()

	_, err := svc.ActiveTenant(context.Background(), tenantID)

	require.ErrorIs(t, err, ErrTenantNotFound)
}

func TestActiveMemberStatus(t *testing.T) {
	svc, _, tenantID, userID := setup(t)

	status, err := svc.ActiveMemberStatus(context.Background(), tenantID, userID)
	require.NoError(t, err)
	assert.Equal(t, membership.StatusActive, status)

	status, err = svc.ActiveMemberStatus(context.Background(), tenantID, uuid.NewString())
	require.NoError(t, err)
	assert.Empty(t, status, "未加入时返回空状态而不是报错")
}
