package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	appaudit "github.com/example/iam-service/internal/application/audit"
	"github.com/example/iam-service/internal/domain/audit"
	"github.com/example/iam-service/internal/ports"
	"github.com/example/iam-service/internal/transport/http/middleware"
	"github.com/example/iam-service/internal/transport/http/responses"
)

// auditLogResponse 是审计记录的响应体。
type auditLogResponse struct {
	ID           string         `json:"id"`
	TenantID     string         `json:"tenant_id,omitempty"`
	ActorUserID  string         `json:"actor_user_id"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resource_type"`
	ResourceID   string         `json:"resource_id"`
	Result       string         `json:"result"`
	RequestID    string         `json:"request_id,omitempty"`
	IPAddress    string         `json:"ip_address,omitempty"`
	UserAgent    string         `json:"user_agent,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	CreatedAt    string         `json:"created_at"`
}

// AuditHandler 提供审计日志查询接口。
type AuditHandler struct {
	audits *appaudit.Service
}

// NewAuditHandler 构造审计处理器。
func NewAuditHandler(audits *appaudit.Service) *AuditHandler {
	return &AuditHandler{audits: audits}
}

// ListAuditLogs 返回当前租户的审计记录。
//
// 访问受独立权限 audit:read 保护——审计日志覆盖整个租户的操作留痕，
// 不应因为"能管理成员"就自动获得读取能力（docs/TDD.md 10.4）。
func (h *AuditHandler) ListAuditLogs(c *gin.Context) {
	tenantID, ok := middleware.TenantIDFrom(c.Request.Context())
	if !ok {
		responses.Fail(c, http.StatusNotFound, CodeNotFound, "resource not found")
		return
	}

	filter, ok := parseAuditFilter(c)
	if !ok {
		return
	}

	result, err := h.audits.List(c.Request.Context(), tenantID, filter)
	if err != nil {
		writeError(c, err)
		return
	}

	items := make([]auditLogResponse, 0, len(result.Entries))
	for _, entry := range result.Entries {
		items = append(items, toAuditLogResponse(entry))
	}
	responses.OKList(c, items, result.Limit, result.Offset, result.Total)
}

// parseAuditFilter 解析并校验审计查询条件。
//
// 非法操作类型与非法时间区间都返回 400：静默忽略会让管理员误以为"没有这类操作"。
func parseAuditFilter(c *gin.Context) (ports.AuditFilter, bool) {
	since, ok := parseTimeParam(c, "since")
	if !ok {
		return ports.AuditFilter{}, false
	}
	until, ok := parseTimeParam(c, "until")
	if !ok {
		return ports.AuditFilter{}, false
	}

	limit, offset := parsePaging(c)
	return ports.AuditFilter{
		ActorUserID:  c.Query("actor_user_id"),
		Action:       c.Query("action"),
		ResourceType: c.Query("resource_type"),
		Since:        since,
		Until:        until,
		Limit:        limit,
		Offset:       offset,
	}, true
}

// parseTimeParam 解析 RFC3339 时间参数。
func parseTimeParam(c *gin.Context, name string) (*time.Time, bool) {
	parsed, err := appaudit.ParseTime(c.Query(name))
	if err != nil {
		responses.Fail(c, http.StatusBadRequest, CodeAuditFilterInvalid, "invalid "+name+" parameter")
		return nil, false
	}
	return parsed, true
}

// toAuditLogResponse 转换审计记录响应。
func toAuditLogResponse(entry *audit.Entry) auditLogResponse {
	resp := auditLogResponse{
		ID:           entry.ID.String(),
		ActorUserID:  entry.ActorUserID.String(),
		Action:       string(entry.Action),
		ResourceType: entry.ResourceType,
		ResourceID:   entry.ResourceID,
		Result:       string(entry.Result),
		RequestID:    entry.RequestID,
		IPAddress:    entry.IPAddress,
		UserAgent:    entry.UserAgent,
		Metadata:     entry.Metadata,
		CreatedAt:    entry.CreatedAt.Format(dateLayout),
	}
	if entry.TenantID != nil {
		resp.TenantID = entry.TenantID.String()
	}
	return resp
}
