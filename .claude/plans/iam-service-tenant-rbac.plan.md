# Plan: 租户内 RBAC（PRD Milestone 4）

**Source PRD**: `.claude/prds/iam-service.prd.md`
**Selected Milestone**: 4 — 租户内 RBAC
**Complexity**: Large

## Summary

实现租户内 RBAC：新增权限目录、租户角色、角色权限、成员角色四张表；建立"业务表为唯一写来源、`casbin_rule` 为同事务投影"的策略一致性方案；实现 `/authorize` 统一授权判定与 `RequirePermission` 中间件；租户创建时预置系统角色与权限矩阵，首个接受邀请的成员绑定 `owner`；补齐"不能移除最后一个有效 Owner"等管理员保护。

验收口径（PRD Milestone 4）：租户内角色与权限可配置，成员可分配角色，业务服务可通过统一接口判定权限；策略变更能正确撤权。

## Requirements Restatement

- **权限目录**：`resource:action` 形式的系统权限集合由**系统维护**（迁移种子），租户管理员只能在既有集合内组合自定义角色，不能新建可执行权限（TDD §3.5、§10.2）。
- **租户内角色**：`owner/admin/member/viewer` 四个系统角色在租户创建时预置；租户可创建自定义角色；系统角色不可删除、不可改 `key`。
- **成员角色分配**：`PUT /tenants/:tenantID/members/:userID/roles`；**必须保证 Membership 与 Role 同租户**（TDD §8.2 要求复合外键或事务内验证，仅比对 UUID 存在是不够的）。
- **统一授权判定**：`POST /authorize`，调用者身份只能来自已验证令牌，请求体不得声明身份（TDD §9.5）；`allowed=false, err=nil` 为明确拒绝，`err!=nil` 为决策无法完成 → **fail closed**。
- **策略一致性**：业务关系表、Casbin 持久化策略、版本号必须在**同一数据库事务**内更新（TDD §8.3）；角色或成员变更后必须正确撤权。
- **管理员保护**：不能移除最后一个有效 Owner；删除角色须检查是否仍有成员使用；不能通过批量接口绕过单项权限检查（TDD §10.2）。

## Patterns to Mirror

> 以下均来自仓库中**已存在并通过测试**的代码，实施时必须沿用，不要另起风格。

| Category | Source | Pattern |
| --- | --- | --- |
| Naming | `internal/domain/tenant/tenant.go:15,27,71,107` | `type Status string` + 状态常量；错误变量 `var (...)` 声明在实体文件顶部；`New{Entity}(...) (*Entity, error)` 构造并校验不变量；变更方法返回 `bool` 表示是否发生变更 |
| Naming | `internal/domain/invitation/invitation.go:84,129` | 敏感值只存摘要，比较用 `subtle.ConstantTimeCompare` |
| Errors | `internal/application/tenancy/access.go:20-28,64` | 用例层哨兵错误 + `fmt.Errorf("%w：%s", Err, val)` 携带上下文 |
| Errors | `internal/adapters/postgres/membership_repository.go:49` | 适配器统一 `fmt.Errorf("中文动作描述失败：%w", err)` |
| Errors | `internal/transport/http/handlers/errors.go:19-42,47-84` | 错误码常量集中定义；`writeError` 用 `switch { case errors.Is(...) }` 映射，`default` 记 ERROR 日志 + 500，不泄漏内部细节 |
| Data access | `internal/adapters/postgres/membership_repository.go:17,28,36,48` | 包私有 `xxxModel` + 显式 `TableName()` + `var _ ports.X = (*X)(nil)` 编译期断言；每个查询都走 `dbFrom(ctx, r.db)` |
| Data access | `internal/adapters/postgres/tx.go:42-47` | **所有仓储必须经 `dbFrom(ctx)` 取句柄，否则事务不生效** |
| Data access | `internal/ports/repositories.go:14` | 查询未命中返回 `(nil, nil)`，未命中不是错误 |
| Data access | `migrations/000004_create_memberships.up.sql` | 中文注释说明设计意图 → `CREATE TABLE`（`CHECK` 枚举约束、外键）→ 显式命名唯一索引 → 普通索引；`.down.sql` 精确回滚 |
| Transport | `internal/transport/http/router.go:29-30,59-60` | 分组统一挂载中间件，避免个别路由漏挂造成越权 |
| Transport | `internal/transport/http/middleware/tenant_guard.go:48,59` | 租户标识**只从 `c.Param` 取**，不接受任何请求头 |
| Transport | `internal/transport/http/handlers/member.go:16,24,74` | DTO 与 handler 同文件；请求体内联匿名 struct + `binding:"required"` |
| Bootstrap | `internal/bootstrap/tenancy.go:19,51` | 每个依赖一个 `NewXxx` 构造函数；用例装配集中在 `NewXxxServices(...)` |
| Bootstrap | `internal/bootstrap/app.go:56-109` | `New(cfg)` 顺序装配，任一步失败即 `closeDB` 并返回（快速失败） |
| Tests | `tests/integration/bootstrap_test.go:132,207,271` | `newDatabase` 每用例独立库；`assertEnforce` 断言 Casbin 决策；已有"默认拒绝""跨租户域隔离"基线用例 |
| Tests | `tests/integration/tenancy_stack_test.go:43,108` | `newTenancyStack` 装配"真实 DB + OIDC 桩 + 路由"；`token(subject,email,verified,roles)` 构造令牌 |

## 本计划锁定的决策

