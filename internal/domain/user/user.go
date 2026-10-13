// Package user 定义业务用户实体与不变量。
//
// 分层约束：本包属于 Domain，不得依赖 Gin、GORM、Redis 或 Casbin。
// 用户不持有认证凭据，身份的权威来源是 ZITADEL，本地只保存业务侧映射与展示资料。
package user

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Status 表示用户在业务侧的状态。
type Status string

const (
	// StatusActive 表示用户可正常使用业务功能。
	StatusActive Status = "active"
	// StatusDisabled 表示用户被停用，禁止访问受保护资源。
	StatusDisabled Status = "disabled"
)

// ErrInvalidZitadelUserID 表示外部身份标识非法。
var ErrInvalidZitadelUserID = errors.New("zitadel_user_id 不能为空")

// User 是业务用户实体，与 ZITADEL 用户通过 ZitadelUserID 关联。
type User struct {
	// ID 是本地业务主键。
	ID uuid.UUID
	// ZitadelUserID 是外部身份唯一标识，也是同步的幂等依据。
	ZitadelUserID string
	// DisplayName 与 AvatarURL 为展示资料，权威值在 ZITADEL 侧。
	DisplayName string
	AvatarURL   string
	// Status 决定用户能否访问受保护资源。
	Status Status
	// CreatedAt、UpdatedAt 由领域层初始化，持久化时由数据库最终确定。
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewUser 依据外部身份标识创建业务用户。
func NewUser(zitadelUserID string) (*User, error) {
	zitadelUserID = strings.TrimSpace(zitadelUserID)
	if zitadelUserID == "" {
		return nil, ErrInvalidZitadelUserID
	}

	now := time.Now()
	return &User{
		ID:            uuid.New(),
		ZitadelUserID: zitadelUserID,
		Status:        StatusActive,
		CreatedAt:     now,
		UpdatedAt:     now,
	}, nil
}

// IsActive 报告用户是否处于可用状态。
func (u *User) IsActive() bool {
	return u.Status == StatusActive
}

// Disable 停用用户，重复调用无副作用。
func (u *User) Disable() {
	u.Status = StatusDisabled
	u.UpdatedAt = time.Now()
}

// Enable 恢复用户为可用状态。
func (u *User) Enable() {
	u.Status = StatusActive
	u.UpdatedAt = time.Now()
}

// Rename 更新展示名称，返回是否发生变更。
// 空白值不覆盖已有名称，避免身份平台返回空字段时抹掉本地资料。
func (u *User) Rename(displayName string) bool {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" || displayName == u.DisplayName {
		return false
	}
	u.DisplayName = displayName
	u.UpdatedAt = time.Now()
	return true
}

// SetAvatar 更新头像地址，返回是否发生变更。
func (u *User) SetAvatar(avatarURL string) bool {
	avatarURL = strings.TrimSpace(avatarURL)
	if avatarURL == "" || avatarURL == u.AvatarURL {
		return false
	}
	u.AvatarURL = avatarURL
	u.UpdatedAt = time.Now()
	return true
}
