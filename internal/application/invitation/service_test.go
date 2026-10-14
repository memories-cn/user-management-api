package invitation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	approle "github.com/example/iam-service/internal/application/role"
	"github.com/example/iam-service/internal/domain/identity"
	invitationdomain "github.com/example/iam-service/internal/domain/invitation"
	membershipdomain "github.com/example/iam-service/internal/domain/membership"
	domainrole "github.com/example/iam-service/internal/domain/role"
	"github.com/example/iam-service/internal/ports"
	"github.com/example/iam-service/internal/rbactest"
)

const testTTL = 7 * 24 * time.Hour

// fakeInvitations 是内存实现的邀请仓储。
type fakeInvitations struct {
	items     map[uuid.UUID]*invitationdomain.Invitation
	createErr error
	updateErr error
	listErr   error
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

func (f *fakeInvitations) ListByTenant(_ context.Context, tenantID, _ string, _, _ int) ([]*invitationdomain.Invitation, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []*invitationdomain.Invitation
	for _, inv := range f.items {
		if inv.TenantID.String() == tenantID {
			out = append(out, inv)
		}
	}
	return out, nil
}

func (f *fakeInvitations) ListPendingByEmail(_ context.Context, email string) ([]*invitationdomain.Invitation, error) {
	var out []*invitationdomain.Invitation
	normalized := invitationdomain.NormalizeEmail(email)
	for _, inv := range f.items {
		if inv.Email == normalized && inv.Status == invitationdomain.StatusPending {
			out = append(out, inv)
		}
	}
	return out, nil
}

func (f *fakeInvitations) Update(_ context.Context, inv *invitationdomain.Invitation) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.items[inv.ID] = inv
	return nil
}

// fakeMemberships 是内存实现的成员关系仓储。
type fakeMemberships struct {
	items     map[string]*membershipdomain.Membership
	createErr error
}

func newFakeMemberships() *fakeMemberships {
	return &fakeMemberships{items: map[string]*membershipdomain.Membership{}}
}

func key(tenantID, userID string) string { return tenantID + "|" + userID }

func (f *fakeMemberships) Create(_ context.Context, m *membershipdomain.Membership) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.items[key(m.TenantID.String(), m.UserID.String())] = m
	return nil
}

func (f *fakeMemberships) FindByTenantAndUser(_ context.Context, tenantID, userID string) (*membershipdomain.Membership, error) {
	return f.items[key(tenantID, userID)], nil
}

