package ports

import (
	"context"
	"time"

	"github.com/example/iam-service/internal/domain/audit"
)

// AuditFilter 描述审计记录的查询条件。
//
// 所有条件都作用于**单个租户内**；跨租户查询不提供，
// 平台侧统一检索属于后续里程碑。
type AuditFilter struct {
	// ActorUserID 按操作者过滤，空串表示不过滤。
	ActorUserID string
	// Action 按操作类型过滤，空串表示不过滤；取值必须是已知枚举。
	Action string
	// ResourceType 按资源类型过滤，空串表示不过滤。
	ResourceType string
	// Since 只返回该时刻之后的记录，nil 表示不限。
	Since *time.Time
	// Until 只返回该时刻之前的记录，nil 表示不限。
	Until *time.Time
	// Limit 与 Offset 控制分页。
	Limit  int
	Offset int
}

// AuditRepository 提供审计记录的追加与查询能力。
//
// 关键约定：Create **必须**走 dbFrom(ctx) 取事务句柄，
// 使审计记录与业务数据、Casbin 策略投影处于同一事务（docs/TDD.md 8.3）。
// 否则事务回滚后会留下"没有业务效果却有留痕"的脏数据。
type AuditRepository interface {
	// Create 追加一条审计记录。审计表只追加，不提供更新与删除。
	Create(ctx context.Context, e *audit.Entry) error
	// ListByTenant 按条件分页返回审计记录，必须带 tenant_id 条件。
	ListByTenant(ctx context.Context, tenantID string, f AuditFilter) ([]*audit.Entry, error)
	// CountByTenant 统计满足条件的记录数，用于分页信封的 total。
	CountByTenant(ctx context.Context, tenantID string, f AuditFilter) (int64, error)
}
