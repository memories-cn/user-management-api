package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// labelNames 返回收集器上所有指标的标签名，用于校验标签基数与隐私约束。
func labelNames(t *testing.T, c prometheus.Collector) []string {
	t.Helper()

	reg := prometheus.NewPedanticRegistry()
	require.NoError(t, reg.Register(c))

	got, err := reg.Gather()
	require.NoError(t, err)

	var names []string
	for _, family := range got {
		for _, m := range family.GetMetric() {
			for _, pair := range m.GetLabel() {
				names = append(names, pair.GetName())
			}
		}
	}
	return names
}

func TestNewRecorder_RegistersAllMetrics(t *testing.T) {
	rec, err := NewRecorder()
	require.NoError(t, err)

	assert.NotNil(t, rec.Gatherer(), "收集器必须可导出")
}

func TestObserveRequest_CountsAndObserves(t *testing.T) {
	rec, err := NewRecorder()
	require.NoError(t, err)

	rec.ObserveRequest("GET", "/api/v1/tenants/:tenantID", 200, 5*time.Millisecond)
	rec.ObserveRequest("GET", "/api/v1/tenants/:tenantID", 403, 2*time.Millisecond)

	// 每个状态码是独立的计数器序列，分别断言避免互相干扰。
	assert.Equal(t, 1.0, testutil.ToFloat64(rec.requests.WithLabelValues(
		"GET", "/api/v1/tenants/:tenantID", "200")))
	assert.Equal(t, 1.0, testutil.ToFloat64(rec.requests.WithLabelValues(
		"GET", "/api/v1/tenants/:tenantID", "403")))
}

func TestObserveRequest_NormalizesStatusLabel(t *testing.T) {
	rec, err := NewRecorder()
	require.NoError(t, err)

	rec.ObserveRequest("GET", "/x", 0, time.Millisecond)
	rec.ObserveRequest("GET", "/x", 9999, time.Millisecond)

	// 两个非法状态码都归入 unknown，而不是制造两个高基数标签值。
	assert.Equal(t, 2.0, testutil.ToFloat64(rec.requests.WithLabelValues("GET", "/x", "unknown")))
}

func TestIncRateLimited(t *testing.T) {
	rec, err := NewRecorder()
	require.NoError(t, err)

	rec.IncRateLimited("/api/v1/authorize")
	rec.IncRateLimited("/api/v1/authorize")

	assert.Equal(t, 2.0, testutil.ToFloat64(rec.rateLimited.WithLabelValues("/api/v1/authorize")))
}

func TestIncDecision(t *testing.T) {
	rec, err := NewRecorder()
	require.NoError(t, err)

	rec.IncDecision("allowed")
	rec.IncDecision("denied")
	rec.IncDecision("error")

	assert.Equal(t, 1.0, testutil.ToFloat64(rec.decisions.WithLabelValues("allowed")))
	assert.Equal(t, 1.0, testutil.ToFloat64(rec.decisions.WithLabelValues("denied")))
	assert.Equal(t, 1.0, testutil.ToFloat64(rec.decisions.WithLabelValues("error")))
}

func TestSetPolicyVersion(t *testing.T) {
	rec, err := NewRecorder()
	require.NoError(t, err)

	rec.SetPolicyVersion(42)

	assert.Equal(t, 42.0, testutil.ToFloat64(rec.policyVersion))
}

func TestIncPolicyReload(t *testing.T) {
	rec, err := NewRecorder()
	require.NoError(t, err)

	rec.IncPolicyReload("ok")
	rec.IncPolicyReload("failed")

	assert.Equal(t, 1.0, testutil.ToFloat64(rec.policyReloads.WithLabelValues("ok")))
	assert.Equal(t, 1.0, testutil.ToFloat64(rec.policyReloads.WithLabelValues("failed")))
}

// TestMetrics_LabelsExcludeTenantIdentity 是硬性约束：
// 标签里出现租户或用户标识会同时造成基数爆炸与隐私泄漏。
func TestMetrics_LabelsExcludeTenantIdentity(t *testing.T) {
	rec, err := NewRecorder()
	require.NoError(t, err)

	rec.ObserveRequest("GET", "/api/v1/tenants/:tenantID/members", 200, time.Millisecond)

	names := labelNames(t, rec.requests)
	for _, name := range names {
		assert.NotContains(t, []string{"tenant_id", "user_id", "subject", "actor_user_id"}, name,
			"指标标签不得包含租户或用户标识")
	}
}

func TestStatusLabel(t *testing.T) {
	assert.Equal(t, "200", statusLabel(200))
	assert.Equal(t, "503", statusLabel(503))
	assert.Equal(t, "unknown", statusLabel(0))
	assert.Equal(t, "unknown", statusLabel(99))
	assert.Equal(t, "unknown", statusLabel(1000))
}

func TestItoa(t *testing.T) {
	assert.Equal(t, "0", itoa(0))
	assert.Equal(t, "200", itoa(200))
	assert.Equal(t, "503", itoa(503))
}
