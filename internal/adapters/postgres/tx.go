package postgres

import (
	"context"

	"gorm.io/gorm"

	"github.com/example/iam-service/internal/ports"
)

// txKey 是事务句柄在上下文中的键，使用私有类型避免冲突。
type txKey struct{}

// 编译期断言：确保实现 ports.Transactor。
var _ ports.Transactor = (*TxManager)(nil)

// TxManager 基于 GORM 提供事务边界。
type TxManager struct {
	db *gorm.DB
}

// NewTxManager 构造事务管理器。
func NewTxManager(db *gorm.DB) *TxManager {
	return &TxManager{db: db}
}

// WithinTx 在事务中执行 fn：返回错误即回滚，返回 nil 即提交。
//
// 若上下文已存在事务，则**复用**它而不是再开一个：
// GORM 的 db.Transaction 只在句柄本身已是事务时才使用保存点，
// 而这里持有的是基础连接，再开一个就是独立的物理事务——
// 那样内层会先提交，外层回滚就带不走它，原子性形同虚设。
// 用例相互组合（如"创建租户"调用"邀请"）是常态，因此必须支持事务加入。
func (m *TxManager) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(*gorm.DB); ok {
		return fn(ctx)
	}
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(withTx(ctx, tx))
	})
}

// withTx 把事务句柄写入上下文。
func withTx(ctx context.Context, tx *gorm.DB) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// dbFrom 从上下文取事务句柄，取不到时回退到默认连接。
//
// 所有仓储方法都必须通过该函数取句柄，否则事务不会生效。
func dbFrom(ctx context.Context, db *gorm.DB) *gorm.DB {
	if tx, ok := ctx.Value(txKey{}).(*gorm.DB); ok && tx != nil {
		return tx
	}
	return db
}
