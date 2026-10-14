// Package casbin 把 Casbin 授权引擎适配为应用层使用的 ports.Authorizer。
//
// 分层约束：本包属于 Adapters，是唯一直接依赖 Casbin 的地方（除 bootstrap 装配外）。
package casbin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/casbin/casbin/v3"

	"github.com/example/iam-service/internal/ports"
)

// ErrPolicyReload 表示策略重载失败，此时本地内存策略可能已过期。
//
// 该错误必须被显式处理：不能继续用旧策略放行，也不能假装重载成功。
var ErrPolicyReload = errors.New("授权策略重载失败")

// Authorizer 基于 Casbin 实现统一授权判定。
type Authorizer struct {
	enforcer *casbin.SyncedEnforcer
	versions ports.PolicyRepository
	// localVersion 是本地已加载的策略版本，用原子变量避免与判定路径争锁。
	localVersion atomic.Int64
	// interval 是后台失效检查间隔。
	interval time.Duration

	// lastErrMu 保护 lastErr；atomic.Value 不允许存 nil，因此用互斥锁。
	lastErrMu sync.RWMutex
	lastErr   error
}

// 编译期断言：确保实现 ports.Authorizer。
var _ ports.Authorizer = (*Authorizer)(nil)

// NewAuthorizer 构造授权器，并立即读取当前策略版本作为本地基线。
func NewAuthorizer(enforcer *casbin.SyncedEnforcer, versions ports.PolicyRepository, interval time.Duration) (*Authorizer, error) {
	if enforcer == nil {
		return nil, errors.New("授权引擎不能为空")
	}
	if versions == nil {
		return nil, errors.New("策略版本仓储不能为空")
	}
	if interval <= 0 {
		return nil, errors.New("策略刷新间隔必须为正")
	}

	a := &Authorizer{enforcer: enforcer, versions: versions, interval: interval}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	version, err := versions.CurrentVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取初始策略版本失败：%w", err)
	}
	a.localVersion.Store(version)
	return a, nil
}

// Enforce 判定 subject 在 tenantID 下能否对 resource 执行 action。
//
// 语义严格遵循 ports.Authorizer 的约定：
//   - (false, nil)：明确拒绝；
//   - (_, err)：决策无法完成，调用方必须失败关闭。
func (a *Authorizer) Enforce(ctx context.Context, subject, tenantID, resource, action string) (bool, error) {
	if subject == "" || tenantID == "" || resource == "" || action == "" {
		return false, fmt.Errorf("授权判定参数不完整：subject=%q tenant=%q resource=%q action=%q",
			subject, tenantID, resource, action)
	}

	allowed, err := a.enforcer.Enforce(subject, tenantID, resource, action)
	if err != nil {
		return false, fmt.Errorf("授权决策失败：%w", err)
	}
	return allowed, nil
}

// RefreshIfStale 比对全局策略版本，过期则重新加载内存策略。
//
// 这是"多实例失效"的当前实现：不引入 Redis，由各实例按固定间隔自行比对版本号。
// 成员级撤权不依赖本窗口——租户守卫每次请求都会直查数据库（见 docs/TDD.md 8.4）。
func (a *Authorizer) RefreshIfStale(ctx context.Context) error {
	current, err := a.versions.CurrentVersion(ctx)
	if err != nil {
		return fmt.Errorf("读取策略版本失败：%w", err)
	}
	if current <= a.localVersion.Load() {
		return nil
	}
	return a.reload(ctx, current)
}

// LocalVersion 返回本地已加载的策略版本，便于测试与排障观察。
func (a *Authorizer) LocalVersion() int64 {
	return a.localVersion.Load()
}

// LastError 返回最近一次重载错误，供就绪检查判断是否应摘除流量。
func (a *Authorizer) LastError() error {
	a.lastErrMu.RLock()
	defer a.lastErrMu.RUnlock()
	return a.lastErr
}

// setLastErr 记录重载结果。
func (a *Authorizer) setLastErr(err error) {
	a.lastErrMu.Lock()
	defer a.lastErrMu.Unlock()
	a.lastErr = err
}

// StartRefresher 启动后台失效检查，直到 ctx 取消为止。
//
// 任何一次 panic 都被拦截并记录，绝不让后台协程把进程带崩。
func (a *Authorizer) StartRefresher(ctx context.Context) {
	go a.refreshLoop(ctx)
}

// refreshLoop 按固定间隔执行版本比对与重载。
func (a *Authorizer) refreshLoop(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("策略刷新协程异常退出", "panic", r)
			a.setLastErr(fmt.Errorf("%w：协程 panic", ErrPolicyReload))
		}
	}()

	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.refreshOnce(ctx)
		}
	}
}

// refreshOnce 执行一次刷新，失败时记录错误供就绪检查读取。
func (a *Authorizer) refreshOnce(ctx context.Context) {
	refreshCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := a.RefreshIfStale(refreshCtx); err != nil {
		slog.ErrorContext(refreshCtx, "刷新授权策略失败", "error", err)
		a.setLastErr(err)
		return
	}
	a.setLastErr(nil)
}

// reload 重新加载策略并更新本地版本。
func (a *Authorizer) reload(ctx context.Context, version int64) error {
	if err := a.enforcer.LoadPolicy(); err != nil {
		return fmt.Errorf("%w：%v", ErrPolicyReload, err)
	}
	a.localVersion.Store(version)
	slog.InfoContext(ctx, "授权策略已刷新", "version", version)
	return nil
}
