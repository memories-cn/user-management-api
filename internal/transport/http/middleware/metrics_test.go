package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/iam-service/internal/ports"
)

// recordingRecorder 记录指标调用，便于断言标签取值。
type recordingRecorder struct {
	calls []recordedRequest
}

type recordedRequest struct {
	method   string
	route    string
	status   int
	duration time.Duration
}

func (r *recordingRecorder) ObserveRequest(method, route string, status int, d time.Duration) {
	r.calls = append(r.calls, recordedRequest{method: method, route: route, status: status, duration: d})
}

func (r *recordingRecorder) IncRateLimited(string)  {}
func (r *recordingRecorder) IncDecision(string)     {}
func (r *recordingRecorder) SetPolicyVersion(int64) {}
func (r *recordingRecorder) IncPolicyReload(string) {}

// 编译期断言：确保替身实现接口。
var _ ports.MetricsRecorder = (*recordingRecorder)(nil)

func TestMetrics_UsesRouteTemplateNotPath(t *testing.T) {
	rec := &recordingRecorder{}
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.GET("/api/v1/tenants/:tenantID/members", Metrics(rec), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/tenants/11111111-1111-1111-1111-111111111111/members", nil)
	router.ServeHTTP(httptest.NewRecorder(), req)

	require.Len(t, rec.calls, 1)
	assert.Equal(t, "/api/v1/tenants/:tenantID/members", rec.calls[0].route,
		"必须用路由模板而不是具体路径，否则标签基数会无限增长")
	assert.Equal(t, http.MethodGet, rec.calls[0].method)
	assert.Equal(t, http.StatusOK, rec.calls[0].status)
	assert.Greater(t, rec.calls[0].duration, time.Duration(0))
}

func TestMetrics_RecordsUnmatchedRoutes(t *testing.T) {
	rec := &recordingRecorder{}
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.NoRoute(Metrics(rec), func(c *gin.Context) { c.Status(http.StatusNotFound) })

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/nope", nil))

	require.Len(t, rec.calls, 1)
	assert.Equal(t, "unmatched", rec.calls[0].route, "未匹配路由也要有归属，否则监控会出现盲区")
}

func TestMetrics_RecordsRejectedRequests(t *testing.T) {
	rec := &recordingRecorder{}
	gin.SetMode(gin.TestMode)

	// 守卫拒绝发生在 Metrics 之后，因此必须被采集到——
	// 被拒绝的请求恰恰是最需要观测的部分。
	router := gin.New()
	router.GET("/resource",
		Metrics(rec),
		func(c *gin.Context) {
			c.AbortWithStatus(http.StatusForbidden)
		},
	)

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/resource", nil))

	require.Len(t, rec.calls, 1)
	assert.Equal(t, http.StatusForbidden, rec.calls[0].status)
}
