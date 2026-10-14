package ports

import "context"

// Authorizer 是统一授权判定入口（见 docs/TDD.md 7.5）。
//
// 返回值的语义必须严格区分：
//   - allowed = false, err = nil：明确拒绝，业务上不该放行；
//   - err != nil：授权依赖不可用或决策无法完成，调用方必须**失败关闭**
//     （fail closed），不得把错误当成"拒绝后仍按默认放行"。
//
// 调用方在调用本接口之前，必须已经完成用户状态、租户状态与 Membership 校验；
// Casbin 的 Allow 不能替代数据库租户隔离检查（docs/TDD.md 7.1）。
type Authorizer interface {
	// Enforce 判定 subject 在 tenantID 下能否对 resource 执行 action。
	Enforce(ctx context.Context, subject, tenantID, resource, action string) (bool, error)
}

// PolicyReloader 在策略投影提交后刷新本地内存策略。
//
// 用例必须在策略写入事务**提交之后**调用它；提交前调用会读到未提交的投影。
// 刷新失败必须被显式处理（返回错误或让就绪检查失败），不能继续用旧策略放行。
type PolicyReloader interface {
	// RefreshIfStale 在本地策略版本落后于全局版本时重新加载。
	RefreshIfStale(ctx context.Context) error
}