| 决策 | 选择 | 理由 | 可逆性 |
| --- | --- | --- | --- |
| **策略一致性方案** | **业务表为唯一写来源 + `casbin_rule` 由我方 `PolicyRepository` 在同一事务内投影写入**，提交后刷新本实例内存 | TDD §8.3 明确警告"不能因为都使用 GORM 就默认具备原子性" | 中 |
| 排除：`casbin.TransactionalEnforcer` | 其 `BeginTransaction` 走 adapter **自己的连接**（`gorm-adapter/adapter.go:821`），与业务事务不是同一个 tx，无法保证跨表原子性 | 低 |
| 排除：自定义只读 Adapter 从业务表加载 | 全量 join 重载成本随租户规模线性增长；`casbin_rule` 表空置且与 `bootstrap/schema.go:47` 的启动校验冲突 | 高 |
| 策略列映射 | `p`：`v0=role_id, v1=tenant_id, v2=resource, v3=action`；`g`：`v0=user_id, v1=role_id, v2=tenant_id` | `casbin/model.conf:14-17` matcher 要求 `p.sub` 为角色标识；TDD §7.3 要求实际数据用 UUID | 中 |
| 内存刷新 | `NewEnforcer` 改为返回 `*casbin.SyncedEnforcer`；策略写事务提交后本实例立即 `LoadPolicy()` | 原 `*casbin.Enforcer` 的 `Enforce`/`LoadPolicy` 并发不安全 | 低 |
| **多实例失效** | `policy_version` 单行版本表 + 后台按 `POLICY_REFRESH_INTERVAL`（默认 5s）比对版本并重载；Redis pub/sub watcher 留到 Phase 6 | 项目尚无 Redis（`config.go:70` 仅解析不使用） | 高 |
| **撤权时效边界** | 成员移除 / 租户停用 / 用户禁用的撤权**不依赖刷新窗口**——`TenantGuard` 每次请求直查 DB；只有"角色权限矩阵变更"存在 ≤ `POLICY_REFRESH_INTERVAL` 的窗口 | 回答 PRD Open Question 的一部分；高风险操作不依赖长期缓存（TDD §8.4） | 高 |
| 复合外键保证同租户 | `role_permissions`/`membership_roles` 冗余 `tenant_id`，用 `(role_id, tenant_id) → roles(id, tenant_id)`、`(membership_id, tenant_id) → memberships(id, tenant_id)` 复合外键；为 `memberships` 补 `UNIQUE(id, tenant_id)` | TDD §8.2/§10.2 的硬性要求，数据库级兜底而非应用层自觉 | 中 |
| 权限标识 | 对外用 `code`（`resource:action`）而非 UUID；`permissions.code` 唯一 | 便于管理后台与业务服务引用，避免暴露内部 ID | 高 |
| 角色标识 | 租户内用 `key`（`owner`/`admin`/...），`UNIQUE(tenant_id, key)` | TDD §3.4 | 低 |
| 系统角色预置时机 | **平台管理员创建租户时**在同一事务内预置 4 个 `is_system=true` 角色 + 默认权限矩阵 + `p` 规则投影 | 首个成员接受邀请时只需分配角色 | 中 |
| 接受邀请的角色绑定 | 事务内判断：接受后该租户 active 成员数 == 1 → `owner`；否则 → `DEFAULT_MEMBER_ROLE_KEY`（默认 `member`） | 承接 M3 计划"M4 把首个接受邀请的成员绑定为 owner"（`.claude/plans/iam-service-tenants-and-members.plan.md:57`） | 高 |
| 系统角色可变性 | `key` 不可改、不可删除；`name`/`description` 可改；**权限可调**（仅限系统目录内） | 避免租户自毁权限体系 | 高 |
| 平台管理员 | 继续**只走 ZITADEL 角色 + 独立 `/api/v1/platform` 路径**，不进入 Casbin、不参与租户 RBAC | TDD §7.6；M3 已锁定 | 低 |
| `/authorize` 拒绝语义 | 租户不存在/非成员/成员非 active/无权限 → `200 {allowed:false}`（防枚举）；DB 或引擎错误 → `503 DEPENDENCY_UNAVAILABLE` | TDD §7.5 fail closed；错误与成功需可区分 | 中 |
| 审计 | 本里程碑**只写 slog**，不建 `audit_logs` 表 | PRD 将审计划归 Milestone 5；与 M3 一致 | 高 |

### 预置角色权限矩阵（本计划锁定，M5 可调）

| 角色 | 权限 |
| --- | --- |
| `owner` | `tenant:read`、`tenant:update`、`tenant:delete`、`member:read`、`member:invite`、`member:update`、`member:remove`、`role:read`、`role:create`、`role:update`、`role:delete`、`role:assign`、`permission:read`、`permission:assign`、`audit:read` |
| `admin` | `tenant:read`、`tenant:update`、`member:read`、`member:invite`、`member:update`、`member:remove`、`role:read`、`role:create`、`role:update`、`role:delete`、`role:assign`、`permission:read`、`audit:read` |
| `member` | `tenant:read`、`member:read`、`role:read`、`permission:read` |
| `viewer` | `tenant:read`、`member:read`、`role:read`、`permission:read` |

权限目录种子取 TDD §3.5 清单：`tenant:read|update|delete`、`member:read|invite|update|remove`、`role:read|create|update|delete|assign`、`permission:read|assign`、`audit:read`。

## Files to Change

