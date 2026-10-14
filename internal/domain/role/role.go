// Package role 定义租户内角色实体、成员角色分配与相关不变量。
//
// 角色归属于租户；系统预置角色（owner/admin/member/viewer）不可删除、不可改 key，
// 只允许调整展示信息与权限集合（见 docs/TDD.md 3.4、10.2）。
//
// 分层约束：本包属于 Domain，不得依赖 Gin、GORM、Redis 或 Casbin。
package role

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// 系统预置角色标识。这些是业务角色，不是 ZITADEL 管理员角色。
const (
	// KeyOwner 是租户所有者，可管理租户及成员。
	KeyOwner = "owner"
	// KeyAdmin 是租户管理员，可管理大部分租户资源。
	KeyAdmin = "admin"
	// KeyMember 是普通成员，可执行日常操作。
	KeyMember = "member"
	// KeyViewer 是只读用户。
	KeyViewer = "viewer"
)

// SystemKeys 是系统预置角色标识集合。
var SystemKeys = map[string]struct{}{
	KeyOwner: {}, KeyAdmin: {}, KeyMember: {}, KeyViewer: {},
}

var (
	// ErrInvalidTenantID 表示角色缺少租户归属。
	ErrInvalidTenantID = errors.New("角色必须归属于指定租户")
	// ErrInvalidKey 表示角色标识非法。
	ErrInvalidKey = errors.New("角色标识格式非法")
	// ErrInvalidName 表示角色名称非法。
	ErrInvalidName = errors.New("角色名称不能为空")
	// ErrSystemRoleImmutable 表示系统预置角色不允许该变更。
	ErrSystemRoleImmutable = errors.New("系统预置角色不可删除且不可修改标识")
)

// 角色标识与名称的长度约束。
const (
	maxKeyLen  = 64
	maxNameLen = 255
)

// keyPattern 限定角色标识只含小写字母、数字与下划线，且以小写字母开头。
var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Role 是租户内的角色定义。
type Role struct {
	// ID 是本地业务主键。
	ID uuid.UUID
	// TenantID 是角色所属租户，角色不得跨租户使用。
	TenantID uuid.UUID
	// Key 是租户内唯一的机器可读标识。
	Key string
	// Name 是展示名称。
	Name string
	// Description 是角色说明。
	Description string
	// IsSystem 标记系统预置角色。
	IsSystem bool
	// CreatedAt、UpdatedAt 由领域层初始化，持久化时由数据库最终确定。
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NormalizeKey 归一化角色标识：去空白并转小写。
func NormalizeKey(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}

// IsSystemKey 报告给定标识是否为系统预置角色。
func IsSystemKey(key string) bool {
	_, ok := SystemKeys[NormalizeKey(key)]
	return ok
}

// NewRole 构造角色并校验租户、标识与名称。
//
// key 命中系统预置角色集合时自动标记为系统角色，保证预置角色的不可变性无法被绕过。
func NewRole(tenantID uuid.UUID, key, name, description string) (*Role, error) {
	if tenantID == uuid.Nil {
		return nil, ErrInvalidTenantID
	}

	key = NormalizeKey(key)
	if key == "" || len(key) > maxKeyLen || !keyPattern.MatchString(key) {
		return nil, ErrInvalidKey
	}

	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxNameLen {
		return nil, ErrInvalidName
	}

	now := time.Now()
	return &Role{
		ID:          uuid.New(),
		TenantID:    tenantID,
		Key:         key,
		Name:        name,
		Description: strings.TrimSpace(description),
		IsSystem:    IsSystemKey(key),
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// NewSystemRole 构造系统预置角色，is_system 恒为 true。
func NewSystemRole(tenantID uuid.UUID, key, name, description string) (*Role, error) {
	if !IsSystemKey(key) {
		return nil, ErrInvalidKey
	}
	return NewRole(tenantID, key, name, description)
}

// Rename 更新角色名称，返回是否发生变更。
func (r *Role) Rename(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || name == r.Name {
		return false
	}
	r.Name = name
	r.UpdatedAt = time.Now()
	return true
}

// SetDescription 更新角色说明，返回是否发生变更。
func (r *Role) SetDescription(description string) bool {
	description = strings.TrimSpace(description)
	if description == r.Description {
		return false
	}
	r.Description = description
	r.UpdatedAt = time.Now()
	return true
}

// EnsureDeletable 校验角色是否可删除，系统角色返回 ErrSystemRoleImmutable。
func (r *Role) EnsureDeletable() error {
	if r.IsSystem {
		return ErrSystemRoleImmutable
	}
	return nil
}
