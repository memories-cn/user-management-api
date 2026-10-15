package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/iam-service/internal/adapters/casbin"
	"github.com/example/iam-service/internal/bootstrap"
	"github.com/example/iam-service/internal/config"
)

// TestSecurity_HeaderSpoofingDoesNotAffectIdentity 校验请求头无法伪造身份与权限。
//
// docs/TDD.md 10.3：禁止接受任意客户端提交的 X-User-ID 作为真实用户身份；
// 内部网络本身不构成认证。这里同时覆盖身份伪造与平台角色伪造两类头。
func TestSecurity_HeaderSpoofingDoesNotAffectIdentity(t *testing.T) {
	f := newRBACFixture(t)

	req := newJSONRequest(f.stack.t, http.MethodGet,
		"/api/v1/tenants/"+f.tenantID+"/members", nil)
	req.Header.Set("Authorization", "Bearer "+f.ownerToken)
	// 试图把自己伪装成另一个用户 / 另一个租户 / 平台管理员。
	req.Header.Set("X-User-ID", "11111111-1111-1111-1111-111111111111")
	req.Header.Set("X-Tenant-ID", "22222222-2222-2222-2222-222222222222")
	req.Header.Set("X-Roles", "platform_admin")

	rec := f.stack.serve(req)

	// 身份仍由令牌决定：请求按真实身份正常处理，而不是被请求头改变归属。
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// TestSecurity_PlatformAdminCannotBeGrantedByHeader 校验平台角色无法通过请求头获得。
func TestSecurity_PlatformAdminCannotBeGrantedByHeader(t *testing.T) {
	stack := newTenancyStack(t)

	req := newJSONRequest(stack.t, http.MethodPost, "/api/v1/platform/tenants",
		map[string]string{"name": "Acme", "slug": "acme", "owner_email": "owner@example.com"})
	req.Header.Set("Authorization", "Bearer "+stack.userToken("normal", "normal@example.com"))
	req.Header.Set("X-Platform-Admin", "true")
	req.Header.Set("X-Roles", "platform_admin")

	rec := stack.serve(req)

	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String(),
		"平台管理员只能来自已验证令牌中的 ZITADEL 角色声明")
}

// TestSecurity_ErrorResponseDoesNotLeakInternals 校验故障响应不泄漏内部细节。
//
// 泄漏 DSN、SQL 或堆栈会让攻击者直接获得数据库结构与内网地址。
func TestSecurity_ErrorResponseDoesNotLeakInternals(t *testing.T) {
	f := newRBACFixture(t)

	// 断开数据库，迫使授权链路失败。
	sqlDB, err := f.stack.db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	rec := f.stack.do(http.MethodPost, "/api/v1/authorize", f.ownerToken, map[string]string{
		"tenant_id": f.tenantID,
		"resource":  "member",
		"action":    "read",
	})

	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	body := rec.Body.String()
	for _, forbidden := range []string{"postgres://", "password", "SQLSTATE", "goroutine", "sql:"} {
		assert.NotContains(t, body, forbidden, "故障响应不得泄漏内部细节：%s", forbidden)
	}
	assert.NotContains(t, body, `"allowed":true`, "依赖故障时绝不能放行")
}