| File | Action | Why |
| --- | --- | --- |
| `migrations/000006_create_permissions.{up,down}.sql` | CREATE | 系统权限目录（`code` 唯一） |
| `migrations/000007_create_roles.{up,down}.sql` | CREATE | 租户角色 + `UNIQUE(tenant_id,key)` + `UNIQUE(id,tenant_id)` |
| `migrations/000008_create_role_permissions.{up,down}.sql` | CREATE | 角色权限关联 + 复合外键 |
| `migrations/000009_add_memberships_tenant_unique.{up,down}.sql` | CREATE | 为 `membership_roles` 复合外键补 `UNIQUE(id,tenant_id)` |
| `migrations/000010_create_membership_roles.{up,down}.sql` | CREATE | 成员角色分配 + 双重复合外键 |
| `migrations/000011_create_policy_version.{up,down}.sql` | CREATE | 单行策略版本号，供多实例失效 |
| `internal/domain/permission/permission.go` (+test) | CREATE | 权限实体与 `resource:action` 不变量 |
| `internal/domain/role/role.go` (+test) | CREATE | 角色实体、key 格式、系统角色保护 |
| `internal/domain/role/assignment.go` (+test) | CREATE | 成员角色分配实体与租户一致性校验 |
| `internal/ports/authorizer.go` | CREATE | `Authorizer` 接口（签名照 `docs/TDD.md:594-603`） |
| `internal/ports/repositories.go` | UPDATE | 追加 `RoleRepository`、`PermissionRepository`、`PolicyRepository` |
| `internal/application/authorization/service.go` (+test) | CREATE | 授权判定用例：DB 校验链 + Casbin 决策 |
| `internal/application/role/service.go` (+test) | CREATE | 角色 CRUD、角色权限设置、删除前占用检查 |
| `internal/application/role/assignment.go` (+test) | CREATE | 成员角色分配、查询、最后 Owner 保护 |
| `internal/application/tenant/create.go` | UPDATE | 事务内预置系统角色与权限矩阵 + `p` 规则投影 |
| `internal/application/invitation/service.go` | UPDATE | 接受邀请时绑定角色 + `g` 规则投影 |
| `internal/application/membership/service.go` | UPDATE | 移除成员时删除 `g` 规则（撤权）+ 最后 Owner 保护 |
| `internal/adapters/postgres/permission_repository.go` | CREATE | GORM 实现 |
| `internal/adapters/postgres/role_repository.go` | CREATE | GORM 实现 |
| `internal/adapters/postgres/membership_role_repository.go` | CREATE | GORM 实现 |
| `internal/adapters/postgres/policy_repository.go` (+test) | CREATE | `casbin_rule` 投影写入，走 `dbFrom(ctx)`；版本号递增 |
| `internal/adapters/casbin/authorizer.go` (+test) | CREATE | 实现 `ports.Authorizer` + 版本驱动的重载 |
| `internal/bootstrap/casbin.go` | UPDATE | 返回 `*casbin.SyncedEnforcer`；新增 `NewAuthorizer` |
| `internal/bootstrap/rbac.go` | CREATE | 装配角色/权限/策略依赖 |
| `internal/bootstrap/app.go` | UPDATE | `App` 增字段、`New` 接线、`Check` 增加策略版本校验 |
| `internal/config/config.go` (+test) | UPDATE | 新增 `POLICY_REFRESH_INTERVAL`、`DEFAULT_MEMBER_ROLE_KEY` |
| `internal/transport/http/handlers/permission.go` | CREATE | `GET /api/v1/permissions` |
| `internal/transport/http/handlers/role.go` | CREATE | 角色 CRUD + `PUT .../permissions` |
| `internal/transport/http/handlers/member.go` | UPDATE | 成员角色 `GET`/`PUT` |
| `internal/transport/http/handlers/authorize.go` | CREATE | `POST /api/v1/authorize` |
| `internal/transport/http/middleware/require_permission.go` (+test) | CREATE | 权限守卫中间件 |
| `internal/transport/http/handlers/errors.go` | UPDATE | 新增 RBAC 错误码与映射 |
| `internal/transport/http/router.go` | UPDATE | 挂载新路由与权限中间件 |
| `tests/integration/rbac_test.go` | CREATE | RBAC 端到端 + 事务一致性 + 撤权 |
| `tests/integration/tenancy_stack_test.go` | UPDATE | 栈装配含 RBAC 依赖 |
| `tests/integration/tenancy_test.go` | UPDATE | 适配权限收紧后的状态码预期 |
| `.env.example` / `README.md` | UPDATE | 新环境变量、角色矩阵、撤权窗口说明 |

## NOT Building

- **审计表与审计查询接口**（`audit_logs`、`GET /tenants/:tenantID/audit-logs`）—— Milestone 5；本里程碑只写 slog
- **Redis 缓存与 pub/sub watcher** —— Phase 6；本里程碑用版本号 + 轮询
- **Vue 管理后台前端** —— PRD Open Question 未决
- **ABAC / ReBAC / 组织树继承 / 策略脚本引擎 / 审批流** —— PRD Out of scope
- **租户停用/删除/恢复等平台管理接口** —— Milestone 5（M3 已建 `status` 字段）
- **新建权限目录的写接口** —— 权限集合由系统迁移维护，不提供管理 API
- **角色继承（role → role）** —— 首期不支持，`g` 规则只表达 user → role
- **服务间认证的 Client Credentials 流程** —— `/authorize` 本里程碑只接受用户令牌；服务身份留后续
- **`/me/permissions`** —— TDD §9.1 有此接口，但依赖"当前租户"上下文约定未定，留到 M5 与管理后台一并定义

---

## Tasks

