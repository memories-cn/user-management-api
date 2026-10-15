package integration

import (
	"net/http"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/example/iam-service/internal/config"
)

// 负载测试参数。
const (
	// loadConcurrency 是并发 worker 数，对应 docs/TDD.md 12.1 的
	// "单实例支持至少 100 个并发请求"基线。
	loadConcurrency = 100
	// loadRequestsPerWorker 是每个 worker 的请求数。
	loadRequestsPerWorker = 200
	// loadWarmup 是预热请求数：不计入统计，用于建立连接池与 Casbin 热缓存。
	loadWarmup = 20

	// loadAuthorizeTargetP95 是授权判定接口的 P95 目标（docs/TDD.md 12.1）。
	loadAuthorizeTargetP95 = 200 * time.Millisecond
	// loadManageTargetP95 是常规管理接口的 P95 目标。
	loadManageTargetP95 = 200 * time.Millisecond
)

// loadResult 汇总一次负载测试的结果。
type loadResult struct {
	Scenario string
	Total    int
	// Durations 是全部请求的耗时，已排序，便于取分位数。
	Durations []time.Duration
	Failures  int
	// Statuses 记录每个状态码的出现次数，便于发现非预期的错误分布。
	Statuses map[int]int
}

// pct 返回第 p 百分位的耗时（p 取值 0-100）。
func (r *loadResult) pct(p float64) time.Duration {
	if len(r.Durations) == 0 {
		return 0
	}
	idx := int(float64(len(r.Durations)-1) * p / 100)
	return r.Durations[idx]
}

// report 输出 Markdown 表格，便于直接贴进 docs/performance.md。
func (r *loadResult) report(t *testing.T) {
	t.Helper()
	t.Logf("### %s\n", r.Scenario)
	t.Logf("| 指标 | 值 |")
	t.Logf("| --- | --- |")
	t.Logf("| 总请求数 | %d（失败 %d） |", r.Total, r.Failures)
	t.Logf("| 并发 | %d |", loadConcurrency)
	for _, code := range sortedStatuses(r.Statuses) {
		t.Logf("| HTTP %d | %d |", code, r.Statuses[code])
	}
	t.Logf("| P50 | %v |", r.pct(50))
	t.Logf("| P95 | %v |", r.pct(95))
	t.Logf("| P99 | %v |", r.pct(99))
	t.Logf("")
}

// sortedStatuses 返回排序后的状态码，保证报告顺序稳定。
func sortedStatuses(m map[int]int) []int {
	codes := make([]int, 0, len(m))
	for code := range m {
		codes = append(codes, code)
	}
	sort.Ints(codes)
	return codes
}