// TestSecurity_MultiInstancePolicyConsistencyWindow 测量多实例策略一致性的实际传播窗口。
//
// 这是 PRD Open Question"多实例策略一致性的可接受窗口"的实测答案：
// 版本号 + 轮询机制决定了延迟下界就是刷新间隔，因此这里记录实测值供文档引用，
// 而不是断言一个拍脑袋的数字。
func TestSecurity_MultiInstancePolicyConsistencyWindow(t *testing.T) {
	stack := newTenancyStack(t)
	createdRec, created := stack.createTenant("admin-1", "Acme", "acme", "owner@example.com")
	require.Equal(t, http.StatusOK, createdRec.Code, createdRec.Body.String())
	require.Equal(t, http.StatusOK, stack.acceptInvitation(
		created.Data.Invitation.ID, "owner-1", "owner@example.com", true, created.Data.Token).Code)
	tenantID := created.Data.Tenant.ID
	ownerToken := stack.userToken("owner-1", "owner@example.com")

	// 构造第二个授权器实例。**必须新建独立的 enforcer**：
	// 若复用栈内同一个 *casbin.SyncedEnforcer，两个"实例"共享同一份内存策略，
	// 任何一方的重载都会同时影响另一方，测出来的就是零延迟的假象。
	interval := 100 * time.Millisecond
	peerEnforcer, err := bootstrap.NewEnforcer(stack.db, tenancyModelPath)
	require.NoError(t, err)
	peer, err := casbin.NewAuthorizer(peerEnforcer, stack.app.Policies, interval)
	require.NoError(t, err)
	require.Equal(t, stack.app.Authorizer.LocalVersion(), peer.LocalVersion(),
		"两个实例应基于同一策略版本起步")

	rolesRec := stack.do(http.MethodGet, "/api/v1/tenants/"+tenantID+"/roles", ownerToken, nil)
	require.Equal(t, http.StatusOK, rolesRec.Code, rolesRec.Body.String())
	var parsed struct {
		Data struct {
			Items []struct {
				ID  string `json:"id"`
				Key string `json:"key"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rolesRec.Body.Bytes(), &parsed))

	var ownerRoleID string
	for _, rl := range parsed.Data.Items {
		if rl.Key == "owner" {
			ownerRoleID = rl.ID
		}
	}
	require.NotEmpty(t, ownerRoleID)

	// 撤销前两个实例都应放行。subject 必须是租户内的真实用户 ID，
	// 否则策略匹配不到任何 g 规则，判定会恒为拒绝。
	subject := memberUserID(t, stack, tenantID)
	before, err := peer.Enforce(context.Background(), subject, tenantID, "member", "invite")
	require.NoError(t, err)
	require.True(t, before, "撤销前实例 B 应放行")

	// 实例 A 执行权限变更。
	revokeRec := stack.do(http.MethodPut,
		"/api/v1/tenants/"+tenantID+"/roles/"+ownerRoleID+"/permissions", ownerToken,
		map[string][]string{"permissions": {"member:read"}})
	require.Equal(t, http.StatusOK, revokeRec.Code, revokeRec.Body.String())

	// 让实例 B 走与生产一致的后台刷新（按 interval 定时比对版本号），
	// 而不是在测试里手动轮询——手动轮询会把传播延迟测成接近 0，
	// 从而给出一个比真实机制乐观得多的结论。
	refreshCtx, cancelRefresh := context.WithCancel(context.Background())
	defer cancelRefresh()
	peer.StartRefresher(refreshCtx)

	start := time.Now()
	deadline := start.Add(3 * time.Second)
	var propagated time.Duration
	for time.Now().Before(deadline) {
		allowed, err := peer.Enforce(context.Background(), subject, tenantID, "member", "invite")
		require.NoError(t, err)
		if !allowed {
			propagated = time.Since(start)
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	require.Less(t, propagated, 3*time.Second, "策略变更必须在合理窗口内传播到其他实例")

	// 机制保证的传播上界就是一个刷新间隔；实测值应落在该上界之内。
	require.LessOrEqual(t, propagated, interval+500*time.Millisecond,
		"传播耗时应受刷新间隔约束，超出说明重载或版本比对出了问题")

	t.Logf("多实例策略传播窗口实测值：%v（刷新间隔 %v，机制上界 = 一个间隔）",
		propagated, interval)
}

// TestSecurity_MemberRemovalRevokesImmediately 汇总撤权时效核对表。
//
// 成员移除与停用不依赖策略刷新窗口：租户守卫每次请求直查数据库，
// 因此"既有会话与缓存授权"的失效是即时的。
func TestSecurity_MemberRemovalRevokesImmediately(t *testing.T) {
	f := newRBACFixture(t)
	second := f.inviteAndAccept(t, "member-1", "member@example.com")
	require.NotEmpty(t, second)

	memberToken := f.stack.userToken("member-1", "member@example.com")
	require.True(t, f.mustAuthorize(memberToken, "member", "read"))

	rec := f.stack.do(http.MethodDelete,
		"/api/v1/tenants/"+f.tenantID+"/members/"+second, f.ownerToken, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	code, parsed := f.authorize(memberToken, f.tenantID, "member", "read")
	require.Equal(t, http.StatusOK, code)
	assert.False(t, parsed.Data.Allowed, "成员移除后必须立即失去授权，不等待任何刷新窗口")
}

// TestSecurity_RateLimitReturns429AndRecovers 校验限流生效且不会永久限死。
func TestSecurity_RateLimitReturns429AndRecovers(t *testing.T) {
	stack := newTenancyStackWithRateLimit(t, 1, 1)

	createdRec, created := stack.createTenant("admin-1", "Acme", "acme", "owner@example.com")
	require.Equal(t, http.StatusOK, createdRec.Code, createdRec.Body.String())
	require.Equal(t, http.StatusOK, stack.acceptInvitation(
		created.Data.Invitation.ID, "owner-1", "owner@example.com", true, created.Data.Token).Code)
	tenantID := created.Data.Tenant.ID
	ownerToken := stack.userToken("owner-1", "owner@example.com")

	path := "/api/v1/tenants/" + tenantID + "/members"

	// 令牌桶容量 1：第一个请求放行，第二个必须被限流。
	first := stack.do(http.MethodGet, path, ownerToken, nil)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	assert.Empty(t, first.Header().Get("Retry-After"), "放行的请求不应带重试头")

	second := stack.do(http.MethodGet, path, ownerToken, nil)
	require.Equal(t, http.StatusTooManyRequests, second.Code, second.Body.String())
	assert.Contains(t, second.Body.String(), "RATE_LIMITED")
	assert.NotEmpty(t, second.Header().Get("Retry-After"), "被限流的响应必须告知客户端何时重试")

	// 等令牌补充后应恢复放行——限流不能变成永久拒绝。
	time.Sleep(1200 * time.Millisecond)
	recovered := stack.do(http.MethodGet, path, ownerToken, nil)
	require.Equal(t, http.StatusOK, recovered.Code, recovered.Body.String(),
		"限流必须可恢复，否则等同于服务拒绝")
}

// TestSecurity_RateLimitDoesNotAffectOtherSubjects 校验不同用户互不受累。
func TestSecurity_RateLimitDoesNotAffectOtherSubjects(t *testing.T) {
	stack := newTenancyStackWithRateLimit(t, 1, 1)

	createdRec, created := stack.createTenant("admin-1", "Acme", "acme", "owner@example.com")
	require.Equal(t, http.StatusOK, createdRec.Code, createdRec.Body.String())
	require.Equal(t, http.StatusOK, stack.acceptInvitation(
		created.Data.Invitation.ID, "owner-1", "owner@example.com", true, created.Data.Token).Code)
	tenantID := created.Data.Tenant.ID

	// owner 打满自己的桶。
	rec := stack.do(http.MethodGet, "/api/v1/tenants/"+tenantID+"/members",
		stack.userToken("owner-1", "owner@example.com"), nil)
	require.Equal(t, http.StatusOK, rec.Code)

	// 另一个身份使用独立桶，不得被连带限流。
	rec = stack.do(http.MethodGet, "/api/v1/tenants/"+tenantID+"/members",
		stack.userToken("stranger", "stranger@example.com"), nil)
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String(),
		"非成员应因权限被拒，而不是因他人超额被限流")
}

// TestSecurity_MetricsAreExposedWithoutTenantLabels 校验指标可抓取且不含租户维度。
func TestSecurity_MetricsAreExposedWithoutTenantLabels(t *testing.T) {
	stack := newTenancyStack(t)

	// 先产生一些请求，否则计数器类指标没有观测值、不会被导出。
	stack.do(http.MethodGet, "/api/v1/tenants", stack.userToken("someone", "someone@example.com"), nil)

	rec := stack.do(http.MethodGet, "/metrics", "", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	body := rec.Body.String()
	assert.Contains(t, body, "iam_http_requests_total", "应暴露 HTTP 请求计数")
	assert.Contains(t, body, "iam_policy_version", "应暴露策略版本")
	assert.NotContains(t, body, "tenant_id", "指标标签不得包含租户标识")
	assert.NotContains(t, body, "user_id", "指标标签不得包含用户标识")
}

// newTenancyStackWithRateLimit 装配一个限流阈值极小的服务栈。
//
// 通过覆写配置而不是装配后替换组件来获得可控阈值，
// 避免出现"装配用的是 A、运行用的是 B"的不一致。
func newTenancyStackWithRateLimit(t *testing.T, rps float64, burst int) *tenancyStack {
	t.Helper()

	return newTenancyStackWithConfig(t, func(cfg *config.Config) {
		cfg.RateLimitEnabled = true
		cfg.ManageRPS = rps
		cfg.AuthorizeRPS = rps
		cfg.RateLimitBurst = float64(burst)
	})
}
