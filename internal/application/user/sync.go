// Package user 编排业务用户相关用例。
//
// 分层约束：本包属于 Application，只依赖 ports 定义的接口，
// 不直接依赖 GORM、Gin 或 Casbin。
package user

import (
	"context"
	"errors"
	"fmt"

	"github.com/example/iam-service/internal/domain/user"
	"github.com/example/iam-service/internal/ports"
)

// ErrUserDisabled 表示用户已被停用，不得访问受保护资源。
var ErrUserDisabled = errors.New("user is disabled")

// ErrInvalidClaims 表示可信声明缺少必需的外部身份标识。
var ErrInvalidClaims = errors.New("claims 缺少 sub")

// SyncService 实现用户按需同步：首次访问时创建本地映射，后续访问按需更新展示资料。
type SyncService struct {
	repo ports.UserRepository
}

// NewSyncService 构造用户同步服务。
func NewSyncService(repo ports.UserRepository) *SyncService {
	return &SyncService{repo: repo}
}

// Ensure 依据已验证的声明保证本地用户存在且可用。
//
// 约定：
//   - 主键是 Token 中的 sub，禁止用邮箱等可变更属性作为关联键；
//   - 展示资料只来自已验证的声明，且仅在发生变化时才写库；
//   - 用户被停用返回 ErrUserDisabled，由传输层映射为 403；
//   - 仓储错误原样上抛，由传输层映射为 503（依赖不可用不得放行）。
func (s *SyncService) Ensure(ctx context.Context, claims *ports.Claims) (*user.User, error) {
	if claims == nil || claims.Subject == "" {
		return nil, ErrInvalidClaims
	}

	existing, err := s.repo.FindByZitadelUserID(ctx, claims.Subject)
	if err != nil {
		return nil, err
	}

	if existing == nil {
		return s.create(ctx, claims)
	}

	if !existing.IsActive() {
		return nil, ErrUserDisabled
	}

	// 仅在展示资料确实发生变化时写库，避免每个请求都产生一次更新。
	renamed := existing.Rename(claims.DisplayName)
	avatarChanged := existing.SetAvatar(claims.AvatarURL)
	if renamed || avatarChanged {
		if err := s.repo.Update(ctx, existing); err != nil {
			return nil, fmt.Errorf("更新用户资料失败：%w", err)
		}
	}
	return existing, nil
}

// create 首次访问时创建本地业务用户映射。
func (s *SyncService) create(ctx context.Context, claims *ports.Claims) (*user.User, error) {
	created, err := user.NewUser(claims.Subject)
	if err != nil {
		return nil, err
	}
	created.Rename(claims.DisplayName)
	created.SetAvatar(claims.AvatarURL)

	if err := s.repo.Create(ctx, created); err != nil {
		return nil, err
	}
	return created, nil
}
