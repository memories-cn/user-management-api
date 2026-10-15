package casbin

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/casbin/casbin/v3"
	fileadapter "github.com/casbin/casbin/v3/persist/file-adapter"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/example/iam-service/internal/rbactest"
)

// benchPolicy 是基准测试使用的策略文件内容。
//
// 形态与生产一致：一条 owner 角色的 p 规则 + 一条 user→role 的 g 规则，
// 因此测量到的是真实的"用户继承角色权限"判定路径，而不是空模型短路。
const benchPolicy = `p, role-owner, tenant-1, member, invite
p, role-owner, tenant-1, member, read
g, user-1, role-owner, tenant-1
`

// setupBenchEnforcer 构造一个带真实策略的授权器。
//
// 用文件 adapter 与内存版本仓储：基准测试关注的是 Enforce 本身的开销，
// 不该被数据库往返污染。注意不能给 NewSyncedEnforcer 传 nil adapter——
// 那会在初始化时 panic。
func setupBenchEnforcer(b *testing.B) *Authorizer {
	b.Helper()

	dir := b.TempDir()
	policyPath := filepath.Join(dir, "policy.csv")
	require.NoError(b, os.WriteFile(policyPath, []byte(benchPolicy), 0o600))

	modelPath := "../../../casbin/model.conf"
	enforcer, err := casbin.NewSyncedEnforcer(modelPath, fileadapter.NewAdapter(policyPath))
	require.NoError(b, err)

	fix := rbactest.NewFixture()
	// interval 只影响后台刷新协程的节流，基准不启动它，
	// 但仍需给一个正值以通过构造校验。
	authorizer, err := NewAuthorizer(enforcer, fix.Policies, time.Hour)
	require.NoError(b, err)
	return authorizer
}

// BenchmarkEnforce_Hit 测量命中最深角色继承链的判定耗时。
//
// 对应 docs/TDD.md 12.1 的目标"纯本地授权决策 P95 ≤ 10ms"。
func BenchmarkEnforce_Hit(b *testing.B) {
	authorizer := setupBenchEnforcer(b)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		allowed, err := authorizer.Enforce(ctx, "user-1", "tenant-1", "member", "invite")
		if err != nil || !allowed {
			b.Fatalf("判定结果不符合预期：allowed=%v err=%v", allowed, err)
		}
	}
}

// BenchmarkEnforce_Miss 测量未定义权限的判定耗时。
//
// 默认拒绝路径与命中路径同样在热路径上，且是安全回归的重点，必须一并测量。
func BenchmarkEnforce_Miss(b *testing.B) {
	authorizer := setupBenchEnforcer(b)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		allowed, err := authorizer.Enforce(ctx, "user-1", "tenant-1", "member", "remove")
		if err != nil || allowed {
			b.Fatalf("未定义权限必须拒绝：allowed=%v err=%v", allowed, err)
		}
	}
}

// BenchmarkEnforce_CrossTenant 测量跨租户判定的耗时。
//
// 跨租户必须命中不了其他租户的策略——这条路径的正确性由集成测试保证，
// 这里只测量它的开销，确认它没有因为额外匹配而显著变慢。
func BenchmarkEnforce_CrossTenant(b *testing.B) {
	authorizer := setupBenchEnforcer(b)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		allowed, err := authorizer.Enforce(ctx, "user-1", "tenant-2", "member", "invite")
		if err != nil || allowed {
			b.Fatalf("跨租户不得命中策略：allowed=%v err=%v", allowed, err)
		}
	}
}

// BenchmarkEnforce_UnknownSubject 测量完全未知用户的判定耗时。
func BenchmarkEnforce_UnknownSubject(b *testing.B) {
	authorizer := setupBenchEnforcer(b)
	ctx := context.Background()
	subject := uuid.NewString()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		allowed, err := authorizer.Enforce(ctx, subject, "tenant-1", "member", "invite")
		if err != nil || allowed {
			b.Fatalf("未知用户必须拒绝：allowed=%v err=%v", allowed, err)
		}
	}
}
