# Plan: 管理后台 API 与审计（PRD Milestone 5）

**Source PRD**: `.claude/prds/iam-service.prd.md`
**Selected Milestone**: 5 — 管理后台 API 与审计
**Complexity**: Large

## Summary

把高风险操作从"只写结构化日志"升级为**落库留痕**：新增 `audit_logs` 表与审计域，让成员移除、角色分配、角色权限变更、租户停用、平台管理操作在同一业务事务内写入审计记录；提供受 `audit:read` 独立权限保护的审计查询接口；补齐管理接口的分页与过滤；把散落的错误码常量收敛到 `responses` 包；产出 OpenAPI 3.0 文档并用测试防止其与路由表脱节。

验收口径（PRD Milestone 5）：管理员能完成租户成员管理与角色权限配置；高风险操作全部留痕且审计访问受独立权限保护。

## Requirements Restatement

- **高风险操作留痕**：成员移除、角色分配、角色权限变更、租户停用、平台管理操作必须产生审计记录，覆盖率 100%（PRD Success Metrics）。审计记录与业务数据、Casbin 策略投影必须在**同一事务**内写入（TDD §8.3）。
- **审计内容安全**：`metadata` 不得包含 Access Token、Refresh Token、密码或其他敏感认证材料（TDD §3.7）。
- **审计访问受独立权限保护**：审计日志的访问权限应独立定义（TDD §10.4），不能因为能管理成员就自动获得全租户操作留痕的读取能力。
- **管理接口的分页与过滤**：管理后台需要按条件浏览成员、邀请、角色与审计日志（PRD MVP 第 5 项、TDD Phase 5）。
- **统一错误码**：目前常量分散在 `handlers` 与三个 `middleware` 文件，且存在硬编码字符串；需收敛为唯一来源。
- **API 文档**：产出 OpenAPI 3.0（TDD §16 最终验收项），并保证其不随代码演进而腐化。

## Patterns to Mirror

> 以下均来自仓库中**已存在并通过测试**的代码，实施时必须沿用，不要另起风格。

| Category | Source | Pattern |
| --- | --- | --- |
| Naming | `internal/domain/tenant/tenant.go:15,27,71,107` | `type Status string` + 状态常量；哨兵错误 `var (...)` 声明在实体文件顶部；`New{Entity}(...) (*Entity, error)` 构造并校验不变量；变更方法返回 `bool` 表示是否变更 |
| Naming | `internal/domain/role/role.go` | 领域常量（如 `KeyOwner`）与 `NormalizeKey` 归一化函数放在领域层，避免魔法字符串扩散 |
| Errors | `internal/application/tenancy/access.go:20-28,64` | 用例层哨兵错误 + `fmt.Errorf("%w：%s", Err, val)` 携带上下文 |
| Errors | `internal/adapters/postgres/policy_repository.go` | 适配器统一 `fmt.Errorf("中文动作描述失败：%w", err)`；**必须走 `dbFrom(ctx)`** 才能参与事务 |
| Errors | `internal/transport/http/handlers/errors.go:19-42,47-84` | 错误码常量 + `writeError` 用 `switch { case errors.Is(...) }` 映射；`default` 记 ERROR 日志 + 500，不泄漏内部细节 |
| Data access | `internal/adapters/postgres/membership_repository.go:17,28,36,48` | 包私有 `xxxModel` + 显式 `TableName()` + `var _ ports.X = (*X)(nil)` 编译期断言 |
| Data access | `internal/ports/repositories.go:16-18` | 接口注释写明"查询未命中返回 `(nil, nil)`，未命中不是错误" |
| Data access | `migrations/000011_create_policy_version.up.sql` | 中文注释说明设计意图 → `CREATE TABLE`（CHECK 约束、外键）→ 显式命名索引 → `.down.sql` 精确回滚 |
| Transport | `internal/transport/http/handlers/role.go:35,175` | DTO 与 handler 同文件；租户 ID 只从 `middleware.TenantIDFrom` 取；`tenantAndXxxID(c)` 统一解析路径参数 |
| Transport | `internal/transport/http/router.go:59-60,`guard`函数` | 分组挂 `TenantGuard`，每条路由再**显式**挂 `guard(app, resource, action)` |
| Transport | `internal/transport/http/responses/response.go:42,47` | 所有响应经 `responses.OK` / `responses.Fail`，request_id 自动一致 |
| Logging | `internal/application/tenant/create.go:129` | 平台/高风险操作用 `slog.InfoContext(ctx, "...", "key", value...)` 结构化记录 |
| Bootstrap | `internal/bootstrap/app.go:56-109` | `New(cfg)` 顺序装配，任一步失败即 `closeDB` 并返回（快速失败） |
| Bootstrap | `internal/bootstrap/tenancy.go` | 每个依赖一个 `NewXxx`；用例装配集中在 `NewXxxServices(...)`，返回聚合 struct 避免长返回值列表 |
| Tests | `tests/integration/tenancy_stack_test.go:43` | `newTenancyStack` 装配"真实 DB + OIDC 桩 + 路由"，装配顺序与生产一致 |
| Tests | `tests/integration/rbac_routes_test.go` | 用 `router.Routes()` 遍历已注册路由做覆盖断言——本项目已用该手法防止漏挂权限 |
| Tests | `internal/rbactest/fakes.go` | 共享内存替身包（对齐 `internal/oidctest` 先例），避免各测试包重复实现同一组替身 |

