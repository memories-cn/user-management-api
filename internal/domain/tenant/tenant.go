// Package tenant 定义业务租户实体与不变量。
//
// 分层约束：本包属于 Domain，不得依赖 Gin、GORM、Redis 或 Casbin。
package tenant

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Status 表示租户状态。
type Status string

const (
	// StatusActive 表示租户可用。
	StatusActive Status = "active"
	// StatusSuspended 表示租户被停用，成员不得继续执行租户级操作。
	StatusSuspended Status = "suspended"
	// StatusDeleted 表示租户已被删除（软删除）。
	StatusDeleted Status = "deleted"
)

var (
	// ErrInvalidName 表示租户名称非法。
	ErrInvalidName = errors.New("租户名称不能为空")
	// ErrInvalidSlug 表示租户标识非法或命中保留字。
	ErrInvalidSlug = errors.New("slug 格式非法或命中保留字")
	// ErrInvalidCreator 表示创建者缺失。
	ErrInvalidCreator = errors.New("created_by 不能为空")
)

// maxNameLen 与 slug 格式约束。
const maxNameLen = 255

// slugPattern 限定 slug 只含小写字母、数字与连字符，且不以连字符开头或结尾。
var slugPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// reservedSlugs 是保留标识，避免与系统路径或常见管理入口冲突。
var reservedSlugs = map[string]struct{}{
	"admin": {}, "api": {}, "www": {}, "iam": {}, "auth": {},
	"console": {}, "system": {}, "platform": {}, "root": {}, "help": {},
}

// Tenant 是业务租户实体。
type Tenant struct {
	// ID 是本地业务主键。
	ID uuid.UUID
	// Name 是展示名称。
	Name string
	// Slug 是可读且全局唯一的标识。
	Slug string
	// Status 决定成员能否执行租户级操作。
	Status Status
	// CreatedBy 记录创建者（平台管理员），不代表其是租户成员。
	CreatedBy uuid.UUID
	// CreatedAt、UpdatedAt 由领域层初始化，持久化时由数据库最终确定。
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NormalizeSlug 归一化租户标识：去空白并转小写。
func NormalizeSlug(slug string) string {
	return strings.ToLower(strings.TrimSpace(slug))
}

// NewTenant 构造租户并校验名称、标识与创建者。
func NewTenant(name, slug string, createdBy uuid.UUID) (*Tenant, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxNameLen {
		return nil, ErrInvalidName
	}

	slug = NormalizeSlug(slug)
	if !slugPattern.MatchString(slug) {
		return nil, ErrInvalidSlug
	}
	if _, reserved := reservedSlugs[slug]; reserved {
		return nil, ErrInvalidSlug
	}

	if createdBy == uuid.Nil {
		return nil, ErrInvalidCreator
	}

	now := time.Now()
	return &Tenant{
		ID:        uuid.New(),
		Name:      name,
		Slug:      slug,
		Status:    StatusActive,
		CreatedBy: createdBy,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// IsActive 报告租户是否可用。
func (t *Tenant) IsActive() bool {
	return t.Status == StatusActive
}

// Rename 更新租户名称，返回是否发生变更。
func (t *Tenant) Rename(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || name == t.Name {
		return false
	}
	t.Name = name
	t.UpdatedAt = time.Now()
	return true
}

// Suspend 停用租户。该能力供平台管理接口使用（Milestone 5）。
func (t *Tenant) Suspend() {
	t.Status = StatusSuspended
	t.UpdatedAt = time.Now()
}
