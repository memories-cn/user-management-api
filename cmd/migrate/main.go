// Command migrate 以独立、可审计的步骤执行数据库迁移。
//
// 设计约定：API 服务启动**不执行**迁移，只校验迁移状态（见 docs/TDD.md 13.3），
// 避免多个实例同时执行结构变更。迁移文件通过 go:embed 编入本二进制。
//
// 用法：
//
//	migrate -cmd up            应用全部待执行迁移
//	migrate -cmd up -steps 1   只向前应用 1 个版本
//	migrate -cmd down          回滚全部迁移
//	migrate -cmd version       打印当前版本与 dirty 状态
//	migrate -cmd force -steps 1 强制把版本标记为 1（仅用于修复 dirty 状态）
package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/golang-migrate/migrate/v4"
	// 注册 PostgreSQL 数据库驱动，缺失会报 "database driver unknown"。
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/example/iam-service/internal/config"
	"github.com/example/iam-service/migrations"
)

func main() {
	cmd := flag.String("cmd", "up", "迁移命令：up | down | version | force")
	steps := flag.Int("steps", 0, "步数，0 表示全部；force 命令下表示要写入的版本号")
	flag.Parse()

	if err := run(*cmd, *steps); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}
}

// run 执行指定迁移命令，DATABASE_DSN 来自环境变量。
func run(cmd string, steps int) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("加载内嵌迁移文件失败：%w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", src, cfg.DatabaseDSN)
	if err != nil {
		return fmt.Errorf("初始化迁移实例失败：%w", err)
	}
	defer func() {
		if srcErr, dbErr := m.Close(); srcErr != nil || dbErr != nil {
			slog.Warn("failed to close migration instance", "source_error", srcErr, "database_error", dbErr)
		}
	}()

	switch cmd {
	case "up":
		return applyUp(m, steps)
	case "down":
		return applyDown(m, steps)
	case "version":
		return printVersion(m)
	case "force":
		if steps <= 0 {
			return fmt.Errorf("force 命令必须通过 -steps 指定要写入的版本号")
		}
		return m.Force(steps)
	default:
		return fmt.Errorf("未知迁移命令：%q，仅支持 up | down | version | force", cmd)
	}
}

// applyUp 向前应用迁移，已是最新版本时视为成功。
func applyUp(m *migrate.Migrate, steps int) error {
	var err error
	if steps <= 0 {
		err = m.Up()
	} else {
		err = m.Steps(steps)
	}
	if errors.Is(err, migrate.ErrNoChange) {
		slog.Info("no pending migration")
		return nil
	}
	if err != nil {
		return fmt.Errorf("应用迁移失败：%w", err)
	}
	slog.Info("migration applied")
	return nil
}

// applyDown 回滚迁移，已无版本可回滚时视为成功。
func applyDown(m *migrate.Migrate, steps int) error {
	var err error
	if steps <= 0 {
		err = m.Down()
	} else {
		err = m.Steps(-steps)
	}
	if errors.Is(err, migrate.ErrNoChange) {
		slog.Info("no migration to roll back")
		return nil
	}
	if err != nil {
		return fmt.Errorf("回滚迁移失败：%w", err)
	}
	slog.Info("migration rolled back")
	return nil
}

// printVersion 输出当前迁移版本与 dirty 状态。
func printVersion(m *migrate.Migrate) error {
	version, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		fmt.Println("version=none dirty=false")
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取迁移版本失败：%w", err)
	}
	fmt.Printf("version=%d dirty=%t\n", version, dirty)
	return nil
}