### Task 1: RBAC 四张表与版本表迁移
- **Action**: 新增 `000006`–`000011` 六对迁移
- **Implement**（关键约束）:
  ```sql
  -- permissions（系统权限目录，无 tenant_id）
  code        VARCHAR(128) NOT NULL,
  resource    VARCHAR(64)  NOT NULL,
  action      VARCHAR(64)  NOT NULL,
  CREATE UNIQUE INDEX idx_permissions_code ON permissions (code);

  -- roles
  tenant_id  UUID NOT NULL REFERENCES tenants(id),
  key        VARCHAR(64) NOT NULL,
  is_system  BOOLEAN NOT NULL DEFAULT false,
  CREATE UNIQUE INDEX idx_roles_tenant_key  ON roles (tenant_id, key);
  CREATE UNIQUE INDEX idx_roles_id_tenant   ON roles (id, tenant_id);   -- 供复合外键

  -- role_permissions（冗余 tenant_id 以支撑复合外键）
  tenant_id UUID NOT NULL,
  CREATE UNIQUE INDEX idx_role_permissions_role_permission ON role_permissions (role_id, permission_id);
  ALTER TABLE role_permissions ADD CONSTRAINT fk_role_permissions_tenant
      FOREIGN KEY (role_id, tenant_id) REFERENCES roles (id, tenant_id);

  -- 000009：为 memberships 补复合外键所需的唯一约束
  CREATE UNIQUE INDEX idx_memberships_id_tenant ON memberships (id, tenant_id);

  -- membership_roles
  tenant_id   UUID NOT NULL,
  assigned_by UUID NOT NULL REFERENCES users(id),
  CREATE UNIQUE INDEX idx_membership_roles_unique ON membership_roles (membership_id, role_id);
  ALTER TABLE membership_roles ADD CONSTRAINT fk_membership_roles_membership
      FOREIGN KEY (membership_id, tenant_id) REFERENCES memberships (id, tenant_id);
  ALTER TABLE membership_roles ADD CONSTRAINT fk_membership_roles_role
      FOREIGN KEY (role_id, tenant_id) REFERENCES roles (id, tenant_id);

  -- policy_version（单行）
  id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  version BIGINT NOT NULL DEFAULT 0,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now();
  INSERT INTO policy_version (id, version) VALUES (1, 0);
  ```
- **Mirror**: Data access（`migrations/000004_create_memberships.up.sql`）
- **Gotcha**:
  - **顺序不可调换**：`000009` 必须在 `000010` 之前，否则复合外键无唯一约束可引用
  - `permissions` 不要加 `tenant_id`：它是系统级目录，加租户列会诱导"租户自建权限"
  - `assigned_by` 记录的是**内部 user UUID**（来自 Principal），不接受请求体传入
- **Validate**: `make migrate-up` 后用 `\d membership_roles` 确认两个复合外键存在；`make migrate-down` 可精确回滚

### Task 2: 领域实体（permission / role / assignment）
- **Action**: 三个领域包与单测，**先写失败测试**
- **Implement**:
  - `permission`: `NewPermission(resource, action, description) (*Permission, error)`；校验 `resource`/`action` 非空且匹配 `^[a-z][a-z0-9_]*$`；`Code()` 返回 `resource:action`；`ParseCode(code) (resource, action string, err error)`
  - `role`: `NewRole(tenantID uuid.UUID, key, name, description string, isSystem bool) (*Role, error)`；`key` 格式 `^[a-z][a-z0-9_]{1,62}$`、保留字校验；`Rename(name) bool`、`SetDescription(string) bool`；`ErrSystemRoleImmutable`（系统角色改 key/删除时返回）
  - `role.Assignment`: `NewAssignment(tenantID, membershipID, roleID, assignedBy) (*Assignment, error)`；构造时校验 `tenantID` 非 nil 且三个 ID 互不相同，注释说明"同租户的最终保证在数据库复合外键"
- **Mirror**: Naming（`internal/domain/tenant/tenant.go:15,27,71,107`）
- **Gotcha**: 领域层**不得**引入 Casbin 或 `resource:action` 之外的策略概念；权限目录的"是否存在"由仓储校验，不在领域层硬编码清单
- **Validate**: `go test ./internal/domain/...` 全绿

### Task 3: 策略投影仓储（同事务写入 `casbin_rule`）
- **Action**: `internal/adapters/postgres/policy_repository.go` + `ports.PolicyRepository`
- **Implement**:
  ```go
  type PolicyRepository interface {
      // ReplaceRolePermissions 用给定权限集合整体替换某角色在本租户的 p 规则。
      ReplaceRolePermissions(ctx, tenantID, roleID string, perms []PermissionRef) error
      // ReplaceMemberRoles 用给定角色集合整体替换某成员在本租户的 g 规则。
      ReplaceMemberRoles(ctx, tenantID, userID string, roleIDs []string) error
      // DeleteTenant 删除租户的全部 p/g 规则（租户删除或重置时）。
      DeleteTenant(ctx, tenantID string) error
      // DeleteRole 删除某角色的 p 规则与所有引用它的 g 规则。
      DeleteRole(ctx, tenantID, roleID string) error
      // BumpVersion 在同一事务内递增全局策略版本号。
      BumpVersion(ctx) (int64, error)
  }
  ```
  - GORM 模型 `casbinRuleModel`（`TableName() = "casbin_rule"`），字段 `PType, V0..V5`
  - 列映射集中在**一个**函数内（`policyRule(...) casbinRuleModel`），并写清注释：`p`: v0=role_id, v1=tenant_id, v2=resource, v3=action；`g`: v0=user_id, v1=role_id, v2=tenant_id
  - 所有方法走 `dbFrom(ctx, r.db)`
  - `Replace*` 用"先 `DELETE WHERE ...` 再批量 `INSERT`"，**同一事务内**完成