## 本计划锁定的决策

| 决策 | 选择 | 理由 | 可逆性 |
| --- | --- | --- | --- |
| **审计写入方式** | **与业务写入、策略投影同一数据库事务**；审计写失败即回滚整个高风险操作 | TDD §8.3 明确三者须同事务；"改了权限但没留痕"不可接受 | 中 |
| 排除：Outbox / 异步审计 | 审计是本地写入，可同事务；异步方案引入额外组件与延迟，收益不匹配 MVP | 高 |
| 审计失败语义 | 返回错误 → 操作整体失败（fail closed），**不允许**"业务成功 + 审计丢失" | PRD 要求审计覆盖率 100% | 中 |
| 审计覆盖范围 | 仅记录**已成功执行**的高风险操作；被拒绝的请求仍记 slog WARN 但不落库 | 合规关注点是"谁改了什么"；避免审计表被拒绝尝试淹没 | 高 |
| 审计上下文传递 | 领域层定义 `audit.Context{RequestID, IPAddress, UserAgent}`，由中间件写入 `context`，用例层从 ctx 取 | 用例层不得依赖 Gin（分层约束）；与 `identity.WithPrincipal` 同构 | 中 |
| 审计元数据 | `metadata` 为 JSONB；领域层提供拒绝敏感键的校验（token/password/secret/authorization 等） | TDD §3.7 硬性要求 | 低 |
| 审计表可变性 | **只追加**，不提供更新与删除接口，无软删除 | 审计记录本身必须可信、不可篡改 | 低 |
| IP 地址可信度 | 取 `c.ClientIP()`；README 标注生产必须配置 Gin `TrustedProxies`，否则该字段可被伪造 | 审计中的 IP 属辅助线索，不作为授权依据 | 高 |
| **分页响应格式** | 所有分页列表统一 `{"items":[...],"pagination":{"limit","offset","total"}}` 信封 | 管理后台需要总数；一致性优先于兼容性 | 中 |
| 破坏性影响 | members（M3）与 roles/invitations（M4）的响应体由裸数组改为信封；需同步更新既有集成测试 | 项目未上线、前端未接入，是统一契约的最后窗口 | 中 |
| 不提供 `total` 的例外 | 无——统一带 total；审计日志的 COUNT 走 `(tenant_id, created_at)` 索引 | — | 高 |
| **错误码唯一来源** | 全部错误码常量下沉到 `responses` 包；`middleware` 与 `handlers` 均引用它 | 两 package 都已 import `responses`，无循环依赖；消除硬编码 `"UNAUTHENTICATED"` 字符串 | 低 |
| 审计查询权限 | 独立 `audit:read`；**维持现有预置矩阵**（owner 与 admin 均具备），不额外收紧 | 租户 admin 本就持有 `permission:assign`，收紧预设形同虚设；真正收窄需另案限制自授 | 高 |
| OpenAPI 防腐化 | 手写 `api/openapi.yaml` + 集成测试遍历 `router.Routes()` 断言文档覆盖每条 `/api/v1` 路由 | 项目无 swag 依赖；测试能保证文档不随代码演进静默过期 | 中 |
| 平台租户生命周期 | 单端点 `PATCH /api/v1/platform/tenants/:tenantID/status`，仅允许 `active`/`suspended` | `tenant.Suspend()` 已注释"供平台管理接口使用（Milestone 5）"；`deleted` 的清理策略未定 | 中 |
| 不提供租户删除 | `deleted` 状态与数据清理、备份策略未定，留在后续里程碑 | 高 |
| 审计查询过滤维度 | `actor_user_id`、`action`、`resource_type`、`since`、`until` + `limit`/`offset` | 覆盖合规对账与日常排查 | 高 |
| Vue 管理后台前端 | **不纳入**本里程碑 | PRD Open Question 未决；PRD 明确"只约束其所需的管理 API" | 高 |