func (f *fakeMemberships) ListByTenant(_ context.Context, tenantID, _ string, _, _ int) ([]*membershipdomain.Membership, error) {
	var out []*membershipdomain.Membership
	for k, m := range f.items {
		if len(k) > len(tenantID) && k[:len(tenantID)] == tenantID {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeMemberships) CountActiveByTenant(_ context.Context, tenantID string) (int64, error) {
	var count int64
	for k, m := range f.items {
		if len(k) > len(tenantID) && k[:len(tenantID)] == tenantID && m.IsActive() {
			count++
		}
	}
	return count, nil
}

func (f *fakeMemberships) Update(_ context.Context, m *membershipdomain.Membership) error {
	f.items[key(m.TenantID.String(), m.UserID.String())] = m
	return nil
}

// fakeTx 直接执行回调，模拟无外部事务环境。
type fakeTx struct{ err error }

func (f *fakeTx) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if f.err != nil {
		return f.err
	}
	return fn(ctx)
}

// newTestService 构造用例服务与依赖替身。
//
// 授权域依赖使用真实用例 + 内存仓储替身（rbactest），
// 既避免为接受邀请流程重复实现一整套 RBAC 假实现，也能断言角色绑定确实发生。
func newTestService() (*Service, *fakeInvitations, *fakeMemberships, *rbactest.Fixture) {
	invitations := newFakeInvitations()
	memberships := newFakeMemberships()

	fix := rbactest.NewFixture()
	assignments := approle.NewAssignmentService(
		memberships, fix.Roles, fix.MembershipRoles, fix.Policies, fix.Tx, fix.Reloader, nil)

	svc := NewService(invitations, memberships, &fakeTx{}, assignments, domainrole.KeyMember, testTTL, nil)
	return svc, invitations, memberships, fix
}

// principal 构造已认证且邮箱已验证的身份。
func principal(userID uuid.UUID, email string, verified bool) identity.Principal {
	return identity.Principal{
		Subject:       "zitadel-" + userID.String(),
		UserID:        userID.String(),
		Authenticated: true,
		Email:         email,
		EmailVerified: verified,
	}
}

func TestInvite_CreatesInvitation(t *testing.T) {
	svc, _, _, _ := newTestService()
	tenantID, actor := uuid.New(), uuid.New()

	result, err := svc.Invite(context.Background(), actor, tenantID, "Alice@Example.com")

	require.NoError(t, err)
	assert.False(t, result.Reused)
	assert.NotEmpty(t, result.Token)
	assert.Equal(t, "alice@example.com", result.Invitation.Email)
	assert.Equal(t, invitationdomain.StatusPending, result.Invitation.Status)
}

func TestInvite_IsIdempotent(t *testing.T) {
	svc, invitations, _, _ := newTestService()
	tenantID, actor := uuid.New(), uuid.New()
	ctx := context.Background()

	first, err := svc.Invite(ctx, actor, tenantID, "alice@example.com")
	require.NoError(t, err)

	// 内存替身返回的是同一指针，需先记录原始有效期再比对。
	firstExpiry := first.Invitation.ExpiresAt

	// 伪造一个"已经过一段时间"的时钟，验证复用时会刷新有效期。
	svc.now = func() time.Time { return time.Now().Add(3 * 24 * time.Hour) }
	second, err := svc.Invite(ctx, actor, tenantID, "alice@example.com")

	require.NoError(t, err)
	assert.True(t, second.Reused, "重复邀请必须复用而不是新建")
	assert.Empty(t, second.Token, "复用时不应重新下发令牌")
	assert.Equal(t, first.Invitation.ID, second.Invitation.ID)
	assert.True(t, second.Invitation.ExpiresAt.After(firstExpiry), "复用时必须刷新有效期")
	assert.Len(t, invitations.items, 1)
}

func TestInvite_RejectsAlreadyAccepted(t *testing.T) {
	svc, invitations, _, _ := newTestService()
	tenantID, actor := uuid.New(), uuid.New()
	ctx := context.Background()

	created, err := svc.Invite(ctx, actor, tenantID, "alice@example.com")
	require.NoError(t, err)
	created.Invitation.Status = invitationdomain.StatusAccepted
	require.NoError(t, invitations.Update(ctx, created.Invitation))

	_, err = svc.Invite(ctx, actor, tenantID, "alice@example.com")

	require.ErrorIs(t, err, ErrMemberAlreadyExists)
}

func TestAccept_BindsVerifiedIdentity(t *testing.T) {
	svc, _, memberships, fix := newTestService()
	tenantID, actor, userID := uuid.New(), uuid.New(), uuid.New()
	ctx := context.Background()
	fix.SeedSystemRoles(tenantID)

	invited, err := svc.Invite(ctx, actor, tenantID, "alice@example.com")
	require.NoError(t, err)

	m, err := svc.Accept(ctx, principal(userID, "alice@example.com", true), invited.Invitation.ID, invited.Token)

	require.NoError(t, err)
	assert.Equal(t, membershipdomain.StatusActive, m.Status)
	assert.Equal(t, tenantID, m.TenantID)
	assert.Len(t, memberships.items, 1)

	// 首个接受邀请的成员必须绑定为 owner，并写入 Casbin g 规则。
	fix.MembershipRoles.RegisterMembership(m.ID.String(), tenantID.String(), userID.String(), true)
	roles, err := fix.MembershipRoles.ListRoles(ctx, tenantID.String(), m.ID.String())
	require.NoError(t, err)
	require.Len(t, roles, 1)
	assert.Equal(t, domainrole.KeyOwner, roles[0].Key)
	assert.Len(t, fix.Policies.MemberRules(tenantID.String(), userID.String()), 1)
}

func TestAccept_RejectsUnverifiedEmail(t *testing.T) {
	svc, _, memberships, _ := newTestService()
	tenantID, actor, userID := uuid.New(), uuid.New(), uuid.New()
	ctx := context.Background()

	invited, err := svc.Invite(ctx, actor, tenantID, "alice@example.com")
	require.NoError(t, err)

	_, err = svc.Accept(ctx, principal(userID, "alice@example.com", false), invited.Invitation.ID, invited.Token)

	require.ErrorIs(t, err, ErrEmailNotVerified, "邮箱未验证不得绑定")
	assert.Empty(t, memberships.items)
}

func TestAccept_RejectsEmailMismatch(t *testing.T) {
	svc, _, memberships, _ := newTestService()
	tenantID, actor, userID := uuid.New(), uuid.New(), uuid.New()
	ctx := context.Background()

	invited, err := svc.Invite(ctx, actor, tenantID, "alice@example.com")
	require.NoError(t, err)

	_, err = svc.Accept(ctx, principal(userID, "bob@example.com", true), invited.Invitation.ID, invited.Token)

	require.ErrorIs(t, err, ErrInvitationEmailMismatch)
	assert.Empty(t, memberships.items)
}

func TestAccept_RejectsWrongToken(t *testing.T) {
	svc, _, memberships, _ := newTestService()
	tenantID, actor, userID := uuid.New(), uuid.New(), uuid.New()
	ctx := context.Background()

	invited, err := svc.Invite(ctx, actor, tenantID, "alice@example.com")
	require.NoError(t, err)

	_, err = svc.Accept(ctx, principal(userID, "alice@example.com", true), invited.Invitation.ID, "wrong-token")

	require.ErrorIs(t, err, ErrInvalidInvitationToken)
	assert.Empty(t, memberships.items)
}

func TestAccept_RejectsExpired(t *testing.T) {
	svc, _, memberships, _ := newTestService()
	tenantID, actor, userID := uuid.New(), uuid.New(), uuid.New()
	ctx := context.Background()

	invited, err := svc.Invite(ctx, actor, tenantID, "alice@example.com")
	require.NoError(t, err)

	// 时钟推进到有效期之后。
	svc.now = func() time.Time { return time.Now().Add(8 * 24 * time.Hour) }
	_, err = svc.Accept(ctx, principal(userID, "alice@example.com", true), invited.Invitation.ID, invited.Token)

	require.ErrorIs(t, err, invitationdomain.ErrExpired)
	assert.Empty(t, memberships.items)
	assert.Equal(t, invitationdomain.StatusExpired, invited.Invitation.Status, "过期状态应落库")
}

func TestAccept_RejectsCancelled(t *testing.T) {
	svc, _, _, _ := newTestService()
	tenantID, actor, userID := uuid.New(), uuid.New(), uuid.New()
	ctx := context.Background()

	invited, err := svc.Invite(ctx, actor, tenantID, "alice@example.com")
	require.NoError(t, err)
	require.NoError(t, svc.Cancel(ctx, tenantID, invited.Invitation.ID))

	_, err = svc.Accept(ctx, principal(userID, "alice@example.com", true), invited.Invitation.ID, invited.Token)

	require.ErrorIs(t, err, invitationdomain.ErrNotPending)
}

func TestAccept_IsIdempotent(t *testing.T) {
	svc, _, memberships, fix := newTestService()
	tenantID, actor, userID := uuid.New(), uuid.New(), uuid.New()
	ctx := context.Background()
	fix.SeedSystemRoles(tenantID)

	invited, err := svc.Invite(ctx, actor, tenantID, "alice@example.com")
	require.NoError(t, err)
	p := principal(userID, "alice@example.com", true)

	_, err = svc.Accept(ctx, p, invited.Invitation.ID, invited.Token)
	require.NoError(t, err)
	second, err := svc.Accept(ctx, p, invited.Invitation.ID, invited.Token)

	require.NoError(t, err, "重复接受必须幂等成功")
	assert.Equal(t, membershipdomain.StatusActive, second.Status)
	assert.Len(t, memberships.items, 1)
}

func TestAccept_RejoinsRemovedMember(t *testing.T) {
	svc, _, memberships, fix := newTestService()
	tenantID, actor, userID := uuid.New(), uuid.New(), uuid.New()
	ctx := context.Background()
	fix.SeedSystemRoles(tenantID)

	// 场景：邀请待接受，但该用户此前曾加入后又被移除（软删除保留行）。
	removed, err := membershipdomain.NewMembership(tenantID, userID)
	require.NoError(t, err)
	removed.Remove()
	require.NoError(t, memberships.Create(ctx, removed))

	invited, err := svc.Invite(ctx, actor, tenantID, "alice@example.com")
	require.NoError(t, err)

	rejoined, err := svc.Accept(ctx, principal(userID, "alice@example.com", true),
		invited.Invitation.ID, invited.Token)

	require.NoError(t, err)
	assert.Equal(t, membershipdomain.StatusActive, rejoined.Status)
	assert.Len(t, memberships.items, 1, "重新加入不得产生重复成员")
}

// 保留以下辅助函数用于需要重建邀请的场景。

func TestAccept_RejectsUnauthenticated(t *testing.T) {
	svc, _, _, _ := newTestService()
	tenantID, actor := uuid.New(), uuid.New()
	ctx := context.Background()

	invited, err := svc.Invite(ctx, actor, tenantID, "alice@example.com")
	require.NoError(t, err)

	_, err = svc.Accept(ctx, identity.Principal{UserID: uuid.NewString()}, invited.Invitation.ID, invited.Token)

	require.ErrorIs(t, err, ports.ErrInvalidToken)
}

func TestCancel_IsTenantScoped(t *testing.T) {
	svc, _, _, _ := newTestService()
	tenantID, otherTenant, actor := uuid.New(), uuid.New(), uuid.New()
	ctx := context.Background()

	invited, err := svc.Invite(ctx, actor, tenantID, "alice@example.com")
	require.NoError(t, err)

	err = svc.Cancel(ctx, otherTenant, invited.Invitation.ID)

	require.ErrorIs(t, err, ErrInvitationNotFound, "不得跨租户撤销邀请")
	require.NoError(t, svc.Cancel(ctx, tenantID, invited.Invitation.ID))
}

// newInvitationID 为已移除成员重新邀请并获取邀请 ID。
func newInvitationID(t *testing.T, svc *Service, tenantID, actor uuid.UUID, email string) uuid.UUID {
	t.Helper()
	result, err := svc.Invite(context.Background(), actor, tenantID, email)
	require.NoError(t, err)
	return result.Invitation.ID
}

// mustToken 返回指定邮箱邀请的明文令牌。
func mustToken(t *testing.T, svc *Service, tenantID, actor uuid.UUID, email string) string {
	t.Helper()
	result, err := svc.Invite(context.Background(), actor, tenantID, email)
	require.NoError(t, err)
	require.NotEmpty(t, result.Token)
	return result.Token
}

// 确保 errors 被使用。
var _ = errors.New

// CountByTenant 返回固定计数，测试不依赖该值。
func (f *fakeMemberships) CountByTenant(context.Context, string, string) (int64, error) {
	return 0, nil
}

// CountByTenant 返回固定计数，测试不依赖该值。
func (f *fakeInvitations) CountByTenant(context.Context, string, string) (int64, error) {
	return int64(len(f.items)), nil
}
