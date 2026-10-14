package tenant

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	appinvitation "github.com/example/iam-service/internal/application/invitation"
	approle "github.com/example/iam-service/internal/application/role"
	invitationdomain "github.com/example/iam-service/internal/domain/invitation"
	membershipdomain "github.com/example/iam-service/internal/domain/membership"
	domainrole "github.com/example/iam-service/internal/domain/role"
	tenantdomain "github.com/example/iam-service/internal/domain/tenant"
	"github.com/example/iam-service/internal/ports"
	"github.com/example/iam-service/internal/rbactest"
)

// fakeTenants 是内存实现的租户仓储，支持回滚模拟。
type fakeTenants struct {
	items      map[uuid.UUID]*tenantdomain.Tenant
	createErr  error
	rollbackFn func()
}

func newFakeTenants() *fakeTenants {
	return &fakeTenants{items: map[uuid.UUID]*tenantdomain.Tenant{}}
}

func (f *fakeTenants) Create(_ context.Context, t *tenantdomain.Tenant) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.items[t.ID] = t
	return nil
}

func (f *fakeTenants) FindByID(_ context.Context, id string) (*tenantdomain.Tenant, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return nil, err
	}
	return f.items[parsed], nil
}

func (f *fakeTenants) FindBySlug(_ context.Context, slug string) (*tenantdomain.Tenant, error) {
	normalized := tenantdomain.NormalizeSlug(slug)
	for _, t := range f.items {
		if t.Slug == normalized {
			return t, nil
		}
	}
	return nil, nil
}

func (f *fakeTenants) ListByMemberUserID(context.Context, string) ([]*tenantdomain.Tenant, error) {
	return nil, nil
}

func (f *fakeTenants) CountByCreator(context.Context, string) (int64, error) {
	return int64(len(f.items)), nil
}

func (f *fakeTenants) Update(_ context.Context, t *tenantdomain.Tenant) error {
	f.items[t.ID] = t
	return nil
}

// rollbackTx 在回调返回错误时执行回滚钩子，用于验证事务语义。
type rollbackTx struct{ rollback func() }

func (r rollbackTx) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if err := fn(ctx); err != nil {
		if r.rollback != nil {
			r.rollback()
		}
		return err
	}
	return nil
}

// fakeInvitations 是内存实现的邀请仓储。
type fakeInvitations struct {
	items     map[uuid.UUID]*invitationdomain.Invitation
	createErr error
}

func newFakeInvitations() *fakeInvitations {
	return &fakeInvitations{items: map[uuid.UUID]*invitationdomain.Invitation{}}
}

func (f *fakeInvitations) Create(_ context.Context, inv *invitationdomain.Invitation) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.items[inv.ID] = inv
	return nil
}

func (f *fakeInvitations) FindByID(_ context.Context, id string) (*invitationdomain.Invitation, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return nil, err
	}
	return f.items[parsed], nil
}

func (f *fakeInvitations) FindPendingByTenantAndEmail(_ context.Context, tenantID, email string) (*invitationdomain.Invitation, error) {
	for _, inv := range f.items {
		if inv.TenantID.String() == tenantID &&
			inv.Email == invitationdomain.NormalizeEmail(email) &&
			inv.Status == invitationdomain.StatusPending {
			return inv, nil
		}
	}
	return nil, nil
}

func (f *fakeInvitations) ListByTenant(_ context.Context, tenantID, _ string, _ int, _ int) ([]*invitationdomain.Invitation, error) {
	var out []*invitationdomain.Invitation
	for _, inv := range f.items {
		if inv.TenantID.String() == tenantID {
			out = append(out, inv)
		}
	}
	return out, nil
}

func (f *fakeInvitations) ListPendingByEmail(context.Context, string) ([]*invitationdomain.Invitation, error) {
	return nil, nil
}

func (f *fakeInvitations) Update(_ context.Context, inv *invitationdomain.Invitation) error {
	f.items[inv.ID] = inv
	return nil
}

// fakeMemberships 记录是否创建过成员关系，用于验证职责分离。
type fakeMemberships struct {
	created int
}

func (f *fakeMemberships) Create(_ context.Context, _ *membershipdomain.Membership) error {
	f.created++
	return nil
}

func (f *fakeMemberships) FindByTenantAndUser(context.Context, string, string) (*membershipdomain.Membership, error) {
	return nil, nil
}

func (f *fakeMemberships) ListByTenant(context.Context, string, string, int, int) ([]*membershipdomain.Membership, error) {
	return nil, nil
}

func (f *fakeMemberships) CountActiveByTenant(context.Context, string) (int64, error) {
	return 0, nil
}

func (f *fakeMemberships) Update(context.Context, *membershipdomain.Membership) error {
	return nil
}

// 编译期断言：保证替身实现接口。
var (
	_ ports.TenantRepository     = (*fakeTenants)(nil)
	_ ports.InvitationRepository = (*fakeInvitations)(nil)
	_ ports.MembershipRepository = (*fakeMemberships)(nil)
)