## Files to Change

| File | Action | Why |
| --- | --- | --- |
| `migrations/000012_create_audit_logs.{up,down}.sql` | CREATE | 审计表 + 索引 |
| `internal/domain/audit/entry.go` (+test) | CREATE | 审计实体、`Action`/`Result` 枚举、元数据脱敏 |
| `internal/domain/audit/context.go` (+test) | CREATE | 审计上下文（request_id / ip / user_agent）的 ctx 读写 |
| `internal/ports/audit.go` | CREATE | `AuditRepository` 接口 |
| `internal/ports/repositories.go` | UPDATE | 追加列表查询的过滤结构体说明（如需） |
| `internal/application/audit/service.go` (+test) | CREATE | 审计记录与查询用例 |
| `internal/application/role/service.go` | UPDATE | 角色创建/删除/权限变更落审计 |
| `internal/application/role/assignment.go` | UPDATE | 成员角色分配落审计 |
| `internal/application/membership/service.go` | UPDATE | 成员移除/状态变更落审计；分页信封 |
| `internal/application/invitation/service.go` | UPDATE | 邀请创建落审计 |
| `internal/application/tenant/create.go` | UPDATE | 平台创建租户由 slog 改为落库审计 |
| `internal/application/tenant/status.go` (+test) | CREATE | 平台停用/恢复租户用例 |
| `internal/adapters/postgres/audit_repository.go` (+test) | CREATE | GORM 实现，走 `dbFrom(ctx)` |
| `internal/transport/http/middleware/audit_context.go` (+test) | CREATE | 从请求提取审计上下文写入 ctx |
| `internal/transport/http/handlers/audit.go` | CREATE | `GET /tenants/:tenantID/audit-logs` |
| `internal/transport/http/handlers/platform.go` | UPDATE | 租户状态变更接口 |
| `internal/transport/http/handlers/{member,role,invitation,tenant}.go` | UPDATE | 分页信封与过滤参数 |
| `internal/transport/http/handlers/errors.go` | UPDATE | 错误码改为引用 `responses`；新增审计/平台错误码 |
| `internal/transport/http/middleware/{authn,tenant_guard,platform_admin,require_permission}.go` | UPDATE | 错误码改为引用 `responses` |
| `internal/transport/http/responses/response.go` | UPDATE | 承载全部错误码常量 + `ListEnvelope` 分页信封 |
| `internal/transport/http/router.go` | UPDATE | 审计路由、平台状态路由 |
| `internal/bootstrap/{audit,rbac,tenancy,app}.go` | UPDATE | 装配审计依赖 |
| `internal/config/config.go` (+test) | UPDATE | 新增 `AUDIT_PAGE_MAX_LIMIT`（如需） |
| `internal/rbactest/fakes.go` | UPDATE | 增加内存审计仓储替身 |
| `api/openapi.yaml` | CREATE | OpenAPI 3.0 文档 |
| `tests/integration/audit_test.go` | CREATE | 审计落库、查询、权限、事务一致性 |
| `tests/integration/openapi_test.go` | CREATE | 文档覆盖路由表的回归测试 |
| `tests/integration/{tenancy,rbac}_test.go` | UPDATE | 适配分页信封与新增依赖 |
| `README.md` / `.env.example` | UPDATE | 接口、错误码、审计说明 |

## NOT Building

- **Vue 管理后台前端** —— PRD Open Question 未决，本里程碑只交付其所需 API
- **租户删除（`deleted` 状态）与数据清理** —— 清理与备份策略未定
- **审计记录的更新、删除、导出与归档** —— 审计只追加；导出/归档留后续
- **被拒绝请求（403/401）的审计落库** —— 仍只记 slog；是否纳入待 Open Question
- **平台管理员的只读运营视图** —— PRD Open Question 未决
- **限流、监控指标、压测、备份恢复** —— Milestone 6
- **Redis 缓存与策略 watcher** —— Milestone 6
- **`govulncheck` / 依赖漏洞扫描** —— Milestone 6（ci.yml 中已有 TODO）
- **审计日志的跨租户平台级查询接口** —— 平台侧统一检索留后续

