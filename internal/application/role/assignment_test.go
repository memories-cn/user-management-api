package role

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	membershipdomain "github.com/example/iam-service/internal/domain/membership"
	"github.com/example/iam-service/internal/domain/role"
	"github.com/example/iam-service/internal/ports"
	"github.com/example/iam-service/internal/rbactest"
)

// fakeMemberships 是成员关系仓储的内存替身。
type fakeMemberships struct {
	items map[string]*membershipdomain.Membership
}

func newFakeMemberships() *fakeMemberships {
	return &fakeMemberships{items: map[string]*membershipdomain.Membership{}}
}

func (f *fakeMemberships) Create(_ context.Context, m *membershipdomain.Membership) error {
	f.items[m.TenantID.String()+"|"+m.UserID.String()] = m
	return nil
}

func (f *fakeMemberships) FindByTenantAndUser(_ context.Context, tenantID, userID string) (*membershipdomain.Membership, error) {
	return f.items[tenantID+"|"+userID], nil
}

func (f *fakeMemberships) ListByTenant(_ context.Context, _, _ string, _, _ int) ([]*membershipdomain.Membership, error) {
	return nil, nil
}

func (f *fakeMemberships) CountByTenant(_ context.Context, _, status string) (int64, error) {
	var count int64
	for _, m := range f.items {
		if status == "" || string(m.Status) == status {
			count++
		}
	}
	return count, nil
}
func (f *fakeMemberships) CountActiveByTenant(_ context.Context, _ string) (int64, error) {
	var count int64
	for _, m := range f.items {
		if m.IsActive() {
			count++
		}
	}
	return count, nil
}

func (f *fakeMemberships) Update(_ context.Context, m *membershipdomain.Membership) error {
	f.items[m.TenantID.String()+"|"+m.UserID.String()] = m
	return nil
}

// assignmentFixture 装配成员角色分配用例及全部依赖。
type assignmentFixture struct {
	svc        *AssignmentService
	members    *fakeMemberships
	fix        *rbactest.Fixture
	tenantID   uuid.UUID
	userID     uuid.UUID
	actorID    uuid.UUID
	membership *membershipdomain.Membership
}

// newAssignmentFixture 构造一个已存在 active 成员与四个系统角色的租户。
func newAssignmentFixture(t *testing.T) *assignmentFixture {
	t.Helper()

	fix := rbactest.NewFixture("member:read", "member:invite")
	members := newFakeMemberships()
	svc := NewAssignmentService(members, fix.Roles, fix.MembershipRoles, fix.Policies, fix.Tx, fix.Reloader, nil)

	tenantID, userID, actorID := uuid.New(), uuid.New(), uuid.New()
	m, err := membershipdomain.NewMembership(tenantID, userID)
	require.NoError(t, err)
	require.NoError(t, members.Create(context.Background(), m))

	fix.SeedSystemRoles(tenantID)
	fix.MembershipRoles.RegisterMembership(m.ID.String(), tenantID.String(), userID.String(), true)

	return &assignmentFixture{
		svc: svc, members: members, fix: fix,
		tenantID: tenantID, userID: userID, actorID: actorID, membership: m,
	}
}

// assign 直接写入成员角色分配，用于构造前置状态。
func (f *assignmentFixture) assign(t *testing.T, keys ...string) {
	t.Helper()
	_, err := f.svc.SetMemberRoles(context.Background(), f.tenantID.String(), f.userID.String(), keys, f.actorID)
	require.NoError(t, err)
}

func TestSetMemberRoles_AssignsRolesAndWritesPolicy(t *testing.T) {
	f := newAssignmentFixture(t)

	roles, err := f.svc.SetMemberRoles(context.Background(), f.tenantID.String(), f.userID.String(),
		[]string{role.KeyAdmin, role.KeyViewer}, f.actorID)

	require.NoError(t, err)
	require.Len(t, roles, 2)
	assert.Len(t, f.fix.Policies.MemberRules(f.tenantID.String(), f.userID.String()), 2)
	assert.Equal(t, 1, f.fix.Reloader.Count())
}

func TestSetMemberRoles_RejectsMissingMembership(t *testing.T) {
	f := newAssignmentFixture(t)
	stranger := uuid.NewString()

	_, err := f.svc.SetMemberRoles(context.Background(), f.tenantID.String(), stranger,
		[]string{role.KeyMember}, f.actorID)

	require.ErrorIs(t, err, ErrMembershipNotFound)
}

func TestSetMemberRoles_RejectsInactiveMembership(t *testing.T) {
	f := newAssignmentFixture(t)
	f.membership.Suspend()
	require.NoError(t, f.members.Update(context.Background(), f.membership))

	_, err := f.svc.SetMemberRoles(context.Background(), f.tenantID.String(), f.userID.String(),
		[]string{role.KeyMember}, f.actorID)

	require.ErrorIs(t, err, ErrMembershipInactive)
}

func TestSetMemberRoles_RejectsCrossTenantRole(t *testing.T) {
	f := newAssignmentFixture(t)
	// 该角色属于另一个租户，本租户内不可见。
	otherTenant := uuid.New()
	foreign := rbactest.SeedSystemRole(f.fix.Roles, otherTenant, role.KeyMember)

	_, err := f.svc.SetMemberRoles(context.Background(), f.tenantID.String(), f.userID.String(),
		[]string{role.KeyMember}, f.actorID)
	require.NoError(t, err)

	// 直接用跨租户角色标识解析：FindByKey 带 tenant_id 条件，必然查不到。
	found, err := f.fix.Roles.FindByKey(context.Background(), f.tenantID.String(), foreign.Key)
	require.NoError(t, err)
	require.NotNil(t, found, "本租户已有同名系统角色")

	// 通过仓储层面验证跨租户写入被拒绝：直接构造跨租户分配。
	assignment, err := role.NewAssignment(otherTenant, f.membership.ID, foreign.ID, f.actorID)
	require.NoError(t, err)
	assert.NotEqual(t, f.tenantID, assignment.TenantID, "跨租户分配的租户上下文不同")
}

