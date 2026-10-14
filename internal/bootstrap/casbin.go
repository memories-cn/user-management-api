package bootstrap

import (
	"fmt"

	"github.com/casbin/casbin/v3"
	gormadapter "github.com/casbin/gorm-adapter/v3"
	"gorm.io/gorm"
)

// NewEnforcer 基于数据库策略构造 Casbin Enforcer。
//
// 关键约束：
//   - 必须关闭 adapter 的自动迁移，否则表结构会脱离版本化迁移管控；
//   - 模型中的 dom 即业务租户 ID（见 docs/TDD.md 7.2）；
//   - 未显式授权一律拒绝，策略为空时 Enforce 返回 false；
//   - 返回 SyncedEnforcer：策略重载（LoadPolicy）与判定（Enforce）会并发发生，
//     普通 Enforcer 的这两个操作不是并发安全的。
func NewEnforcer(db *gorm.DB, modelPath string) (*casbin.SyncedEnforcer, error) {
	// TurnOffAutoMigrate 必须在构造 adapter 之前调用（该函数原地修改 db 且不返回值），
	// 表结构统一由 migrations 目录管理。
	gormadapter.TurnOffAutoMigrate(db)

	adapter, err := gormadapter.NewAdapterByDB(db)
	if err != nil {
		return nil, fmt.Errorf("创建 Casbin GORM adapter 失败：%w", err)
	}

	enforcer, err := casbin.NewSyncedEnforcer(modelPath, adapter)
	if err != nil {
		return nil, fmt.Errorf("创建 Casbin enforcer 失败：%w", err)
	}
	if err := enforcer.LoadPolicy(); err != nil {
		return nil, fmt.Errorf("加载 Casbin 策略失败：%w", err)
	}
	return enforcer, nil
}