---

## Tasks

### Task 1: 审计表迁移
- **Action**: 新增 `migrations/000012_create_audit_logs.{up,down}.sql`
- **Implement**（关键约束）:
  ```sql
  CREATE TABLE audit_logs (
      id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
      tenant_id      UUID,                       -- 平台级操作可为空，但不得用作隐式提权通道
      actor_user_id  UUID NOT NULL REFERENCES users(id),
      action         VARCHAR(64)  NOT NULL,
      resource_type  VARCHAR(64)  NOT NULL,
      resource_id    VARCHAR(128) NOT NULL DEFAULT '',
      result         VARCHAR(32)  NOT NULL DEFAULT 'success'
                     CHECK (result IN ('success', 'denied', 'error')),
      request_id     VARCHAR(128) NOT NULL DEFAULT '',
      ip_address     VARCHAR(64)  NOT NULL DEFAULT '',
      user_agent     TEXT         NOT NULL DEFAULT '',
      metadata       JSONB        NOT NULL DEFAULT '{}'::jsonb,
      created_at     TIMESTAMPTZ  NOT NULL DEFAULT now()
  );

  -- 审计查询的主路径：按租户与时间倒序浏览。
  CREATE INDEX idx_audit_logs_tenant_created ON audit_logs (tenant_id, created_at DESC);
  CREATE INDEX idx_audit_logs_tenant_actor   ON audit_logs (tenant_id, actor_user_id);
  CREATE INDEX idx_audit_logs_tenant_action  ON audit_logs (tenant_id, action);
  ```
- **Mirror**: Data access（`migrations/000011_create_policy_version.up.sql`）
- **Gotcha**:
  - **不建外键到 `tenants`**：审计应保留租户被清理后的历史；且 `tenant_id` 为空用于平台级操作
  - **不加 `updated_at`、不加软删除**：审计只追加，任何更新/删除都会破坏其可信度
  - JSONB 默认值必须是 `'{}'::jsonb` 而非 `'{}'`，否则类型推断为 text
- **Validate**: `make migrate-up` 后 `\d audit_logs` 符合预期；`make migrate-down` 可精确回滚

### Task 2: 审计领域实体与上下文
- **Action**: `internal/domain/audit/entry.go`、`internal/domain/audit/context.go`（**先写失败测试**）
- **Implement**:
  - `Action` 常量：`member.invite`、`member.remove`、`member.status_change`、`role.assign`、`role.create`、`role.update`、`role.delete`、`role.permission.update`、`tenant.create`、`tenant.suspend`、`tenant.activate`
  - `Result` 常量：`success` / `denied` / `error`
  - `NewEntry(tenantID *uuid.UUID, actorUserID uuid.UUID, action Action, resourceType, resourceID string, result Result) (*Entry, error)`：校验 actor 非 nil、action 非空且在枚举内
  - `WithMetadata(map[string]any) error`：**拒绝敏感键**——键名（小写后）包含 `token`、`password`、`secret`、`authorization`、`refresh`、`credential` 时返回 `ErrSensitiveMetadata`；值只能是 JSON 可序列化的标量，避免塞入结构体泄漏内部字段
  - `Context{RequestID, IPAddress, UserAgent}` + `WithContext(ctx, Context)` / `FromContext(ctx)`，未设置时返回零值（不得因此让业务失败）
- **Mirror**: Naming（`internal/domain/tenant/tenant.go:15,27,71`）
- **Gotcha**: 领域层不得依赖 Gin；`user_agent` 需截断（如 512 字符）防止超长请求头撑爆字段
- **Validate**: `go test ./internal/domain/audit/...` 覆盖构造校验、敏感键拒绝、上下文读写

### Task 3: 审计仓储与用例
- **Action**: `internal/ports/audit.go`、`internal/adapters/postgres/audit_repository.go`、`internal/application/audit/service.go`
- **Implement**:
  ```go
  type AuditRepository interface {
      // Create 追加一条审计记录。**必须走 dbFrom(ctx)**，以便与业务写入同事务。
      Create(ctx context.Context, e *audit.Entry) error
      // ListByTenant 按过滤条件分页返回，必须带 tenant_id 条件。
      ListByTenant(ctx context.Context, tenantID string, f AuditFilter) ([]*audit.Entry, error)
      // CountByTenant 统计满足过滤条件的记录数，用于分页信封的 total。
      CountByTenant(ctx context.Context, tenantID string, f AuditFilter) (int64, error)
  }
  ```
  - `AuditFilter{ActorUserID, Action, ResourceType string, Since, Until *time.Time, Limit, Offset int}`
  - `audit.Service` 提供 `Record(ctx, *Entry) error` 与 `List(ctx, tenantID, filter) (*ListResult, error)`
  - `Service.NewService(repo)`；用例依赖具体类型（与 M4 中用例依赖 `*approle.AssignmentService` 一致）
