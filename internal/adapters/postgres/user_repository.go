// Package postgres 提供基于 GORM 的持久化实现。
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/example/iam-service/internal/domain/user"
	"github.com/example/iam-service/internal/ports"
)

// userModel 是 users 表的 GORM 映射，仅在适配器内部使用，避免 GORM 标签污染领域实体。
type userModel struct {
	ID            uuid.UUID `gorm:"column:id;primaryKey"`
	ZitadelUserID string    `gorm:"column:zitadel_user_id"`
	DisplayName   string    `gorm:"column:display_name"`
	AvatarURL     string    `gorm:"column:avatar_url"`
	Status        string    `gorm:"column:status"`
	CreatedAt     time.Time `gorm:"column:created_at"`
	UpdatedAt     time.Time `gorm:"column:updated_at"`
}

// TableName 指定表名。
func (userModel) TableName() string { return "users" }

// UserRepository 基于 PostgreSQL 实现用户持久化。
type UserRepository struct {
	db *gorm.DB
}

// 编译期断言：确保实现 ports.UserRepository。
var _ ports.UserRepository = (*UserRepository)(nil)

// NewUserRepository 构造用户仓储。
func NewUserRepository(db *gorm.DB) *UserRepository {
	return &UserRepository{db: db}
}

// FindByZitadelUserID 依据外部身份标识查询用户，未命中返回 (nil, nil)。
func (r *UserRepository) FindByZitadelUserID(ctx context.Context, zitadelUserID string) (*user.User, error) {
	var m userModel
	err := r.db.WithContext(ctx).Where("zitadel_user_id = ?", zitadelUserID).Take(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 未命中不是错误，首次访问属于正常流程。
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询用户失败：%w", err)
	}
	return toDomain(m), nil
}

// Create 创建用户，唯一约束冲突由数据库保证并原样上抛。
func (r *UserRepository) Create(ctx context.Context, u *user.User) error {
	if u == nil {
		return errors.New("待创建的用户不能为空")
	}
	if err := r.db.WithContext(ctx).Create(fromDomain(u)).Error; err != nil {
		return fmt.Errorf("创建用户失败：%w", err)
	}
	return nil
}

// Update 更新用户资料。
func (r *UserRepository) Update(ctx context.Context, u *user.User) error {
	if u == nil {
		return errors.New("待更新的用户不能为空")
	}
	if err := r.db.WithContext(ctx).Save(fromDomain(u)).Error; err != nil {
		return fmt.Errorf("更新用户失败：%w", err)
	}
	return nil
}

// fromDomain 把领域实体转换为 GORM 模型。
func fromDomain(u *user.User) *userModel {
	return &userModel{
		ID:            u.ID,
		ZitadelUserID: u.ZitadelUserID,
		DisplayName:   u.DisplayName,
		AvatarURL:     u.AvatarURL,
		Status:        string(u.Status),
		CreatedAt:     u.CreatedAt,
		UpdatedAt:     u.UpdatedAt,
	}
}

// toDomain 把 GORM 模型转换为领域实体。
func toDomain(m userModel) *user.User {
	return &user.User{
		ID:            m.ID,
		ZitadelUserID: m.ZitadelUserID,
		DisplayName:   m.DisplayName,
		AvatarURL:     m.AvatarURL,
		Status:        user.Status(m.Status),
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
	}
}
