package handlers

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/example/iam-service/internal/adapters/metrics"
)

// MetricsHandler 提供 Prometheus 指标端点。
type MetricsHandler struct {
	recorder *metrics.Recorder
}

// NewMetricsHandler 构造指标处理器。
func NewMetricsHandler(recorder *metrics.Recorder) *MetricsHandler {
	return &MetricsHandler{recorder: recorder}
}

// Metrics 以 Prometheus 文本格式导出指标。
//
// **该端点必须由边缘网关阻断，不对公网暴露**：它包含路由结构与请求量等运营信息。
// 指标标签中刻意不含 tenant_id / user_id，避免把租户维度泄漏进指标存储。
func (h *MetricsHandler) Metrics(c *gin.Context) {
	var gatherer prometheus.Gatherer
	if h.recorder != nil {
		gatherer = h.recorder.Gatherer()
	}

	handler := promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{
		// 指标损坏时返回 500 而不是部分数据：宁可让抓取失败，
		// 也不要让监控基于一份不完整的指标做出容量判断。
		ErrorHandling: promhttp.HTTPErrorOnError,
	})
	handler.ServeHTTP(c.Writer, c.Request)
}