- **Mirror**: Data access（`internal/adapters/postgres/tx.go:42-47`）
- **Gotcha**:
  - **绝不**在事务外调用 `enforcer.AddPolicy`：那会用默认连接写入，事务回滚后策略残留 → 权限幽灵
  - `DELETE` 条件必须带 `tenant_id`，避免误删其他租户
  - `BumpVersion` 用 `UPDATE policy_version SET version = version + 1 WHERE id = 1 RETURNING version`，必须走同一 tx
- **Validate**: 集成测试断言——投影写入后 `enforcer.LoadPolicy()`，`Enforce(userID, tenantID, "member", "invite")` 与业务表一致；事务回滚后 `casbin_rule` 无残留

### Task 4: 授权引擎升级为并发安全 + Authorizer 适配器
- **Action**: `internal/bootstrap/casbin.go`、`internal/adapters/casbin/authorizer.go`、`internal/ports/authorizer.go`
- **Implement**:
  - `ports.Authorizer` 签名照 `docs/TDD.md:594-603`，注释写明 `allowed=false, err=nil` 与 `err!=nil` 的语义差别
  - `NewEnforcer` 改用 `casbin.NewSyncedEnforcer(modelPath, adapter)`，返回 `*casbin.SyncedEnforcer`；`bootstrap/app.go:27` 字段类型同步修改
  - `casbin.Authorizer` 实现 `Enforce`：`enforcer.Enforce(subject, tenantID, resource, action)`，把 Casbin 内部错误包装为 `fmt.Errorf("授权决策失败：%w", err)`
  - 版本驱动重载：`Authorizer.Refresh(ctx)` 比对 DB 版本与本地版本，不一致则 `LoadPolicy()`；提供 `StartRefresher(ctx, interval)` 由 `cmd/api/main.go` 以 goroutine 启动并在关停时退出
  - 重载失败**不得**静默：记 `slog.Error` 并让 `App.Check` 返回错误（就绪检查失败 → 摘流量），而不是继续用旧策略放行
- **Mirror**: Errors（`internal/adapters/postgres/membership_repository.go:49`）
- **Gotcha**:
  - `SyncedEnforcer` 的 `LoadPolicy` 自带锁，不要再套一层锁导致死锁
  - **刷新 goroutine 不能吞掉 panic**：用 `defer recover` + `slog.Error`，并把刷新失败反映到就绪检查
  - `Enforce` 返回 `(false, err)` 时上层必须 fail closed，不允许当作"拒绝"后继续走默认放行分支
- **Validate**: `go test ./internal/adapters/casbin/...`；集成测试并发 `Enforce` + 并发 `Refresh` 无 data race（`go test -race`）

### Task 5: 角色与权限用例
- **Action**: `internal/application/role/service.go`
- **Implement**:
  - `ListRoles(ctx, tenantID)`、`GetRole(ctx, tenantID, roleID)`（未命中或跨租户 → `ErrRoleNotFound`）
  - `CreateRole(ctx, tenantID, key, name, description)`：key 冲突 → `ErrRoleKeyConflict`；`is_system=false`
  - `UpdateRole(ctx, tenantID, roleID, name, description)`：系统角色只允许改 `name`/`description`
  - `DeleteRole(ctx, tenantID, roleID)`：系统角色 → `ErrSystemRoleImmutable`；仍有成员使用 → `ErrRoleInUse`；**同一事务内**删除 `role_permissions`、`membership_roles`、`casbin_rule` 投影并 `BumpVersion`
  - `SetRolePermissions(ctx, tenantID, roleID, codes []string)`：
    1. 逐个 code 查 `permissions` 目录，**任一不存在 → `ErrPermissionNotFound`（不部分应用）**
    2. 事务内替换 `role_permissions` + `ReplaceRolePermissions` 投影 + `BumpVersion`
    3. 提交后刷新本地 enforcer
  - `ListPermissions(ctx)`：返回系统权限目录
- **Mirror**: Errors（`internal/application/tenancy/access.go:20-28`）
- **Gotcha**:
  - 权限集合只能取系统目录，**不接受**请求体里不存在的 code（TDD §10.2）
  - 删除角色与权限变更都要删除/重建投影，否则策略残留即"权限幽灵"
  - 提交后刷新失败要返回错误（已提交但内存未更新 → 必须让就绪检查失败，不能假装成功）
- **Validate**: `go test ./internal/application/role/...` 覆盖：创建、key 冲突、系统角色保护、占用检查、非法权限 code、权限变更生效

### Task 6: 成员角色分配用例
- **Action**: `internal/application/role/assignment.go`
- **Implement**:
  - `GetMemberRoles(ctx, tenantID, userID)`：join `membership_roles` + `roles`，**带 tenant 条件**
  - `SetMemberRoles(ctx, actorUserID, tenantID, userID, roleKeys []string)`：
    1. 校验 membership 属于该租户且 active（复用 `tenancy.AccessService` 语义）
    2. 角色按 `key` 解析，**必须属于同一租户**（SQL 带 `tenant_id`；跨租户角色 → `ErrRoleNotFound`）
    3. **Owner 保护**：若移除后该租户 active owner 数为 0 → `ErrCannotRemoveLastOwner`
    4. 事务内替换 `membership_roles` + `ReplaceMemberRoles` 投影 + `BumpVersion`
  - 用例依赖：`memberships`、`roles`、`membership_roles`、`policy`、`tx`