- **Mirror**: Data access（`internal/adapters/postgres/policy_repository.go`）
- **Gotcha**:
  - `Create` 若忘记 `dbFrom(ctx)`，审计会在独立连接写入——事务回滚后审计残留，形成"没有业务效果却有留痕"的脏数据
  - 过滤条件的 `action` 必须校验在枚举内，非法值直接报错而不是静默忽略（ silently 忽略会让管理员误以为"没有这类操作"）
  - `since`/`until` 非法（如 since > until）返回 400
- **Validate**: `go test ./internal/adapters/postgres/... ./internal/application/audit/...`；集成测试验证事务回滚后无审计残留

### Task 4: 高风险操作接入审计（同一事务）
- **Action**: 修改 `membership/service.go`、`role/service.go`、`role/assignment.go`、`invitation/service.go`、`tenant/create.go`
- **Implement**（每个用例在其**已有事务内**追加一步审计写入）:
  | 用例 | action | resource_type |
  | --- | --- | --- |
  | `membership.Remove` | `member.remove` | `member` |
  | `membership.UpdateStatus` | `member.status_change` | `member` |
  | `assignment.SetMemberRoles` | `role.assign` | `member` |
  | `role.CreateRole` | `role.create` | `role` |
  | `role.DeleteRole` | `role.delete` | `role` |
  | `role.SetRolePermissions` | `role.permission.update` | `role` |
  | `invitation.Invite` | `member.invite` | `invitation` |
  | `tenant.CreateService.Create` | `tenant.create` | `tenant` |
- **Gotcha**:
  - 审计写入必须放在 `WithinTx` **内部**，与业务写入、策略投影、版本号递增并列；放在事务外会出现"操作失败却留痕"
  - `tenant.CreateService.Create` 目前的 `slog.InfoContext`（`create.go:129`）**保留**，落库审计与日志并存——审计失败时日志仍是可排查线索
  - 用例构造函数需新增 `audits *audit.Service` 参数，会连带更新 `bootstrap` 与全部单测装配
- **Mirror**: Errors（`internal/application/tenancy/access.go:64`）
- **Validate**: 集成测试对每个高风险操作断言审计记录存在且内容正确

### Task 5: 平台租户停用/恢复（带审计）
- **Action**: `internal/application/tenant/status.go` + `handlers/platform.go` + `router.go`
- **Implement**:
  - `StatusService.SetStatus(ctx, actor uuid.UUID, tenantID uuid.UUID, status tenant.Status) (*tenant.Tenant, error)`：
    1. 仅接受 `active` / `suspended`，其他 → `ErrInvalidStatus`
    2. 租户不存在 → `ErrTenantNotFound`（平台路径返回 404，平台管理员是全知的，无需防枚举）
    3. 状态未变化 → 幂等返回，不写审计
    4. **同一事务内**：更新租户 + 写审计（`tenant.suspend` / `tenant.activate`）
  - 路由：`PATCH /api/v1/platform/tenants/:tenantID/status`，挂 `AuthN + RequirePlatformAdmin`
  - 停用后成员访问已由 `TenantGuard` 拒绝（`403 TENANT_SUSPENDED`），本任务不改动该逻辑
- **Mirror**: Bootstrap（`internal/bootstrap/tenancy.go`）、Transport（`internal/transport/http/handlers/platform.go:57`）
- **Gotcha**:
  - 平台路径**不得**复用 `TenantGuard`（它会因平台管理员不是成员而返回 404），必须走独立的平台路由组
  - 恢复（activate）与停用都要审计——恢复同样是高风险操作
  - 不提供 `deleted`：宁可留空也不实现一个无法撤销的删除
- **Validate**: 集成测试覆盖停用→成员被拒（403）、恢复→可访问、两次操作均有审计、非平台管理员 403