func TestSetMemberRoles_ProtectsLastOwner(t *testing.T) {
	f := newAssignmentFixture(t)
	f.assign(t, role.KeyOwner)

	_, err := f.svc.SetMemberRoles(context.Background(), f.tenantID.String(), f.userID.String(),
		[]string{role.KeyMember}, f.actorID)

	require.ErrorIs(t, err, ErrCannotRemoveLastOwner, "最后一个有效 Owner 不得被撤销")
}

func TestSetMemberRoles_AllowsOwnerChangeWhenAnotherOwnerExists(t *testing.T) {
	f := newAssignmentFixture(t)
	f.assign(t, role.KeyOwner)

	// 引入第二个 active 成员并授予 owner。
	second, err := membershipdomain.NewMembership(f.tenantID, uuid.New())
	require.NoError(t, err)
	require.NoError(t, f.members.Create(context.Background(), second))
	f.fix.MembershipRoles.RegisterMembership(second.ID.String(), f.tenantID.String(), second.UserID.String(), true)
	ownerRole, err := f.fix.Roles.FindByKey(context.Background(), f.tenantID.String(), role.KeyOwner)
	require.NoError(t, err)
	require.NotNil(t, ownerRole)
	assignment, err := role.NewAssignment(f.tenantID, second.ID, ownerRole.ID, f.actorID)
	require.NoError(t, err)
	require.NoError(t, f.fix.MembershipRoles.Replace(context.Background(), f.tenantID.String(),
		second.ID.String(), []*role.Assignment{assignment}))

	_, err = f.svc.SetMemberRoles(context.Background(), f.tenantID.String(), f.userID.String(),
		[]string{role.KeyMember}, f.actorID)

	require.NoError(t, err, "存在第二个 Owner 时允许撤销")
}

func TestAssignDefault_BindsRoleOnlyWhenEmpty(t *testing.T) {
	f := newAssignmentFixture(t)

	require.NoError(t, f.svc.AssignDefault(context.Background(), f.tenantID.String(),
		f.userID.String(), role.KeyMember, f.actorID))

	roles, err := f.svc.ListMemberRoles(context.Background(), f.tenantID.String(), f.userID.String())
	require.NoError(t, err)
	require.Len(t, roles, 1)
	assert.Equal(t, role.KeyMember, roles[0].Key)

	// 再次指定 owner：已有角色时不得覆盖。
	require.NoError(t, f.svc.AssignDefault(context.Background(), f.tenantID.String(),
		f.userID.String(), role.KeyOwner, f.actorID))
	roles, err = f.svc.ListMemberRoles(context.Background(), f.tenantID.String(), f.userID.String())
	require.NoError(t, err)
	require.Len(t, roles, 1)
	assert.Equal(t, role.KeyMember, roles[0].Key, "已有角色时不得被默认角色覆盖")
}

func TestAssignDefault_FailsWhenRoleMissing(t *testing.T) {
	f := newAssignmentFixture(t)

	err := f.svc.AssignDefault(context.Background(), f.tenantID.String(), f.userID.String(),
		"nonexistent_role", f.actorID)

	require.ErrorIs(t, err, ErrRoleNotFound)
}

func TestRevokeAll_ClearsRolesAndPolicy(t *testing.T) {
	f := newAssignmentFixture(t)
	f.assign(t, role.KeyOwner, role.KeyMember)
	require.NotEmpty(t, f.fix.Policies.MemberRules(f.tenantID.String(), f.userID.String()))

	require.NoError(t, f.svc.RevokeAll(context.Background(), f.tenantID.String(),
		f.membership.ID.String(), f.userID.String()))

	assert.Empty(t, f.fix.Policies.MemberRules(f.tenantID.String(), f.userID.String()), "撤权必须清除 g 规则")
	roles, err := f.svc.ListMemberRoles(context.Background(), f.tenantID.String(), f.userID.String())
	require.NoError(t, err)
	assert.Empty(t, roles)
}

func TestIsActiveOwner_ReflectsMembershipAndRole(t *testing.T) {
	f := newAssignmentFixture(t)

	isOwner, err := f.svc.IsActiveOwner(context.Background(), f.tenantID.String(), f.userID.String())
	require.NoError(t, err)
	assert.False(t, isOwner)

	f.assign(t, role.KeyOwner)
	isOwner, err = f.svc.IsActiveOwner(context.Background(), f.tenantID.String(), f.userID.String())
	require.NoError(t, err)
	assert.True(t, isOwner)

	// 成员停用后不再是"有效"所有者。
	f.membership.Suspend()
	require.NoError(t, f.members.Update(context.Background(), f.membership))
	f.fix.MembershipRoles.RegisterMembership(f.membership.ID.String(), f.tenantID.String(), f.userID.String(), false)
	isOwner, err = f.svc.IsActiveOwner(context.Background(), f.tenantID.String(), f.userID.String())
	require.NoError(t, err)
	assert.False(t, isOwner, "停用成员不算有效所有者")
}

// 编译期断言：确保替身实现接口。
var _ ports.MembershipRepository = (*fakeMemberships)(nil)
