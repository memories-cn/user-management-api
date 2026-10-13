package bootstrap

import (
	"context"

	"gorm.io/gorm"

	"github.com/example/iam-service/internal/adapters/postgres"
	"github.com/example/iam-service/internal/adapters/zitadel"
	appuser "github.com/example/iam-service/internal/application/user"
	"github.com/example/iam-service/internal/config"
	"github.com/example/iam-service/internal/ports"
)

// NewTokenVerifier 完成 OIDC Discovery 并构造令牌验证器。
//
// Discovery 在启动阶段完成，失败即启动失败，避免把配置问题拖到首个请求才暴露。
func NewTokenVerifier(ctx context.Context, cfg config.Config) (ports.TokenVerifier, error) {
	return zitadel.NewTokenVerifier(ctx, cfg.ZitadelIssuer, cfg.ZitadelAPIAudience)
}

// NewUserRepository 构造基于 PostgreSQL 的用户仓储。
func NewUserRepository(db *gorm.DB) ports.UserRepository {
	return postgres.NewUserRepository(db)
}

// NewUserSync 构造用户按需同步用例。
func NewUserSync(repo ports.UserRepository) *appuser.SyncService {
	return appuser.NewSyncService(repo)
}