### Task 6: 审计查询接口与独立权限
- **Action**: `internal/transport/http/handlers/audit.go` + 路由挂载
- **Implement**:
  ```
  GET /api/v1/tenants/:tenantID/audit-logs    AuthN + TenantGuard + audit:read
  ```
  查询参数：`limit`、`offset`、`actor_user_id`、`action`、`resource_type`、`since`、`until`（RFC3339）
  响应：`{"items":[...],"pagination":{"limit","offset","total"}}`
- **Gotcha**:
  - 必须挂在 `TenantGuard` 之后且显式要求 `audit:read`——这是"审计访问受独立权限保护"的落点
  - 查询只能返回**本租户**的记录：SQL 强制带 `tenant_id`，不接受任何跨租户查询参数
  - `action` 参数必须校验枚举，非法值返回 400 而非空列表
- **Mirror**: Transport（`internal/transport/http/handlers/role.go:35`、`handlers/tenant.go:117` 的 `parsePaging`）
- **Validate**: 集成测试验证权限拒绝（无 `audit:read` 的成员 403）、过滤生效、跨租户不可见

### Task 7: 管理接口分页与过滤统一
- **Action**: 修改 `member.go`、`role.go`、`invitation.go`、`tenant.go`，新增 `responses.ListEnvelope`
- **Implement**:
  ```go
  // responses
  type Pagination struct {
      Limit  int   `json:"limit"`
      Offset int   `json:"offset"`
      Total  int64 `json:"total"`
  }
  type ListEnvelope struct {
      Items      any        `json:"items"`
      Pagination Pagination `json:"pagination"`
  }
  ```
  - `members`：`limit`/`offset` + `status` 过滤（active/suspended/pending）
  - `roles`：`limit`/`offset`（角色数量有限，主要保证契约一致）
  - `invitations`：`limit`/`offset` + `status` 过滤
  - `audit-logs`：见 Task 6
  - `parsePaging` 提升到共用位置并增加上限约束（复用 `appmembership.DefaultLimit/MaxLimit` 语义）
- **Gotcha**:
  - **这是破坏性变更**：members/roles/invitations 响应由裸数组变为信封，需同步更新 `tests/integration/tenancy_test.go`、`rbac_test.go` 中的解析结构体
  - `total` 需要额外 COUNT 查询；审计表上靠 `(tenant_id, created_at)` 索引，成员表规模小
  - 过滤条件必须进 SQL，不允许"先全量查出再内存过滤"
- **Mirror**: Transport（`internal/transport/http/handlers/tenant.go:117`）
- **Validate**: 集成测试断言信封结构与过滤结果；既有用例同步更新后全绿

### Task 8: 错误码收敛为唯一来源
- **Action**: 错误码常量下沉到 `responses` 包；`middleware` 与 `handlers` 全部改为引用
- **Implement**:
  - `responses` 新增：`CodeUnauthenticated`、`CodeForbidden`、`CodeNotFound`、`CodeDependencyUnavailable`、`CodeTenantSuspended`、`CodeMembershipInactive`、`CodeInvalidArgument`、`CodeInternal`
  - 删除 `middleware/authn.go`、`tenant_guard.go`、`platform_admin.go` 中的重复常量定义，改为引用
  - 消除硬编码字符串：`handlers/invitation.go:35`、`handlers/tenant.go:31`、`handlers/platform.go` 等处的 `"UNAUTHENTICATED"`
  - `handlers/errors.go` 保留业务错误码（冲突类）与新错误码：`AUDIT_ACTION_INVALID`、`AUDIT_FILTER_INVALID`、`TENANT_NOT_FOUND`
- **Mirror**: Errors（`internal/transport/http/handlers/errors.go:19-42`）
- **Gotcha**: 常量值字符串必须逐字保持一致，否则是有感破坏性变更（前端已按字符串判断错误码）
- **Validate**: `go test ./internal/transport/http/...`；集成测试断言错误码字符串未变

### Task 9: 审计上下文中间件
- **Action**: `internal/transport/http/middleware/audit_context.go`
- **Implement**: 从请求中提取 `RequestID`（`c.GetString(responses.RequestIDContextKey)`）、`c.ClientIP()`、`c.GetHeader("User-Agent")`，写入 `audit.WithContext`
- **Gotcha**:
  - Gin 默认信任所有代理，`ClientIP()` 可被 `X-Forwarded-For` 伪造；生产必须 `SetTrustedProxies`，README 需写明
  - `User-Agent` 需截断后再落库
  - 中间件必须在 `AuthN` 之后（需要 request_id 与身份），在业务 handler 之前
