package ports

import (
	"context"

	"github.com/example/iam-service/internal/domain/user"
)

// UserRepository 提供业务用户的持久化能力。
//
// 约定：查询未命中时返回 (nil, nil)，未命中不是错误，避免把首次访问当成故障。
type UserRepository interface {
	// FindByZitadelUserID 依据外部身份标识查找用户，未命中返回 (nil, nil)。
	FindByZitadelUserID(ctx context.Context, zitadelUserID string) (*user.User, error)
	// Create 创建用户，唯一约束冲突应返回可被识别为冲突的错误。
	Create(ctx context.Context, u *user.User) error
	// Update 更新用户。
	Update(ctx context.Context, u *user.User) error
}
