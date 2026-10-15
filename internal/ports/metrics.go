package ports

import "time"

// MetricsRecorder 抽象指标采集能力。
//
// 传输层只依赖本接口，不直接依赖 Prometheus，
// 以便将来更换指标后端时不影响中间件与处理器。
type MetricsRecorder interface {
	// ObserveRequest 记录一次 HTTP 请求。
	//
	// route 必须是路由模板（如 /api/v1/tenants/:tenantID/members），
	// 绝不能是具体路径——否则标签基数会无限增长并打爆指标存储。
	ObserveRequest(method, route string, status int, duration time.Duration)
	// IncRateLimited 记录一次被限流的请求。
	IncRateLimited(route string)
	// IncDecision 记录一次授权判定，result 为 allowed / denied / error。
	IncDecision(result string)
	// SetPolicyVersion 更新本地策略版本 gauges。
	SetPolicyVersion(version int64)
	// IncPolicyReload 记录一次策略重载，result 为 ok / failed。
	IncPolicyReload(result string)
}