- **Mirror**: Transport（`internal/transport/http/middleware/request_id.go`）
- **Validate**: 单测覆盖提取与截断；集成测试断言审计记录含 request_id

### Task 10: OpenAPI 3.0 文档与防腐化测试
- **Action**: 新建 `api/openapi.yaml`；`tests/integration/openapi_test.go`
- **Implement**:
  - 覆盖全部 `/api/v1` 接口（含 `/health`），标注认证方式、权限要求、错误码、统一响应信封
  - 测试：解析 YAML，遍历 `router.Routes()`，把 Gin 路径参数（`:tenantID`）还原为 OpenAPI 形式（`{tenantID}`），断言每个 `METHOD /api/v1/...` 都在文档中存在
  - YAML 解析使用依赖树中已有的 `go.yaml.in/yaml/v3`（需转为直接依赖）
- **Mirror**: Tests（`tests/integration/rbac_routes_test.go`）
- **Gotcha**:
  - 文档路径参数命名必须与 Gin 一致，否则断言失败——这正是防腐化测试的价值
  - 不要把 `internal` 实现细节写进文档；`metadata` 只描述为"附加结构化信息（不含凭据）"
- **Validate**: `go test ./tests/integration/... -run TestOpenAPI` 全绿

### Task 11: 集成与安全回归测试
- **Action**: `tests/integration/audit_test.go` + 更新既有测试
- **Implement**（复用 `newTenancyStack` 并扩展审计依赖装配）:
  - **落库**：成员移除、角色分配、角色权限变更、租户停用、平台创建租户各自产生一条审计记录，`result=success`
  - **事务一致性**：注入审计写入失败 → 高风险操作整体失败，且业务表与 `casbin_rule` 均无残留
  - **权限**：无 `audit:read` 的成员查审计 → 403；非成员 → 404
  - **隔离**：A 租户审计不出现在 B 租户查询中；直接构造跨租户查询参数无效
  - **过滤**：按 actor / action / 时间范围过滤结果正确；非法 action → 400
  - **脱敏**：尝试写入含 `token` 键的元数据被拒绝（领域层单测已覆盖，此处验证端到端不落库）
  - **平台路径**：停用后成员访问 403；恢复后 200；两个动作均有审计；非平台管理员 → 403
- **Mirror**: Tests（`tests/integration/rbac_test.go`、`tenancy_stack_test.go:43`）
- **Gotcha**: `newTenancyStack` 需新增审计仓储与用例装配；既有测试的解析结构体需适配分页信封
- **Validate**: `go test ./tests/integration/... -count=1` 全绿

### Task 12: 配置、装配与文档
- **Action**: `bootstrap/audit.go`；`app.go` 增字段与接线；`rbactest` 增内存审计替身；更新 `.env.example` 与 README
- **Implement**:
  - `App` 新增 `Audits`、`AuditService`；`New` 在仓储之后、用例之前构造审计用例
  - `rbactest.Fixture` 增 `Audits`（内存审计仓储），使既有单测无需改动即可获得审计能力
  - README 增补：审计覆盖范围表、审计只追加与不可删除、`ClientIP` 可信代理前置条件、分页信封格式
- **Mirror**: Bootstrap（`internal/bootstrap/tenancy.go`、`app.go:56-109`）
- **Validate**: `go test ./internal/config/...`；`go build ./...`

---

## Validation

```bash
# 静态与构建
gofmt -l .                 # EXPECT: 无输出
go vet ./...               # EXPECT: 无输出
go build ./...             # EXPECT: exit 0

# 单元测试（不依赖 Docker）
go test -short -race ./... # EXPECT: 全部 PASS

# 集成与安全回归（需 Docker）
go test ./tests/integration/... -count=1        # EXPECT: PASS
go test ./tests/integration/... -count=1 -race  # EXPECT: 无 data race

# 数据库约束
make migrate-up
docker compose exec postgres psql -U postgres -d iam -c '\d audit_logs'
# EXPECT: result CHECK 约束、三个 idx_audit_logs_* 索引、无 updated_at

# 冒烟：审计留痕
curl -H "Authorization: Bearer $OWNER_TOKEN" -X DELETE \
  localhost:8080/api/v1/tenants/<uuid>/members/<user-uuid>
# EXPECT: 200 且 audit_logs 新增一条 action='member.remove' 的记录
curl -H "Authorization: Bearer $OWNER_TOKEN" \
  "localhost:8080/api/v1/tenants/<uuid>/audit-logs?action=member.remove&limit=10"
# EXPECT: {"data":{"items":[...],"pagination":{"limit":10,"offset":0,"total":1}}}

# 冒烟：审计受独立权限保护
curl -H "Authorization: Bearer $VIEWER_TOKEN" \
  localhost:8080/api/v1/tenants/<uuid>/audit-logs
# EXPECT: 403 FORBIDDEN（viewer 无 audit:read）

# 冒烟：平台停用
curl -H "Authorization: Bearer $ADMIN_TOKEN" -X PATCH \
  localhost:8080/api/v1/platform/tenants/<uuid>/status -d '{"status":"suspended"}'
# EXPECT: 200；随后成员访问该租户任意接口返回 403 TENANT_SUSPENDED
```