- **Mirror**: Errors（`internal/application/tenancy/access.go:64`）
- **Gotcha**:
  - "最后一个有效 Owner" 统计必须是 **active membership + active owner 角色** 的联合计数，只看 membership 或只看角色都会漏
  - 不能提供批量接口绕过单项权限检查（TDD §10.2）：`SetMemberRoles` 整体作为一个受 `role:assign` 保护的单项操作
- **Validate**: `go test ./internal/application/role/...` 覆盖：分配、跨租户角色拒绝、最后 owner 保护、撤权（移除 `member:invite` 后判定变 false）

### Task 7: 接入既有流程（租户创建、接受邀请、移除成员）
- **Action**: 修改 `tenant/create.go`、`invitation/service.go`、`membership/service.go`
- **Implement**:
  - `tenant.CreateService.Create`：在**已有事务内**追加——创建 4 个 `is_system` 角色 → 绑定预置权限矩阵 → `ReplaceRolePermissions` 投影 → `BumpVersion`
  - `invitation.Service.Accept`：事务内创建/恢复 membership 后，`CountActiveByTenant`：若为 1 → 绑定 `owner`；否则 → 绑定 `DEFAULT_MEMBER_ROLE_KEY`（默认 `member`）；写 `membership_roles` + `ReplaceMemberRoles` 投影 + `BumpVersion`。重复接受幂等
  - `membership.Service.RemoveMember`：事务内软删除 membership + `ReplaceMemberRoles(ctx, tenantID, userID, nil)`（清空 `g` 规则，即撤权）+ `BumpVersion`；移除前做最后 Owner 检查
- **Mirror**: Bootstrap（`internal/bootstrap/tenancy.go:51`）
- **Gotcha**:
  - `invitation.Service` 新增 `role.AssignmentService` 依赖会形成装配顺序约束：assignment **不得**反向依赖 invitation（否则循环）
  - M3 的"不能移除最后一个 active 成员"与本里程碑的"不能移除最后一个有效 Owner"**同时保留**
  - 成员被移除后其 `g` 规则必须立刻消失；即使投影删除成功但刷新失败，DB 层的 `TenantGuard` 仍会拒绝访问（双保险）
- **Validate**: 集成测试——创建租户后接受邀请的成员自动成为 owner；第二个成员为 member；移除成员后立刻无法访问

### Task 8: 授权判定用例与 `/authorize`
- **Action**: `internal/application/authorization/service.go` + `handlers/authorize.go`
- **Implement**: `Authorize(ctx, principal, tenantID, resource, action) (bool, error)`：
  1. `principal.Authenticated` 为 false → `ports.ErrInvalidToken`
  2. `tenancy.AccessService.Check(userID, tenantID)` → `ErrTenantNotFound`/`ErrTenantSuspended`/`ErrMembershipInactive` 均为**业务性拒绝**，返回 `(false, nil)`
  3. 其他错误（DB/引擎不可用）→ 原样返回，`(false, err)` → 传输层 503
  4. `Authorizer.Enforce(ctx, userID, tenantID, resource, action)` → `(allowed, err)`
  5. `slog.Info` 记录判定结果（subject、tenant、resource、action、allowed），**不含令牌**
- **HTTP**: `POST /api/v1/authorize`，body 仅 `{tenant_id, resource, action}`；响应 `{"data":{"allowed":true}}`
- **Mirror**: Transport（`internal/transport/http/handlers/member.go:74`）
- **Gotcha**:
  - 请求体**不得**出现 `user_id`/`subject` 字段，收到即忽略（TDD §9.5）
  - 权限判定必须发生在 DB 校验**之后**；顺序颠倒会让非本租户成员拿到 200 + allowed=false 之外的信息
  - `allowed=false` 与 503 必须可区分，否则业务服务会把"依赖故障"误判为"无权限"而静默放行
- **Validate**: 集成测试——owner 邀请成员 200/allowed=true；viewer 邀请成员 allowed=false；非成员 allowed=false；DB 故障注入 → 503

### Task 9: 权限守卫中间件与路由收紧
- **Action**: `internal/transport/http/middleware/require_permission.go` + `router.go`
- **Implement**: `RequirePermission(authorizer ports.Authorizer, resource, action string)`：
  1. Principal 缺失/未认证 → 401（正常由 `AuthN` 拦截，此处兜底）
  2. 租户 ID 从 `middleware.TenantIDFrom(c)` 取（只信任 `TenantGuard` 写入的值）
  3. 调用 `authorization.Service.Authorize`；`allowed=false` → `403 FORBIDDEN`；`err != nil` → `503 DEPENDENCY_UNAVAILABLE`
  4. 必须 `c.Abort()`
- **路由**（在 `TenantGuard` 之后挂载，`TenantGuard` 仍先跑）:
  ```
  GET    /api/v1/permissions                                    AuthN                      权限目录
  POST   /api/v1/authorize                                      AuthN                      统一授权判定

  GET    /api/v1/tenants/:tenantID                              Guard + tenant:read
  PATCH  /api/v1/tenants/:tenantID                              Guard + tenant:update
  GET    /api/v1/tenants/:tenantID/members                      Guard + member:read
  GET    /api/v1/tenants/:tenantID/members/:userID              Guard + member:read
  PATCH  /api/v1/tenants/:tenantID/members/:userID              Guard + member:update
  DELETE /api/v1/tenants/:tenantID/members/:userID              Guard + member:remove
  GET    /api/v1/tenants/:tenantID/members/:userID/roles        Guard + role:read
  PUT    /api/v1/tenants/:tenantID/members/:userID/roles        Guard + role:assign

  POST   /api/v1/tenants/:tenantID/invitations                  Guard + member:invite
  GET    /api/v1/tenants/:tenantID/invitations                  Guard + member:read
  DELETE /api/v1/tenants/:tenantID/invitations/:invitationID    Guard + member:invite

  GET    /api/v1/tenants/:tenantID/roles                        Guard + role:read
  POST   /api/v1/tenants/:tenantID/roles                        Guard + role:create
  GET    /api/v1/tenants/:tenantID/roles/:roleID                Guard + role:read
  PATCH  /api/v1/tenants/:tenantID/roles/:roleID                Guard + role:update
  DELETE /api/v1/tenants/:tenantID/roles/:roleID                Guard + role:delete
  PUT    /api/v1/tenants/:tenantID/roles/:roleID/permissions    Guard + permission:assign
  ```
