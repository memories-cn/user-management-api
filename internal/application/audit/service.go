// Package audit 编排审计记录的写入与查询。
//
// 设计要点：
//   - 记录高风险操作时，审计写入必须落在调用方**已经开启的事务**内，
//     从而与业务数据、Casbin 策略投影共享原子性（docs/TDD.md 8.3）；
//   - 审计写入失败必须让整个高风险操作失败（fail closed）——
//     "改了权限但没留痕"比"操作失败"更不可接受。
//
// 分层约束：本包属于 Application，只依赖 ports 定义的接口。
package audit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/example/iam-service/internal/domain/audit"
	"github.com/example/iam-service/internal/ports"
)

var (
	// ErrInvalidAction 表示查询条件中的操作类型不在已知清单内。
	ErrInvalidAction = errors.New("非法的操作类型")
	// ErrInvalidTimeRange 表示时间区间非法。
	ErrInvalidTimeRange = errors.New("非法的审计时间区间")
)

// 分页默认值与上限，与管理类列表接口保持一致。
const (
	DefaultLimit = 50
	MaxLimit     = 100
)

// Service 提供审计记录的写入与查询。
type Service struct {
	repo ports.AuditRepository
}

// NewService 构造审计服务。
func NewService(repo ports.AuditRepository) *Service {
	return &Service{repo: repo}
}

// ListResult 是审计查询的分页结果。
type ListResult struct {
	// Entries 是当页记录。
	Entries []*audit.Entry
	// Total 是满足条件的记录总数，用于分页信封。
	Total int64
	// Limit 与 Offset 是实际生效的分页参数。
	Limit  int
	Offset int
}

// Record 在当前事务内追加一条审计记录。
//
// ctx 必须携带业务事务（由调用方的 WithinTx 提供）；
// 审计上下文缺失时只降级为不记录请求信息，不影响业务。
func (s *Service) Record(ctx context.Context, entry *audit.Entry) error {
	if s == nil || s.repo == nil {
		// 审计未装配时记录告警：宁可显式暴露问题，也不静默丢留痕。
		slog.ErrorContext(ctx, "审计仓储未装配，高风险操作未能留痕",
			"action", string(entry.Action))
		return errors.New("审计仓储未装配")
	}
	if entry == nil {
		return errors.New("审计记录不能为空")
	}

	if rc, ok := audit.RequestContextFrom(ctx); ok {
		entry.WithRequestContext(rc)
	}

	if err := s.repo.Create(ctx, entry); err != nil {
		return fmt.Errorf("写入审计记录失败：%w", err)
	}

	slog.InfoContext(ctx, "高风险操作已留痕",
		"action", string(entry.Action),
		"resource_type", entry.ResourceType,
		"resource_id", entry.ResourceID,
		"actor_user_id", entry.ActorUserID.String(),
		"request_id", entry.RequestID)
	return nil
}

// RecordAction 构造并写入一条审计记录，供用例层一行调用。
//
// tenantID 为 nil 表示平台级操作。metadata 可为 nil。
func (s *Service) RecordAction(
	ctx context.Context,
	tenantID *uuid.UUID,
	actorUserID uuid.UUID,
	action audit.Action,
	resourceType, resourceID string,
	result audit.Result,
	metadata map[string]any,
) error {
	entry, err := audit.NewEntry(tenantID, actorUserID, action, resourceType, resourceID, result)
	if err != nil {
		return err
	}
	if err := entry.WithMetadata(metadata); err != nil {
		return err
	}
	return s.Record(ctx, entry)
}

// List 查询指定租户的审计记录。
func (s *Service) List(ctx context.Context, tenantID string, f ports.AuditFilter) (*ListResult, error) {
	normalized, err := normalizeFilter(f)
	if err != nil {
		return nil, err
	}

	entries, err := s.repo.ListByTenant(ctx, tenantID, normalized)
	if err != nil {
		return nil, err
	}
	total, err := s.repo.CountByTenant(ctx, tenantID, normalized)
	if err != nil {
		return nil, err
	}

	return &ListResult{
		Entries: entries,
		Total:   total,
		Limit:   normalized.Limit,
		Offset:  normalized.Offset,
	}, nil
}

// normalizeFilter 校验并约束查询条件。
//
// 非法操作类型必须报错而不是静默忽略——忽略会让管理员误以为"没有这类操作"。
func normalizeFilter(f ports.AuditFilter) (ports.AuditFilter, error) {
	if f.Action != "" && !audit.Action(f.Action).IsValid() {
		return ports.AuditFilter{}, fmt.Errorf("%w：%s", ErrInvalidAction, f.Action)
	}
	if f.Since != nil && f.Until != nil && f.Since.After(*f.Until) {
		return ports.AuditFilter{}, fmt.Errorf("%w：起始时间晚于结束时间", ErrInvalidTimeRange)
	}

	switch {
	case f.Limit <= 0:
		f.Limit = DefaultLimit
	case f.Limit > MaxLimit:
		f.Limit = MaxLimit
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	return f, nil
}

// ParseTime 解析 RFC3339 时间参数，空串返回 nil。
func ParseTime(raw string) (*time.Time, error) {
	raw = trimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("%w：%s", ErrInvalidTimeRange, raw)
	}
	return &parsed, nil
}

// trimSpace 去除首尾空白。
func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
