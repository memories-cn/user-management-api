package bootstrap

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// schemaMigrationsTable、casbinRuleTable 与 policyVersionTable 是启动期必须存在的表。
const (
	schemaMigrationsTable = "schema_migrations"
	casbinRuleTable       = "casbin_rule"
	policyVersionTable    = "policy_version"
)

// VerifyMigrations 校验数据库迁移状态。
//
// 设计约定：服务启动只校验迁移状态，**不执行迁移**（见 docs/TDD.md 13.3），
// 避免多实例并发执行结构变更。迁移由独立的 cmd/migrate 步骤完成。
func VerifyMigrations(ctx context.Context, db *gorm.DB) error {
	exists, err := tableExists(ctx, db, schemaMigrationsTable)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("未发现 %s 表，请先执行数据库迁移", schemaMigrationsTable)
	}

	var state struct {
		Version int64
		Dirty   bool
	}
	err = db.WithContext(ctx).
		Raw("SELECT version, dirty FROM " + schemaMigrationsTable + " ORDER BY version DESC LIMIT 1").
		Scan(&state).Error
	if err != nil {
		return fmt.Errorf("读取迁移版本失败：%w", err)
	}
	if state.Version <= 0 {
		return errors.New("数据库迁移尚未执行")
	}
	if state.Dirty {
		return fmt.Errorf("迁移处于 dirty 状态（version=%d），需人工修复后再启动", state.Version)
	}

	for _, table := range []string{casbinRuleTable, policyVersionTable} {
		exists, err := tableExists(ctx, db, table)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("未发现 %s 表，请先执行数据库迁移", table)
		}
	}
	return nil
}

// tableExists 判断当前 schema 下指定表是否存在。
func tableExists(ctx context.Context, db *gorm.DB, table string) (bool, error) {
	var exists bool
	err := db.WithContext(ctx).
		Raw("SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = ?)", table).
		Scan(&exists).Error
	if err != nil {
		return false, fmt.Errorf("检查表 %s 是否存在失败：%w", table, err)
	}
	return exists, nil
}