- **Mirror**: Transport（`internal/transport/http/router.go:59-60`、`middleware/tenant_guard.go:48`）
- **Gotcha**:
  - `RequirePermission` **必须**在 `TenantGuard` 之后，否则拿不到可信租户 ID
  - 为每条路由显式挂权限，不用"整个 Group 挂一个宽松权限"——后者会让 viewer 拿到写权限
  - 若某条路由忘记挂 → 等价于仅成员校验即可访问，**这是本里程碑最大的越权面**，需有一条测试枚举所有租户级路由断言均被保护
- **Validate**: 集成测试逐条验证状态码；新增一条"路由权限覆盖"测试遍历已注册路由

### Task 10: 错误码与响应映射
- **Action**: `internal/transport/http/handlers/errors.go`
- **Implement**: 新增常量 `ROLE_KEY_CONFLICT`、`ROLE_NOT_FOUND`、`ROLE_IN_USE`、`SYSTEM_ROLE_IMMUTABLE`、`PERMISSION_NOT_FOUND`、`CANNOT_REMOVE_LAST_OWNER`、`FORBIDDEN`、`DEPENDENCY_UNAVAILABLE`；`writeError` 增加对应 `case`
  - `ErrRoleKeyConflict` → 409；`ErrRoleNotFound` → 404；`ErrRoleInUse` → 409；`ErrSystemRoleImmutable` → 409；`ErrPermissionNotFound` → 400；`ErrCannotRemoveLastOwner` → 409
- **Mirror**: Errors（`internal/transport/http/handlers/errors.go:19-42,47-84`）
- **Gotcha**: `default` 分支必须保持"记 ERROR 日志 + 500 INTERNAL"，不要把新的内部错误泄漏成 400
- **Validate**: `go test ./internal/transport/http/...` 覆盖新增映射

### Task 11: 集成与安全回归测试
- **Action**: `tests/integration/rbac_test.go` + 更新 `tenancy_stack_test.go`、`tenancy_test.go`
- **Implement**（复用 `newTenancyStack` 并扩展 RBAC 依赖装配）:
  - **授权判定**：owner 邀请成员 → `/authorize{member,invite}` allowed=true；viewer → false；非成员 → false；跨租户 → false
  - **跨租户角色分配**：Tenant A 的成员 + Tenant B 的 role key → 404/403；直接构造跨租户 `membership_roles` 写入 → **数据库复合外键拒绝**
  - **撤权**：移除成员后 `/authorize` 立即 false（不等待刷新窗口）；撤销 `member:invite` 权限后 owner 判定立即 false
  - **事务一致性**：注入投影写入失败 → `role_permissions` 与 `casbin_rule` 均无残留（回滚验证）
  - **管理员保护**：唯一 owner 不可被移除（409）；系统角色不可删除（409）；被占用的自定义角色不可删除（409）；非法权限 code → 400 且不部分应用
  - **默认拒绝**：无任何角色的成员访问成员列表 → 403
  - **故障关闭**：DB 不可用时 `/authorize` → 503 而非 allowed=false
- **Mirror**: Tests（`tests/integration/tenancy_stack_test.go:43`、`bootstrap_test.go:271`）
- **Gotcha**:
  - M3 既有测试的成员需先有角色才能通过权限校验；`acceptInvitation` 后自动绑定 owner，多数用例无需改动，但**直接置库构造的成员**需要补角色
  - 并发用例用 `-race` 跑，验证 `SyncedEnforcer` 改造有效
- **Validate**: `go test ./tests/integration/... -count=1 -race` 全绿

### Task 12: 配置、装配与文档
- **Action**: `config` 新增 `POLICY_REFRESH_INTERVAL`（默认 `5s`，必须为正）、`DEFAULT_MEMBER_ROLE_KEY`（默认 `member`）；`bootstrap/rbac.go` 装配；`app.go` 接线；更新 `.env.example` 与 README
- **Implement**: 沿用 `internal/config/config.go:26-59` 的"默认值 + 环境变量名集中定义"与 `errors.Join` 聚合校验；`App` 增 `Authorizer`、`Authorization`、`RoleService`、`AssignmentService`、`Permissions`、`Roles`、`MembershipRoles`、`Policies`、`PolicyRefresher`
- **README 需写明**:
  - 权限目录由迁移种子维护，租户只能在既有集合内组合角色
  - 撤权窗口：成员/租户级撤权即时生效；**角色权限矩阵变更在其他实例上最多滞后 `POLICY_REFRESH_INTERVAL`**
  - 多实例部署仍需 Redis watcher（Phase 6），当前版本号轮询是过渡方案
- **Mirror**: Bootstrap（`internal/bootstrap/tenancy.go:19`、`app.go:56-109`）
- **Gotcha**: `POLICY_REFRESH_INTERVAL` 为 0 或负数必须启动失败；`DEFAULT_MEMBER_ROLE_KEY` 必须在预置角色中存在，否则接受邀请会失败（启动期可校验目录里是否有该 key 的系统角色，但租户预置是运行时行为 → 改为在预置时校验，缺失则报错）
- **Validate**: `go test ./internal/config/...`；`go build ./...`

