// Package membership 定义用户与租户的成员关系实体与状态机。
//
// 分层约束：本包属于 Domain，不得依赖 Gin、GORM、Redis 或 Casbin。
package membership

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Status 表示成员关系状态。
type Status string

const (
	// StatusActive 表示成员可用，可访问该租户的业务资源。
	StatusActive Status = "active"
	// StatusPending 表示已邀请但尚未完成绑定。
	StatusPending Status = "pending"
	// StatusSuspended 表示成员被临时停用。
	StatusSuspended Status = "suspended"
	// StatusRemoved 表示成员已被移除（软删除）。
	StatusRemoved Status = "removed"
)

var (
	// ErrInvalidTenantID 表示租户标识缺失。
	ErrInvalidTenantID = errors.New("tenant_id 不能为空")
	// ErrInvalidUserID 表示用户标识缺失。
	ErrInvalidUserID = errors.New("user_id 不能为空")
)

// Membership 表示 User 与 Tenant 的关联，是租户权限的核心边界。
//
// 采用软删除：移除只改状态，行保留以维持 (tenant_id, user_id) 唯一约束。
type Membership struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	UserID   uuid.UUID
	Status   Status
	// JoinedAt 表示加入时间，被移除后清空，重新加入时刷新。
	JoinedAt *time.Time
	// CreatedAt、UpdatedAt 由领域层初始化。
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewMembership 构造一条 active 成员关系。
func NewMembership(tenantID, userID uuid.UUID) (*Membership, error) {
	if tenantID == uuid.Nil {
		return nil, ErrInvalidTenantID
	}
	if userID == uuid.Nil {
		return nil, ErrInvalidUserID
	}

	now := time.Now()
	return &Membership{
		ID:        uuid.New(),
		TenantID:  tenantID,
		UserID:    userID,
		Status:    StatusActive,
		JoinedAt:  &now,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// IsActive 报告成员关系是否可用。
func (m *Membership) IsActive() bool {
	return m.Status == StatusActive
}

// Suspend 停用成员。
func (m *Membership) Suspend() {
	m.Status = StatusSuspended
	m.UpdatedAt = time.Now()
}

// Activate 恢复成员为可用状态。
func (m *Membership) Activate() {
	m.Status = StatusActive
	m.UpdatedAt = time.Now()
}

// Remove 移除成员（软删除），重复调用无副作用。
func (m *Membership) Remove() {
	m.Status = StatusRemoved
	m.JoinedAt = nil
	m.UpdatedAt = time.Now()
}

// Rejoin 重新激活已被移除的成员并刷新加入时间。
func (m *Membership) Rejoin(now time.Time) {
	m.Status = StatusActive
	m.JoinedAt = &now
	m.UpdatedAt = now
}
