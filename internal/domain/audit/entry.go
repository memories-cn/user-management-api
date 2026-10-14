// Package audit 定义审计记录的实体与不变量。
//
// 分层约束：本包属于 Domain，不得依赖 Gin、GORM、Redis 或 Casbin。
package audit

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Action 是被审计的高风险操作类型。
//
// 取值形态与权限代码保持一致（<资源>.<动作>），便于管理员对照权限目录理解留痕。
type Action string

// 被审计的操作清单（对应 PRD 高风险操作审计覆盖率要求）。
const (
	// ActionMemberInvite 表示发起成员邀请。
	ActionMemberInvite Action = "member.invite"
	// ActionMemberRemove 表示移除成员。
	ActionMemberRemove Action = "member.remove"
	// ActionMemberStatusChange 表示成员状态变更（停用/恢复）。
	ActionMemberStatusChange Action = "member.status_change"
	// ActionRoleAssign 表示成员角色分配或撤销。
	ActionRoleAssign Action = "role.assign"
	// ActionRoleCreate 表示创建租户自定义角色。
	ActionRoleCreate Action = "role.create"
	// ActionRoleUpdate 表示修改角色展示信息。
	ActionRoleUpdate Action = "role.update"
	// ActionRoleDelete 表示删除角色。
	ActionRoleDelete Action = "role.delete"
	// ActionRolePermissionUpdate 表示变更角色权限集合。
	ActionRolePermissionUpdate Action = "role.permission_update"
	// ActionTenantCreate 表示平台管理员创建租户。
	ActionTenantCreate Action = "tenant.create"
	// ActionTenantSuspend 表示平台管理员停用租户。
	ActionTenantSuspend Action = "tenant.suspend"
	// ActionTenantActivate 表示平台管理员恢复租户。
	ActionTenantActivate Action = "tenant.activate"
)

// Result 描述操作的执行结果。
type Result string

// 结果枚举。
const (
	// ResultSuccess 表示操作已成功执行。
	ResultSuccess Result = "success"
	// ResultDenied 表示操作因权限或业务规则被拒绝。
	ResultDenied Result = "denied"
	// ResultError 表示操作因依赖故障未能完成。
	ResultError Result = "error"
)

var (
	// ErrInvalidAction 表示操作类型为空或不在已知清单内。
	ErrInvalidAction = errors.New("非法的审计操作类型")
	// ErrInvalidActor 表示操作者缺失。
	ErrInvalidActor = errors.New("审计记录的操作者不能为空")
	// ErrInvalidResourceType 表示资源类型为空。
	ErrInvalidResourceType = errors.New("审计记录的资源类型不能为空")
	// ErrInvalidResult 表示结果不在枚举内。
	ErrInvalidResult = errors.New("非法的审计结果")
	// ErrSensitiveMetadata 表示元数据包含疑似凭据的键名。
	ErrSensitiveMetadata = errors.New("审计元数据包含敏感键名")
	// ErrUnsupportedMetadataValue 表示元数据的值不是可安全序列化的标量。
	ErrUnsupportedMetadataValue = errors.New("审计元数据的值类型不受支持")
)

// sensitiveKeys 是禁止出现在元数据中的键名片段。
//
// 依据 docs/TDD.md 3.7：metadata 不得包含 Access Token、Refresh Token、
// 密码或其他敏感认证材料。只记录"改了什么"，绝不记录"凭什么改的"。
var sensitiveKeys = []string{
	"token", "password", "secret", "authorization",
	"credential", "refresh", "cookie", "private_key",
}

// maxUserAgentLength 是 user_agent 的截断长度，避免超长请求头撑爆字段。
const maxUserAgentLength = 512

// Entry 是一条审计记录。
//
// 审计记录只追加：本类型不提供任何变更字段的方法，
// 元数据也只能在构造后一次性写入并经过脱敏校验。
type Entry struct {
	// ID 是记录主键。
	ID uuid.UUID
	// TenantID 是操作所属租户；平台级操作为 nil。
	TenantID *uuid.UUID
	// ActorUserID 是执行操作的内部用户 ID。
	ActorUserID uuid.UUID
	// Action 是被审计的操作类型。
	Action Action
	// ResourceType 是操作对象的类型，例如 member、role、tenant。
	ResourceType string
	// ResourceID 是操作对象的标识。
	ResourceID string
	// Result 是操作结果。
	Result Result
	// RequestID 用于把审计记录与一次请求关联起来。
	RequestID string
	// IPAddress 是来源地址，仅作辅助线索，不作为授权依据。
	IPAddress string
	// UserAgent 是客户端标识，写入前会被截断。
	UserAgent string
	// Metadata 是附加结构化信息，不含凭据。
	Metadata map[string]any
	// CreatedAt 是记录写入时间。
	CreatedAt time.Time
}

