package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/casbin/casbin/v3"
	"gorm.io/gorm"

	"github.com/example/iam-service/internal/config"
)

// App 聚合进程运行期依赖，提供统一的就绪检查入口。
type App struct {
	// DB 是业务与策略持久化的数据库句柄。
	DB *gorm.DB
	// Enforcer 是已加载策略的授权引擎，本阶段仅校验其可用，尚未接入业务判定。
	Enforcer *casbin.Enforcer
}

// New 按 配置 → 数据库 → 迁移校验 → 授权引擎 的顺序装配依赖。
// 任一步失败即返回错误，由调用方决定是否退出进程（快速失败）。
func New(cfg config.Config) (*App, error) {
	db, err := OpenDB(cfg)
	if err != nil {
		return nil, err
	}

	if err := VerifyMigrations(context.Background(), db); err != nil {
		closeDB(db)
		return nil, err
	}

	enforcer, err := NewEnforcer(db, cfg.CasbinModelPath)
	if err != nil {
		closeDB(db)
		return nil, err
	}

	return &App{DB: db, Enforcer: enforcer}, nil
}

// Check 实现 handlers.ReadinessProbe：校验数据库连通性、迁移状态与策略加载情况。
// 任一依赖不可用都返回错误，调用方应返回 503 而不是放行（fail closed）。
func (a *App) Check(ctx context.Context) error {
	sqlDB, err := a.DB.DB()
	if err != nil {
		return fmt.Errorf("获取底层 sql.DB 失败：%w", err)
	}
	if err := ping(ctx, sqlDB); err != nil {
		return err
	}
	if err := VerifyMigrations(ctx, a.DB); err != nil {
		return err
	}
	if a.Enforcer == nil || a.Enforcer.GetModel() == nil {
		return errors.New("授权引擎策略未加载")
	}
	return nil
}

// Close 释放数据库连接。
func (a *App) Close() error {
	return closeDB(a.DB)
}

// closeDB 关闭底层连接并忽略已关闭等无害错误。
func closeDB(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	if err := sqlDB.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
		return fmt.Errorf("关闭数据库连接失败：%w", err)
	}
	return nil
}