// newTestService 装配创建租户用例及其依赖替身。
//
// 授权域依赖使用真实用例 + 内存仓储替身（rbactest），以便断言
// "创建租户时预置系统角色与策略投影"确实发生。
func newTestService() (*CreateService, *fakeTenants, *fakeInvitations, *fakeMemberships, *rbactest.Fixture) {
	tenants := newFakeTenants()
	invitations := newFakeInvitations()
	memberships := &fakeMemberships{}

	fix := rbactest.NewFixture(
		"tenant:read", "tenant:update", "tenant:delete",
		"member:read", "member:invite", "member:update", "member:remove",
		"role:read", "role:create", "role:update", "role:delete", "role:assign",
		"permission:read", "permission:assign", "audit:read",
	)
	assignments := approle.NewAssignmentService(
		memberships, fix.Roles, fix.MembershipRoles, fix.Policies, fix.Tx, fix.Reloader, nil)
	roleSvc := approle.NewService(fix.Roles, fix.Permissions, fix.Policies, fix.Tx, fix.Reloader, nil)

	invitationSvc := appinvitation.NewService(
		invitations, memberships, rollbackTx{}, assignments, domainrole.KeyMember, 7*24*time.Hour, nil)
	svc := NewCreateService(tenants, fix.Policies, rollbackTx{}, invitationSvc, roleSvc, fix.Reloader, nil)
	return svc, tenants, invitations, memberships, fix
}

func TestCreate_CreatesTenantAndOwnerInvitation(t *testing.T) {
	svc, tenants, invitations, memberships, _ := newTestService()
	actor := uuid.New()

	result, err := svc.Create(context.Background(), actor, "Acme", "acme", "Owner@Example.com")

	require.NoError(t, err)
	assert.Equal(t, "acme", result.Tenant.Slug)
	assert.Equal(t, actor, result.Tenant.CreatedBy)
	assert.Equal(t, invitationdomain.StatusPending, result.Invitation.Status)
	assert.Equal(t, "owner@example.com", result.Invitation.Email)
	assert.NotEmpty(t, result.Token)
	assert.False(t, result.Reused)

	assert.Len(t, tenants.items, 1)
	assert.Len(t, invitations.items, 1)
	assert.Equal(t, 0, memberships.created, "平台管理员不得自动成为租户成员")
}

func TestCreate_RejectsDuplicateSlug(t *testing.T) {
	svc, tenants, _, _, _ := newTestService()
	actor := uuid.New()
	ctx := context.Background()

	_, err := svc.Create(ctx, actor, "Acme", "acme", "owner@example.com")
	require.NoError(t, err)

	_, err = svc.Create(ctx, actor, "Acme 2", "ACME", "owner2@example.com")

	require.ErrorIs(t, err, ErrTenantSlugConflict, "slug 冲突判定必须大小写不敏感")
	assert.Len(t, tenants.items, 1)
}

func TestCreate_RejectsInvalidSlug(t *testing.T) {
	svc, tenants, _, _, _ := newTestService()

	_, err := svc.Create(context.Background(), uuid.New(), "Acme", "Acme_1", "owner@example.com")

	require.ErrorIs(t, err, tenantdomain.ErrInvalidSlug)
	assert.Empty(t, tenants.items)
}

func TestCreate_RequiresActor(t *testing.T) {
	svc, _, _, _, _ := newTestService()

	_, err := svc.Create(context.Background(), uuid.Nil, "Acme", "acme", "owner@example.com")

	require.Error(t, err)
}

func TestCreate_RollsBackWhenInvitationFails(t *testing.T) {
	tenants := newFakeTenants()
	invitations := newFakeInvitations()
	invitations.createErr = errors.New("invitation 创建失败")
	memberships := &fakeMemberships{}

	fix := rbactest.NewFixture()
	// 事务回滚：租户创建后邀请失败，租户不应留存。
	tx := rollbackTx{rollback: func() { tenants.items = map[uuid.UUID]*tenantdomain.Tenant{} }}
	assignments := approle.NewAssignmentService(
		memberships, fix.Roles, fix.MembershipRoles, fix.Policies, fix.Tx, fix.Reloader, nil)
	invitationSvc := appinvitation.NewService(
		invitations, memberships, tx, assignments, domainrole.KeyMember, 7*24*time.Hour, nil)
	roleSvc := approle.NewService(fix.Roles, fix.Permissions, fix.Policies, fix.Tx, fix.Reloader, nil)
	svc := NewCreateService(tenants, fix.Policies, tx, invitationSvc, roleSvc, fix.Reloader, nil)

	_, err := svc.Create(context.Background(), uuid.New(), "Acme", "acme", "owner@example.com")

	require.Error(t, err)
	assert.Empty(t, tenants.items, "邀请创建失败时租户必须回滚")
}

func TestCreate_RollsBackWhenTenantCreateFails(t *testing.T) {
	tenants := newFakeTenants()
	tenants.createErr = errors.New("tenant 创建失败")
	invitations := newFakeInvitations()
	memberships := &fakeMemberships{}

	fix := rbactest.NewFixture()
	tx := rollbackTx{}
	assignments := approle.NewAssignmentService(
		memberships, fix.Roles, fix.MembershipRoles, fix.Policies, fix.Tx, fix.Reloader, nil)
	invitationSvc := appinvitation.NewService(
		invitations, memberships, tx, assignments, domainrole.KeyMember, 7*24*time.Hour, nil)
	roleSvc := approle.NewService(fix.Roles, fix.Permissions, fix.Policies, fix.Tx, fix.Reloader, nil)
	svc := NewCreateService(tenants, fix.Policies, tx, invitationSvc, roleSvc, fix.Reloader, nil)

	_, err := svc.Create(context.Background(), uuid.New(), "Acme", "acme", "owner@example.com")

	require.Error(t, err)
	assert.Empty(t, invitations.items, "租户创建失败时不得留下邀请")
}

// CountByTenant 统计替身中的邀请数。
func (f *fakeInvitations) CountByTenant(_ context.Context, _, _ string) (int64, error) {
	return int64(len(f.items)), nil
}

// CountByTenant 返回固定的成员数，测试不依赖该计数。
func (f *fakeMemberships) CountByTenant(context.Context, string, string) (int64, error) {
	return 0, nil
}
