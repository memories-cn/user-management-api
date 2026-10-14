package ports

import "context"

// Transactor 提供事务边界，保证跨仓储写入的原子性。
//
// 用例在事务内执行多个仓储写入；fn 返回错误即回滚，返回 nil 即提交。
type Transactor interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}
