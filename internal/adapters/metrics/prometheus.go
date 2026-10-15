// Package metrics 提供基于 Prometheus 的指标采集实现。
//
// 分层约束：本包属于 Adapters，是唯一直接依赖 prometheus/client_golang 的地方。
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/example/iam-service/internal/ports"
)

// 编译期断言：确保实现 ports.MetricsRecorder。
var _ ports.MetricsRecorder = (*Recorder)(nil)

// durationBuckets 覆盖 1ms 到 1s 的延迟分布。
//
// 上界取 1s：超过该值的请求已属于异常，落在 +Inf 桶即可，
// 继续细分只会让直方图存储成本上升而没有信息增量。
var durationBuckets = []float64{
	0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0,
}

// Recorder 是基于 Prometheus 的指标采集器。
type Recorder struct {
	requests      *prometheus.CounterVec
	duration      *prometheus.HistogramVec
	rateLimited   *prometheus.CounterVec
	decisions     *prometheus.CounterVec
	policyVersion prometheus.Gauge
	policyReloads *prometheus.CounterVec

	registry *prometheus.Registry
}

// NewRecorder 构造指标采集器并注册全部指标。
//
// 使用独立的 Registry 而非全局默认 Registry：避免测试之间相互污染，
// 也让 /metrics 只暴露本服务自己的指标。
func NewRecorder() (*Recorder, error) {
	registry := prometheus.NewRegistry()

	rec := &Recorder{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "iam",
			Name:      "http_requests_total",
			Help:      "HTTP 请求总数，按方法、路由模板与状态码分桶。",
		}, []string{"method", "route", "status"}),

		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "iam",
			Name:      "http_request_duration_seconds",
			Help:      "HTTP 请求耗时分布，按方法与路由模板分桶。",
			Buckets:   durationBuckets,
		}, []string{"method", "route"}),

		rateLimited: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "iam",
			Name:      "rate_limited_total",
			Help:      "被限流拒绝的请求总数。",
		}, []string{"route"}),

		decisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "iam",
			Name:      "authorization_decisions_total",
			Help:      "授权判定总数，按结果分桶：allowed / denied / error。",
		}, []string{"result"}),

		policyVersion: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "iam",
			Name:      "policy_version",
			Help:      "本地已加载的策略版本号，用于观察跨实例失效是否及时。",
		}),

		policyReloads: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "iam",
			Name:      "policy_reloads_total",
			Help:      "策略重载次数，按结果分桶：ok / failed。",
		}, []string{"result"}),
	}

	collectors := []prometheus.Collector{
		rec.requests, rec.duration, rec.rateLimited,
		rec.decisions, rec.policyVersion, rec.policyReloads,
	}
	for _, c := range collectors {
		if err := registry.Register(c); err != nil {
			return nil, err
		}
	}

	rec.registry = registry
	return rec, nil
}

// ObserveRequest 记录一次 HTTP 请求。
func (r *Recorder) ObserveRequest(method, route string, status int, d time.Duration) {
	r.requests.WithLabelValues(method, route, statusLabel(status)).Inc()
	r.duration.WithLabelValues(method, route).Observe(d.Seconds())
}

// IncRateLimited 记录一次被限流的请求。
func (r *Recorder) IncRateLimited(route string) {
	r.rateLimited.WithLabelValues(route).Inc()
}

// IncDecision 记录一次授权判定。
func (r *Recorder) IncDecision(result string) {
	r.decisions.WithLabelValues(result).Inc()
}

// SetPolicyVersion 更新本地策略版本。
func (r *Recorder) SetPolicyVersion(version int64) {
	r.policyVersion.Set(float64(version))
}

// IncPolicyReload 记录一次策略重载。
func (r *Recorder) IncPolicyReload(result string) {
	r.policyReloads.WithLabelValues(result).Inc()
}

// Gatherer 返回指标收集器，供 HTTP 处理器导出。
func (r *Recorder) Gatherer() prometheus.Gatherer {
	return r.registry
}

// statusLabel 把状态码归一化为三位字符串。
//
// 不归一化会让 200 与 "200" 变成两个标签值，
// 而客户端提供的非法状态码（正常不会发生）则会制造高基数标签。
func statusLabel(status int) string {
	if status < 100 || status > 999 {
		return "unknown"
	}
	return itoa(status)
}

// itoa 避免为一个小转换引入 strconv 之外的开销。
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [3]byte
	i := len(buf)
	for v > 0 && i > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
