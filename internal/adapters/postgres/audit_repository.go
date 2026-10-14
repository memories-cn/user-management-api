package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/example/iam-service/internal/domain/audit"
	"github.com/example/iam-service/internal/ports"
)

// 编译期断言：确保实现 ports.AuditRepository。
var _ ports.AuditRepository = (*AuditRepository)(nil)

// auditLogModel 是 audit_logs 表的 GORM 映射。
//
// 审计记录只追加，因此没有 updated_at，也没有软删除字段。
type auditLogModel struct {
	ID           uuid.UUID  `gorm:"column:id;primaryKey"`
	TenantID     *uuid.UUID `gorm:"column:tenant_id"`
	ActorUserID  uuid.UUID  `gorm:"column:actor_user_id"`
	Action       string     `gorm:"column:action"`
	ResourceType string     `gorm:"column:resource_type"`
	ResourceID   string     `gorm:"column:resource_id"`
	Result       string     `gorm:"column:result"`
	RequestID    string     `gorm:"column:request_id"`
	IPAddress    string     `gorm:"column:ip_address"`
	UserAgent    string     `gorm:"column:user_agent"`
	Metadata     string     `gorm:"column:metadata"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
}

// TableName 指定表名。
func (auditLogModel) TableName() string { return "audit_logs" }

// AuditRepository 基于 GORM 提供审计记录的持久化。
type AuditRepository struct {
	db *gorm.DB
}

// NewAuditRepository 构造审计仓储。
func NewAuditRepository(db *gorm.DB) *AuditRepository {
	return &AuditRepository{db: db}
}

// Create 追加一条审计记录。
//
// 必须走 dbFrom(ctx)：审计与高风险操作必须同事务，
// 否则会出现"操作回滚了但留痕还在"的不一致。
func (r *AuditRepository) Create(ctx context.Context, e *audit.Entry) error {
	if e == nil {
		return fmt.Errorf("审计记录不能为空")
	}

	metadata, err := marshalMetadata(e.Metadata)
	if err != nil {
		return err
	}

	model := auditLogModel{
		ID:           e.ID,
		TenantID:     e.TenantID,
		ActorUserID:  e.ActorUserID,
		Action:       string(e.Action),
		ResourceType: e.ResourceType,
		ResourceID:   e.ResourceID,
		Result:       string(e.Result),
		RequestID:    e.RequestID,
		IPAddress:    e.IPAddress,
		UserAgent:    e.UserAgent,
		Metadata:     metadata,
		CreatedAt:    e.CreatedAt,
	}

	if err := dbFrom(ctx, r.db).WithContext(ctx).Create(&model).Error; err != nil {
		return fmt.Errorf("写入审计记录失败：%w", err)
	}
	return nil
}

// ListByTenant 按条件分页返回审计记录。
func (r *AuditRepository) ListByTenant(ctx context.Context, tenantID string, f ports.AuditFilter) ([]*audit.Entry, error) {
	var models []auditLogModel
	query := r.applyFilter(dbFrom(ctx, r.db).WithContext(ctx).
		Where("tenant_id = ?", tenantID), f)

	// 审计浏览以时间倒序为主，其次用主键保证顺序稳定。
	if err := query.
		Order("created_at DESC, id DESC").
		Limit(f.Limit).Offset(f.Offset).
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("查询审计记录失败：%w", err)
	}

	entries := make([]*audit.Entry, 0, len(models))
	for i := range models {
		entry, err := auditFromModel(&models[i])
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// CountByTenant 统计满足条件的记录数。
func (r *AuditRepository) CountByTenant(ctx context.Context, tenantID string, f ports.AuditFilter) (int64, error) {
	var count int64
	query := r.applyFilter(dbFrom(ctx, r.db).WithContext(ctx).
		Model(&auditLogModel{}).Where("tenant_id = ?", tenantID), f)

	if err := query.Count(&count).Error; err != nil {
		return 0, fmt.Errorf("统计审计记录失败：%w", err)
	}
	return count, nil
}

// applyFilter 把过滤条件叠加到查询上。
//
// 租户条件由调用方负责：审计查询绝不允许跨租户。
func (r *AuditRepository) applyFilter(query *gorm.DB, f ports.AuditFilter) *gorm.DB {
	if f.ActorUserID != "" {
		query = query.Where("actor_user_id = ?", f.ActorUserID)
	}
	if f.Action != "" {
		query = query.Where("action = ?", f.Action)
	}
	if f.ResourceType != "" {
		query = query.Where("resource_type = ?", f.ResourceType)
	}
	if f.Since != nil {
		query = query.Where("created_at >= ?", *f.Since)
	}
	if f.Until != nil {
		query = query.Where("created_at <= ?", *f.Until)
	}
	return query
}

// marshalMetadata 序列化元数据为 JSON 文本。
func marshalMetadata(metadata map[string]any) (string, error) {
	if len(metadata) == 0 {
		return "{}", nil
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("序列化审计元数据失败：%w", err)
	}
	return string(raw), nil
}

// auditFromModel 把数据库模型还原为领域实体。
func auditFromModel(m *auditLogModel) (*audit.Entry, error) {
	metadata := map[string]any{}
	if m.Metadata != "" && m.Metadata != "{}" {
		if err := json.Unmarshal([]byte(m.Metadata), &metadata); err != nil {
			return nil, fmt.Errorf("解析审计元数据失败：%w", err)
		}
	}

	return &audit.Entry{
		ID:           m.ID,
		TenantID:     copyUUID(m.TenantID),
		ActorUserID:  m.ActorUserID,
		Action:       audit.Action(m.Action),
		ResourceType: m.ResourceType,
		ResourceID:   m.ResourceID,
		Result:       audit.Result(m.Result),
		RequestID:    m.RequestID,
		IPAddress:    m.IPAddress,
		UserAgent:    m.UserAgent,
		Metadata:     metadata,
		CreatedAt:    m.CreatedAt,
	}, nil
}

// copyUUID 复制可空的 UUID 指针，避免持有模型内部的引用。
func copyUUID(v *uuid.UUID) *uuid.UUID {
	if v == nil {
		return nil
	}
	copied := *v
	return &copied
}