---

## Validation

```bash
# 静态与构建
gofmt -l .                 # EXPECT: 无输出
make vet                   # EXPECT: 无输出
go build ./...             # EXPECT: exit 0

# 单元测试（不依赖 Docker）
make test                  # go test -short -race ./...  EXPECT: 全部 PASS

# 集成与安全回归（需 Docker）
make test-integration      # go test ./tests/integration/... -count=1  EXPECT: PASS
go test ./tests/integration/... -count=1 -race   # EXPECT: 无 data race

# 数据库约束
make migrate-up
docker compose exec postgres psql -U postgres -d iam -c '\d membership_roles'
# EXPECT: fk_membership_roles_membership / fk_membership_roles_role 两个复合外键存在
docker compose exec postgres psql -U postgres -d iam -c '\d roles'
# EXPECT: UNIQUE (tenant_id, key) 与 UNIQUE (id, tenant_id)

# 冒烟：授权判定
curl -H "Authorization: Bearer $OWNER_TOKEN" -X POST localhost:8080/api/v1/authorize \
  -d '{"tenant_id":"<uuid>","resource":"member","action":"invite"}'
# EXPECT: {"data":{"allowed":true},...}
curl -H "Authorization: Bearer $VIEWER_TOKEN" -X POST localhost:8080/api/v1/authorize \
  -d '{"tenant_id":"<uuid>","resource":"member","action":"invite"}'
# EXPECT: {"data":{"allowed":false},...}
curl -H "Authorization: Bearer $OWNER_TOKEN" -X POST localhost:8080/api/v1/authorize \
  -d '{"tenant_id":"<other-tenant>","resource":"member","action":"invite"}'
# EXPECT: allowed=false（跨租户不得命中策略）
```

## Risks

| Risk | Likelihood | Mitigation |
| --- | --- | --- |
| **策略投影列约定（v0..v5）与 gorm-adapter 内部不一致** | 中 | 映射集中在单一函数 + 注释；集成测试用"写入后 `LoadPolicy` + `Enforce`"锁定约定；adapter 版本已在 `go.mod` 固定为 v3.41.0 |
| 事务外误调 `enforcer.AddPolicy` 导致回滚后策略残留 | 中 | Task 3 明确禁止；代码评审关注点；集成测试验证回滚后 `casbin_rule` 无残留 |
| 多实例策略窗口内撤权滞后 | 中 | 版本号 + 轮询（默认 5s）；成员级撤权走 DB 校验不受窗口影响；README 明确窗口；Redis watcher 留 Phase 6 |
| `SyncedEnforcer` 改造引入死锁或性能下降 | 低 | 不再额外加锁；`-race` 并发测试；压测留 Phase 6 |
| 权限收紧导致 M3 既有集成测试回归 | 中 | Task 11 同步更新；直接置库构造的成员需补角色；`acceptInvitation` 自动绑定 owner |
| 新建路由漏挂 `RequirePermission` | 中 | 逐条显式挂载 + "路由权限覆盖"测试枚举已注册路由 |
| 复合外键迁移在已有数据上失败 | 低 | 迁移前用 `NOT EXISTS` 校验约束；staging 先行；down 文件可回滚 |
| 预置权限矩阵与产品预期不符 | 中 | 计划锁定矩阵并标注"M5 可调"；矩阵集中在种子迁移与一处常量，便于调整 |
| `DEFAULT_MEMBER_ROLE_KEY` 指向不存在的角色 | 低 | 预置角色时校验该 key 存在，缺失即报错（快速失败） |
| 权限目录后续扩充需改迁移 | 中 | 接受：目录由系统维护正是设计意图（TDD §3.5）；新增权限走新迁移 |

## Acceptance

- [ ] `gofmt -l .` 无输出、`go vet ./...` 无错误、`go build ./...` 通过
- [ ] `go test -short -race ./...` 全绿；`go test ./tests/integration/... -count=1 -race` 全绿
- [ ] 权限目录由系统迁移维护，租户只能在既有集合内组合角色，无新建权限的写接口
- [ ] 租户创建时预置 `owner/admin/member/viewer` 四个系统角色与默认权限矩阵；系统角色不可删除、不可改 `key`
- [ ] 成员可分配角色；**跨租户角色分配被数据库复合外键拒绝**
- [ ] `POST /authorize` 只接受 `{tenant_id, resource, action}`，身份仅来自已验证令牌
- [ ] 未定义权限默认拒绝；无角色的成员访问受保护接口返回 403
- [ ] 授权依赖不可用时返回 503，**不出现任何自动放行**
- [ ] 业务关系表与 `casbin_rule` 投影在同一事务内更新；注入失败后无残留
- [ ] 撤权正确：移除成员、撤销角色权限后 `/authorize` 立即返回 false
- [ ] 不能移除最后一个有效 Owner；不能删除仍被占用的角色；系统角色受保护
- [ ] 所有租户级接口均挂载显式权限，且有测试枚举覆盖
- [ ] 新增表与约束均通过版本化迁移，`migrate-down` 可精确回滚
- [ ] 未越界实现 NOT Building 中的任何一项
- [ ] Patterns mirrored, not reinvented：沿用既有命名、错误、数据访问与测试约定

---

**WAITING FOR CONFIRMATION**：是否按此计划实施？可回复 `yes` / `modify: <改动>` / `different approach: <方案>`。