## Risks

| Risk | Likelihood | Mitigation |
| --- | --- | --- |
| **审计写入漏走 `dbFrom(ctx)`，造成"操作失败却留痕"的脏数据** | 中 | Task 3 明确要求；集成测试用审计写入失败注入验证整体回滚 |
| 审计写失败导致高风险操作整体失败，可用性受影响 | 中 | 接受该取舍：PRD 要求审计覆盖率 100%；审计表与主库同实例，故障面与业务一致；后续可评估 Outbox |
| 分页信封变更破坏既有集成测试 | **高** | Task 7 已列出需同步更新的测试；项目未上线、前端未接入，是统一契约的最后窗口 |
| 错误码下沉时误改字符串，造成前端有感变更 | 低 | Task 8 逐字比对并在集成测试中断言错误码字符串 |
| `ClientIP()` 被伪造，审计 IP 不可信 | 中 | README 写明生产必须配置 `TrustedProxies`；IP 仅作辅助线索，不作为授权依据 |
| 审计表随操作量线性增长 | 中 | 首期不分区；索引已覆盖查询路径；归档策略留后续里程碑 |
| OpenAPI 文档与实际路由脱节 | 中 | Task 10 的路由覆盖测试会在 CI 中直接失败 |
| 用例构造函数新增审计参数，改动面较大 | 中 | 集中在 Task 4、`bootstrap` 与 `rbactest`；`rbactest.Fixture` 统一供给内存审计替身 |
| 平台停用后既有会话/缓存仍可访问 | 中 | `TenantGuard` 每次请求直查数据库，停用即时生效；不依赖策略刷新窗口 |
| 元数据意外携带凭据 | 中 | 领域层拒绝敏感键；端到端测试验证含 `token` 键的写入被拒 |

## Acceptance

- [ ] `gofmt -l .` 无输出、`go vet ./...` 无错误、`go build ./...` 通过
- [ ] `go test -short -race ./...` 全绿；`go test ./tests/integration/... -count=1` 全绿
- [ ] `audit_logs` 通过版本化迁移创建，只追加、无更新与删除接口，`migrate-down` 可精确回滚
- [ ] 成员移除、成员状态变更、角色分配、角色创建/删除/权限变更、成员邀请、租户创建、租户停用/恢复**全部产生审计记录**
- [ ] 审计记录与业务数据、策略投影在**同一事务**：注入审计失败后业务与策略均无残留
- [ ] `metadata` 拒绝包含凭据类键；`user_agent` 截断
- [ ] `GET /tenants/:tenantID/audit-logs` 受 `audit:read` 独立权限保护，无权限返回 403，非成员返回 404
- [ ] 审计查询强制带 `tenant_id`，不接受跨租户查询；支持 actor / action / resource_type / 时间范围过滤
- [ ] 平台管理员可停用与恢复租户，两个动作均留痕；停用后成员访问返回 403；非平台管理员返回 403
- [ ] 分页列表统一返回 `{items, pagination{limit, offset, total}}`；过滤条件下推到 SQL
- [ ] 所有错误码常量收敛到 `responses`，消除硬编码字符串，对外字符串值保持不变
- [ ] `api/openapi.yaml` 覆盖全部 `/api/v1` 路由，并有测试防止文档与路由脱节
- [ ] 未越界实现 NOT Building 中的任何一项
- [ ] Patterns mirrored, not reinvented：沿用既有命名、错误、数据访问与测试约定

---

**WAITING FOR CONFIRMATION**：是否按此计划实施？可回复 `yes` / `modify: <改动>` / `different approach: <方案>`。