// runLoad 用固定并发度对 path 发起请求并统计耗时。
//
// worker 池用 WaitGroup 控制：每个 worker 串行发完自己的份额，
// 从而保证任意时刻的并发度恰好是 loadConcurrency，
// 而不是"开 N 个 goroutine 各自跑完全部请求"叠加成 N×M 并发。
func runLoad(t *testing.T, stack *tenancyStack, scenario, method, path, token string, body any) *loadResult {
	t.Helper()

	result := &loadResult{
		Scenario: scenario,
		Statuses: map[int]int{},
	}

	record := func(code int, d time.Duration) {
		result.Total++
		result.Statuses[code]++
		result.Durations = append(result.Durations, d)
		if code != http.StatusOK {
			result.Failures++
		}
	}

	// 预热：建立连接池、触发 Casbin 热缓存，避免首批请求的冷启动拉高分位数。
	for i := 0; i < loadWarmup; i++ {
		start := time.Now()
		rec := stack.do(method, path, token, body)
		record(rec.Code, time.Since(start))
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	start := time.Now()
	for w := 0; w < loadConcurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < loadRequestsPerWorker; i++ {
				reqStart := time.Now()
				rec := stack.do(method, path, token, body)
				elapsed := time.Since(reqStart)

				mu.Lock()
				record(rec.Code, elapsed)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	sort.Slice(result.Durations, func(i, j int) bool {
		return result.Durations[i] < result.Durations[j]
	})

	qps := float64(result.Total) / elapsed.Seconds()
	t.Logf("### %s\n", result.Scenario)
	t.Logf("| 指标 | 值 |")
	t.Logf("| --- | --- |")
	t.Logf("| 总请求数 | %d（失败 %d） |", result.Total, result.Failures)
	t.Logf("| 并发 | %d |", loadConcurrency)
	t.Logf("| 吞吐 | %.0f req/s |", qps)
	for _, code := range sortedStatuses(result.Statuses) {
		t.Logf("| HTTP %d | %d |", code, result.Statuses[code])
	}
	t.Logf("| P50 | %v |", result.pct(50))
	t.Logf("| P95 | %v |", result.pct(95))
	t.Logf("| P99 | %v |", result.pct(99))
	t.Logf("")
	return result
}

// TestLoad_AuthorizeAndManageAPIs 是门控的端到端负载测试。
//
// 默认**不运行**：它需要 Docker，且结果受机器负载影响很大，
// 放进常规 CI 只会让流水线变慢并产出无意义的数据。
// 需要时显式开启：
//
//	IAM_LOAD_TEST=1 go test ./tests/integration/... -run TestLoad -count=1 -v -timeout 30m
//
// 目标见 docs/TDD.md 12.1：常规业务 API P95 ≤ 200ms。
// 纯本地授权判定的 10ms 目标由 internal/adapters/casbin 的微基准覆盖。
func TestLoad_AuthorizeAndManageAPIs(t *testing.T) {
	if os.Getenv("IAM_LOAD_TEST") != "1" {
		t.Skip("跳过负载测试：设置 IAM_LOAD_TEST=1 后运行")
	}

	// 关掉限流：本测试要测的是并发下的延迟分布，
	// 限流行为已由 TestSecurity_RateLimit* 覆盖。开着它只会测出一片 429。
	stack := newTenancyStackWithConfig(t, func(cfg *config.Config) {
		cfg.RateLimitEnabled = false
	})

	tenantID, ownerToken := seedOwnerForLoad(t, stack)

	authorizePath := "/api/v1/authorize"
	authorizeBody := map[string]string{
		"tenant_id": tenantID,
		"resource":  "member",
		"action":    "read",
	}

	authz := runLoad(t, stack, "POST /api/v1/authorize（授权判定）",
		http.MethodPost, authorizePath, ownerToken, authorizeBody)

	membersPath := "/api/v1/tenants/" + tenantID + "/members"
	manage := runLoad(t, stack, "GET /api/v1/tenants/:tenantID/members（成员列表）",
		http.MethodGet, membersPath, ownerToken, nil)

	// 两个场景都不应有失败：负载下的错误会掩盖延迟数字的意义。
	require.Zero(t, authz.Failures, "授权判定场景不应出现失败请求")
	require.Zero(t, manage.Failures, "成员列表场景不应出现失败请求")

	// 与 docs/TDD.md 12.1 的目标对照。
	if authz.pct(95) > loadAuthorizeTargetP95 {
		t.Errorf("授权判定 P95 = %v，超过目标 %v", authz.pct(95), loadAuthorizeTargetP95)
	}
	if manage.pct(95) > loadManageTargetP95 {
		t.Errorf("管理接口 P95 = %v，超过目标 %v", manage.pct(95), loadManageTargetP95)
	}

	t.Logf("对照 docs/TDD.md 12.1：常规业务 API P95 ≤ %v", loadManageTargetP95)
	t.Logf("注意：以上数字在当前机器上测得，目标部署环境需复测（见 docs/performance.md）。")
}

// seedOwnerForLoad 创建一个租户并让 owner 接受邀请，返回负载测试所需的最小环境。
func seedOwnerForLoad(t *testing.T, stack *tenancyStack) (tenantID, ownerToken string) {
	t.Helper()

	createdRec, created := stack.createTenant("admin-1", "Acme", "acme", "owner@example.com")
	require.Equal(t, http.StatusOK, createdRec.Code, createdRec.Body.String())
	require.Equal(t, http.StatusOK, stack.acceptInvitation(
		created.Data.Invitation.ID, "owner-1", "owner@example.com", true, created.Data.Token).Code)

	return created.Data.Tenant.ID, stack.userToken("owner-1", "owner@example.com")
}
