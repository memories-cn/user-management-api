package role

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrInvalidMembership 表示分配缺少成员关系。
	ErrInvalidMembership = errors.New("角色分配必须关联指定成员关系")
	// ErrInvalidRole 表示分配缺少角色。
	ErrInvalidRole = errors.New("角色分配必须关联指定角色")
	// ErrInvalidAssigner 表示缺少分配者。
	ErrInvalidAssigner = errors.New("角色分配必须记录分配者")
)

// Assignment 表示一个成员被授予一个租户角色（对应 membership_roles 表）。
//
// TenantID 是冗余字段：它与 MembershipID、RoleID 一同构成复合外键，
// 让数据库强制"成员关系与角色属于同一个租户"（见 docs/TDD.md 3.6、8.2）。
// 仅检查两个 UUID 是否存在是不够的。
type Assignment struct {
	// MembershipID 是被授予角色的成员关系。
	MembershipID uuid.UUID
	// RoleID 是被授予的角色。
	RoleID uuid.UUID
	// TenantID 是租户上下文，必须与成员关系和角色一致。
	TenantID uuid.UUID
	// AssignedBy 记录分配者，必须是内部用户 ID，不接受请求体传入。
	AssignedBy uuid.UUID
	// CreatedAt 是分配时间。
	CreatedAt time.Time
}

// NewAssignment 构造成员角色分配并校验必填标识。
func NewAssignment(tenantID, membershipID, roleID, assignedBy uuid.UUID) (*Assignment, error) {
	if tenantID == uuid.Nil || membershipID == uuid.Nil {
		return nil, ErrInvalidMembership
	}
	if roleID == uuid.Nil {
		return nil, ErrInvalidRole
	}
	if assignedBy == uuid.Nil {
		return nil, ErrInvalidAssigner
	}
	return &Assignment{
		MembershipID: membershipID,
		RoleID:       roleID,
		TenantID:     tenantID,
		AssignedBy:   assignedBy,
		CreatedAt:    time.Now(),
	}, nil
}