// NewEntry 构造一条审计记录并校验必填不变量。
//
// tenantID 为 nil 表示平台级操作；actorUserID 必填——无法归因的操作没有审计价值。
func NewEntry(
	tenantID *uuid.UUID,
	actorUserID uuid.UUID,
	action Action,
	resourceType, resourceID string,
	result Result,
) (*Entry, error) {
	if actorUserID == uuid.Nil {
		return nil, ErrInvalidActor
	}
	if !action.IsValid() {
		return nil, fmt.Errorf("%w：%s", ErrInvalidAction, action)
	}
	resourceType = strings.TrimSpace(resourceType)
	if resourceType == "" {
		return nil, ErrInvalidResourceType
	}
	if !result.IsValid() {
		return nil, fmt.Errorf("%w：%s", ErrInvalidResult, result)
	}

	return &Entry{
		ID:           uuid.New(),
		TenantID:     copyTenantID(tenantID),
		ActorUserID:  actorUserID,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   strings.TrimSpace(resourceID),
		Result:       result,
		Metadata:     map[string]any{},
		CreatedAt:    time.Now(),
	}, nil
}

// WithMetadata 写入附加信息，键名含疑似凭据片段或值类型不受支持时整次拒绝。
//
// 只接受标量值：结构体容易在序列化时意外带出内部字段。
func (e *Entry) WithMetadata(metadata map[string]any) error {
	if len(metadata) == 0 {
		return nil
	}

	normalized := make(map[string]any, len(metadata))
	for key, value := range metadata {
		if err := validateMetadataKey(key); err != nil {
			return err
		}
		if err := validateMetadataValue(value); err != nil {
			return fmt.Errorf("%w：%s", err, key)
		}
		normalized[key] = value
	}

	if e.Metadata == nil {
		e.Metadata = map[string]any{}
	}
	for key, value := range normalized {
		e.Metadata[key] = value
	}
	return nil
}

// WithRequestContext 补充请求上下文（request_id、来源地址、客户端标识）。
func (e *Entry) WithRequestContext(ctx RequestContext) {
	e.RequestID = strings.TrimSpace(ctx.RequestID)
	e.IPAddress = strings.TrimSpace(ctx.IPAddress)
	e.UserAgent = truncate(ctx.UserAgent, maxUserAgentLength)
}

// IsValid 报告操作类型是否在已知清单内。
func (a Action) IsValid() bool {
	switch a {
	case ActionMemberInvite, ActionMemberRemove, ActionMemberStatusChange,
		ActionRoleAssign, ActionRoleCreate, ActionRoleUpdate, ActionRoleDelete,
		ActionRolePermissionUpdate,
		ActionTenantCreate, ActionTenantSuspend, ActionTenantActivate:
		return true
	}
	return false
}

// IsValid 报告结果是否在枚举内。
func (r Result) IsValid() bool {
	switch r {
	case ResultSuccess, ResultDenied, ResultError:
		return true
	}
	return false
}

// validateMetadataKey 校验键名不含敏感片段。
func validateMetadataKey(key string) error {
	lower := strings.ToLower(strings.TrimSpace(key))
	if lower == "" {
		return ErrSensitiveMetadata
	}
	for _, sensitive := range sensitiveKeys {
		if strings.Contains(lower, sensitive) {
			return fmt.Errorf("%w：%s", ErrSensitiveMetadata, key)
		}
	}
	return nil
}

// validateMetadataValue 只接受可安全序列化的标量。
func validateMetadataValue(value any) error {
	switch value.(type) {
	case nil, string, bool, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64, float32, float64:
		return nil
	}
	return ErrUnsupportedMetadataValue
}

// copyTenantID 复制租户标识，避免调用方后续修改指针影响已构造的记录。
func copyTenantID(tenantID *uuid.UUID) *uuid.UUID {
	if tenantID == nil {
		return nil
	}
	copied := *tenantID
	return &copied
}

// truncate 截断字符串到指定长度。
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
