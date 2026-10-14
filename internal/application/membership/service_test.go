package membership

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	membershipdomain "github.com/example/iam-service/internal/domain/membership"
)

// fakeRepo 是内存实现的成员关系仓储。
type fakeRepo struct {
	items   map[string]*membershipdomain.Membership
	active  int64
	listErr error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{items: map[string]*membershipdomain.Membership{}}
}

func (f *fakeRepo) Create(_ context.Context, m *membershipdomain.Membership) error {
	f.items[m.TenantID.String()+"|"+m.UserID.String()] = m
	return nil
}

func (f *fakeRepo) FindByTenantAndUser(_ context.Context, tenantID, userID string) (*membershipdomain.Membership, error) {
	return f.items[tenantID+"|"+userID], nil
}

func (f *fakeRepo) ListByTenant(_ context.Context, tenantID string, limit, offset int) ([]*membershipdomain.Membership, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []*membershipdomain.Membership
	for k, m := range f.items {
		if len(k) >= len(tenantID) && k[:len(tenantID)] == tenantID {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeRepo) CountActiveByTenant(_ context.Context, _ string) (int64, error) {
	var count int64
	for _, m := range f.items {
		if m.IsActive() {
			count++
		}
	}
	return count, nil
}

func (f *fakeRepo) Update(_ context.Context, m *membershipdomain.Membership) error {
	f.items[m.TenantID.String()+"|"+m.UserID.String()] = m
	return nil
}

// seed 在指定租户下创建一个 active 成员。
func seed(t *testing.T, repo *fakeRepo, tenantID uuid.UUID) *membershipdomain.Membership {
	t.Helper()

	m, err := membershipdomain.NewMembership(tenantID, uuid.New())
	require.NoError(t, err)
	require.NoError(t, repo.Create(context.Background(), m))
	return m
}

func TestList_NormalizesPaging(t *testing.T) {
	cases := []struct {
		limit, offset         int
		wantLimit, wantOffset int
	}{
		{0, -1, DefaultLimit, 0},
		{1000, 5, MaxLimit, 5},
		{10, 20, 10, 20},
	}
	for _, c := range cases {
		gotLimit, gotOffset := normalizePaging(c.limit, c.offset)
		assert.Equal(t, c.wantLimit, gotLimit, "limit=%d", c.limit)
		assert.Equal(t, c.wantOffset, gotOffset, "offset=%d", c.offset)
	}
}

func TestGet_NotFound(t *testing.T) {
	svc, _, _, _ := newTestService(newFakeRepo())

	_, err := svc.Get(context.Background(), uuid.NewString(), uuid.NewString())

	require.ErrorIs(t, err, ErrMembershipNotFound)
}

func TestUpdateStatus_SuspendAndActivate(t *testing.T) {
	repo := newFakeRepo()
	svc, _, _, _ := newTestService(repo)
	tenantID := uuid.New()
	m := seed(t, repo, tenantID)
	ctx := context.Background()

	updated, err := svc.UpdateStatus(ctx, tenantID.String(), m.UserID.String(), membershipdomain.StatusSuspended)
	require.NoError(t, err)
	assert.Equal(t, membershipdomain.StatusSuspended, updated.Status)

	updated, err = svc.UpdateStatus(ctx, tenantID.String(), m.UserID.String(), membershipdomain.StatusActive)
	require.NoError(t, err)
	assert.Equal(t, membershipdomain.StatusActive, updated.Status)
}

func TestUpdateStatus_RejectsInvalidStatus(t *testing.T) {
	repo := newFakeRepo()
	svc, _, _, _ := newTestService(repo)
	tenantID := uuid.New()
	m := seed(t, repo, tenantID)

	_, err := svc.UpdateStatus(context.Background(), tenantID.String(), m.UserID.String(), membershipdomain.StatusRemoved)

	require.ErrorIs(t, err, ErrInvalidStatus)
}

func TestUpdateStatus_RejectsRemovedMember(t *testing.T) {
	repo := newFakeRepo()
	svc, _, _, _ := newTestService(repo)
	tenantID := uuid.New()
	m := seed(t, repo, tenantID)
	m.Remove()
	require.NoError(t, repo.Update(context.Background(), m))

	_, err := svc.UpdateStatus(context.Background(), tenantID.String(), m.UserID.String(), membershipdomain.StatusActive)

	require.ErrorIs(t, err, ErrMembershipNotFound, "已移除成员须重新走邀请流程")
}

func TestRemove_SoftDeletes(t *testing.T) {
	repo := newFakeRepo()
	svc, _, _, _ := newTestService(repo)
	tenantID := uuid.New()
	first := seed(t, repo, tenantID)
	seed(t, repo, tenantID)

	require.NoError(t, svc.Remove(context.Background(), tenantID.String(), first.UserID.String()))

	stored, err := repo.FindByTenantAndUser(context.Background(), tenantID.String(), first.UserID.String())
	require.NoError(t, err)
	require.NotNil(t, stored, "软删除保留行以维持唯一约束")
	assert.Equal(t, membershipdomain.StatusRemoved, stored.Status)
	assert.Nil(t, stored.JoinedAt)
}

func TestRemove_ProtectsLastMember(t *testing.T) {
	repo := newFakeRepo()
	svc, _, _, _ := newTestService(repo)
	tenantID := uuid.New()
	only := seed(t, repo, tenantID)

	err := svc.Remove(context.Background(), tenantID.String(), only.UserID.String())

	require.ErrorIs(t, err, ErrCannotRemoveLastMember)
}

func TestRemove_IsIdempotentForRemoved(t *testing.T) {
	repo := newFakeRepo()
	svc, _, _, _ := newTestService(repo)
	tenantID := uuid.New()
	first := seed(t, repo, tenantID)
	seed(t, repo, tenantID)
	ctx := context.Background()

	require.NoError(t, svc.Remove(ctx, tenantID.String(), first.UserID.String()))

	err := svc.Remove(ctx, tenantID.String(), first.UserID.String())
	require.ErrorIs(t, err, ErrMembershipNotFound, "重复移除返回不存在而不是静默成功")
}
