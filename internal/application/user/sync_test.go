package user

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/iam-service/internal/domain/user"
	"github.com/example/iam-service/internal/ports"
)

// fakeRepo 是内存实现的 UserRepository，同时记录调用次数以便断言写库行为。
type fakeRepo struct {
	users      map[string]*user.User
	findErr    error
	createErr  error
	updateErr  error
	findCalls  int
	createCall int
	updateCall int
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{users: map[string]*user.User{}}
}

func (f *fakeRepo) FindByZitadelUserID(_ context.Context, zitadelUserID string) (*user.User, error) {
	f.findCalls++
	if f.findErr != nil {
		return nil, f.findErr
	}
	return f.users[zitadelUserID], nil
}

func (f *fakeRepo) Create(_ context.Context, u *user.User) error {
	f.createCall++
	if f.createErr != nil {
		return f.createErr
	}
	f.users[u.ZitadelUserID] = u
	return nil
}

func (f *fakeRepo) Update(_ context.Context, u *user.User) error {
	f.updateCall++
	if f.updateErr != nil {
		return f.updateErr
	}
	f.users[u.ZitadelUserID] = u
	return nil
}

func TestEnsure_CreatesUserOnFirstAccess(t *testing.T) {
	repo := newFakeRepo()
	svc := NewSyncService(repo)

	got, err := svc.Ensure(context.Background(), &ports.Claims{Subject: "zitadel-001", DisplayName: "Alice"})

	require.NoError(t, err)
	assert.Equal(t, "zitadel-001", got.ZitadelUserID)
	assert.Equal(t, "Alice", got.DisplayName)
	assert.Equal(t, 1, repo.createCall)
}

func TestEnsure_DoesNotRecreateExistingUser(t *testing.T) {
	repo := newFakeRepo()
	svc := NewSyncService(repo)

	_, err := svc.Ensure(context.Background(), &ports.Claims{Subject: "zitadel-001", DisplayName: "Alice"})
	require.NoError(t, err)
	second, err := svc.Ensure(context.Background(), &ports.Claims{Subject: "zitadel-001", DisplayName: "Alice"})
	require.NoError(t, err)

	assert.Equal(t, 1, repo.createCall, "同一 sub 重复访问不得重复创建")
	assert.Equal(t, 0, repo.updateCall, "资料未变化时不应写库")
	assert.Equal(t, 2, repo.findCalls)
	_ = second
}

func TestEnsure_UpdatesOnlyWhenProfileChanged(t *testing.T) {
	repo := newFakeRepo()
	svc := NewSyncService(repo)

	_, err := svc.Ensure(context.Background(), &ports.Claims{Subject: "zitadel-001", DisplayName: "Alice"})
	require.NoError(t, err)

	_, err = svc.Ensure(context.Background(), &ports.Claims{Subject: "zitadel-001", DisplayName: "Bob"})
	require.NoError(t, err)
	assert.Equal(t, 1, repo.updateCall, "展示资料变化时应更新一次")
	assert.Equal(t, "Bob", repo.users["zitadel-001"].DisplayName)

	_, err = svc.Ensure(context.Background(), &ports.Claims{Subject: "zitadel-001", DisplayName: "Bob"})
	require.NoError(t, err)
	assert.Equal(t, 1, repo.updateCall, "资料未变化时不重复写库")
}

func TestEnsure_IgnoresEmptyDisplayName(t *testing.T) {
	repo := newFakeRepo()
	svc := NewSyncService(repo)

	_, err := svc.Ensure(context.Background(), &ports.Claims{Subject: "zitadel-001", DisplayName: "Alice"})
	require.NoError(t, err)
	_, err = svc.Ensure(context.Background(), &ports.Claims{Subject: "zitadel-001"})
	require.NoError(t, err)

	assert.Equal(t, 0, repo.updateCall, "身份平台未返回展示资料时不得抹掉本地资料")
	assert.Equal(t, "Alice", repo.users["zitadel-001"].DisplayName)
}

func TestEnsure_ReturnsErrUserDisabled(t *testing.T) {
	repo := newFakeRepo()
	svc := NewSyncService(repo)

	existing, err := user.NewUser("zitadel-001")
	require.NoError(t, err)
	existing.Disable()
	repo.users["zitadel-001"] = existing

	_, err = svc.Ensure(context.Background(), &ports.Claims{Subject: "zitadel-001"})

	require.ErrorIs(t, err, ErrUserDisabled)
	assert.Equal(t, 0, repo.updateCall)
}

func TestEnsure_PropagatesRepositoryError(t *testing.T) {
	repoErr := errors.New("database down")

	t.Run("find", func(t *testing.T) {
		repo := newFakeRepo()
		repo.findErr = repoErr
		svc := NewSyncService(repo)

		_, err := svc.Ensure(context.Background(), &ports.Claims{Subject: "zitadel-001"})

		require.ErrorIs(t, err, repoErr)
	})

	t.Run("create", func(t *testing.T) {
		repo := newFakeRepo()
		repo.createErr = repoErr
		svc := NewSyncService(repo)

		_, err := svc.Ensure(context.Background(), &ports.Claims{Subject: "zitadel-001"})

		require.ErrorIs(t, err, repoErr)
	})
}

func TestEnsure_RejectsClaimsWithoutSubject(t *testing.T) {
	svc := NewSyncService(newFakeRepo())

	_, err := svc.Ensure(context.Background(), &ports.Claims{})

	require.Error(t, err)
}
