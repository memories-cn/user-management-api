package membership

import (
	"context"
	"errors"
	"sync"

	"github.com/example/iam-service/internal/domain/role"
	"github.com/example/iam-service/internal/ports"
)

// errCommitFailed 模拟事务提交失败，用于验证撤权不会留下半成品数据。
var errCommitFailed = errors.New("提交失败")

// fakeRoles 是内存实现的成员角色分配仓储。
type fakeRoles struct {
	mu sync.Mutex
	// deleted 记录被清除角色分配的成员关系 ID，用于验证撤权确实发生。
	deleted map[string]int
	// owners 是租户内有效所有者人数。
	owners int64
	// isOwner 标记指定用户是否为有效所有者，键为 userID。
	isOwner map[string]bool
}

func newFakeRoles() *fakeRoles {
	return &fakeRoles{deleted: map[string]int{}, isOwner: map[string]bool{}}
}

func (f *fakeRoles) Replace(_ context.Context, _, _ string, assignments []*role.Assignment) error {
	return nil
}

func (f *fakeRoles) ListRoles(_ context.Context, _, _ string) ([]*role.Role, error) {
	return nil, nil
}

func (f *fakeRoles) DeleteByMembership(_ context.Context, _, membershipID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted[membershipID]++
	return nil
}

func (f *fakeRoles) CountActiveOwners(_ context.Context, _ string) (int64, error) {
	return f.owners, nil
}

func (f *fakeRoles) HasActiveRole(_ context.Context, _, userID, _ string) (bool, error) {
	return f.isOwner[userID], nil
}

// fakePolicies 是内存实现的策略投影仓储。
type fakePolicies struct {
	mu      sync.Mutex
	revoked map[string]int
	version int64
}

func newFakePolicies() *fakePolicies {
	return &fakePolicies{revoked: map[string]int{}}
}

func (f *fakePolicies) ReplaceRoleRules(_ context.Context, _, _ string, _ []ports.PolicyPermission) error {
	return nil
}

func (f *fakePolicies) ReplaceMemberRules(_ context.Context, _, _ string, _ []string) error {
	return nil
}

func (f *fakePolicies) DeleteRoleRules(_ context.Context, _, _ string) error { return nil }

func (f *fakePolicies) DeleteMemberRules(_ context.Context, _, userID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked[userID]++
	return nil
}

func (f *fakePolicies) DeleteTenantRules(_ context.Context, _ string) error { return nil }

func (f *fakePolicies) BumpVersion(_ context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.version++
	return f.version, nil
}

func (f *fakePolicies) CurrentVersion(_ context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.version, nil
}

// fakeTx 直接执行回调的事务替身，用于覆盖回滚分支以外的场景。
type fakeTx struct{}

func (fakeTx) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

// failTx 在回调返回 nil 时仍返回错误，用于模拟提交失败。
type failTx struct{}

func (failTx) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if err := fn(ctx); err != nil {
		return err
	}
	return errCommitFailed
}

// fakeReloader 记录刷新次数。
type fakeReloader struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (f *fakeReloader) RefreshIfStale(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.err
}

// newTestService 用内存替身装配成员管理服务。
func newTestService(repo *fakeRepo) (*Service, *fakeRoles, *fakePolicies, *fakeReloader) {
	roles := newFakeRoles()
	policies := newFakePolicies()
	reloader := &fakeReloader{}
	svc := NewService(repo, roles, policies, fakeTx{}, reloader, nil)
	return svc, roles, policies, reloader
}
