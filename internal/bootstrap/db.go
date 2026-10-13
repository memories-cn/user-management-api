// Package bootstrap 负责在进程启动阶段完成配置、数据库、迁移校验与 Casbin 的装配。
// 这是唯一允许直接依赖 GORM 与 Casbin 具体实现的装配层。
package bootstrap

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/example/iam-service/internal/config"
)

// 数据库连接池参数。
const (
	maxOpenConns    = 25
	maxIdleConns    = 10
	connMaxLifetime = 30 * time.Minute
	connMaxIdleTime = 5 * time.Minute
	pingTimeout     = 5 * time.Second
)

// OpenDB 建立 GORM 连接并校验连通性，返回已配置连接池的数据库句柄。
func OpenDB(cfg config.Config) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: cfg.DatabaseDSN}), &gorm.Config{
		// TranslateError 让唯一约束等数据库错误可被识别，便于后续映射为 409。
		TranslateError: true,
	})
	if err != nil {
		return nil, fmt.Errorf("打开数据库连接失败：%w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("获取底层 sql.DB 失败：%w", err)
	}
	sqlDB.SetMaxOpenConns(maxOpenConns)
	sqlDB.SetMaxIdleConns(maxIdleConns)
	sqlDB.SetConnMaxLifetime(connMaxLifetime)
	sqlDB.SetConnMaxIdleTime(connMaxIdleTime)

	if err := ping(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return db, nil
}

// ping 在有限时间内验证数据库连通性。
func ping(ctx context.Context, sqlDB *sql.DB) error {
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()

	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("数据库连通性检查失败：%w", err)
	}
	return nil
}
