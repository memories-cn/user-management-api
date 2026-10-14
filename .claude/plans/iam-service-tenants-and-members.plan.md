# Plan: 租户与成员管理（PRD Milestone 3）

**Source PRD**: `.claude/prds/iam-service.prd.md`
**Selected Milestone**: 3 — 租户与成员
**Complexity**: Large

> **变更记录**：
> 1. 初版采用"受控的自助创建（Freemium/PLG）"→ 改为 **仅平台管理员可创建租户**。
> 2. 平台管理员判定 → **仅使用 ZITADEL 角色声明**，不提供环境变量白名单或任何应急通道。

## Summary

实现业务租户与租户成员：新增 `tenants`、`memberships`、`invitations` 三张表与迁移；租户**只能由持有 ZITADEL 平台管理员角色的身份创建**，创建时指定 owner 邮箱并由系统发出邀请；成员管理遵循"先认证、后绑定"的邀请机制（7 天有效期、幂等、标准接受流程）；新增租户守卫中间件，保证所有租户级接口强制执行"用户有效 + 租户有效 + 成员关系有效 + 资源属于该租户"的校验链。

验收口径（PRD Milestone 3）：租户管理员可管理本租户成员及成员状态；所有租户级接口执行成员资格与租户状态检查。

## Requirements Restatement

- **租户创建**：**仅平台管理员可创建**，普通认证用户一律拒绝（403）。平台管理员身份**唯一来源**是已验证令牌中的 ZITADEL 角色声明；平台权限走**独立、显式的授权路径**（`/api/v1/platform/...`），不复用租户 RBAC，不使用通配租户或空租户隐式提权（TDD §7.6）。
- **租户归属**：创建时必须指定 `owner_email`，系统创建 pending 邀请；平台管理员本人**不**自动成为该租户成员（职责分离）。`tenants.created_by` 记录创建者。
- **成员邀请**：遵循**先认证、后绑定**——邀请只绑定"已通过 ZITADEL 认证且邮箱已验证"的身份，绝不因为一个邮箱字符串就创建可登录用户。有效期 **7 天**，重复邀请与重复接受**幂等**，提供**标准接受流程**。
- **租户隔离**：所有租户级接口必须校验成员资格与租户状态，跨租户访问一律拒绝。

## Patterns to Mirror

> 以下均来自仓库中**已存在并通过测试**的代码，实施时必须沿用，不要另起风格。

| Category | Source | Pattern |
| --- | --- | --- |
| Naming | `internal/domain/user/user.go:16,29,45` | `type Status string` + 状态常量；`New{Entity}(必填参数) (*Entity, error)` 构造并校验不变量 |
| Naming | `internal/domain/user/user.go:80` | 变更类方法返回 `bool` 表示"是否发生变更"，空白值不覆盖已有值 |
| Errors | `internal/ports/identity.go:12,15` | 哨兵错误定义在 `ports`，适配器与用例用 `fmt.Errorf("%w：%v", Err, err)` 包裹 |
| Errors | `internal/application/user/sync.go:17,54` | 用例层定义业务哨兵错误（如 `ErrUserDisabled`），由传输层统一映射 |
| Errors | `internal/transport/http/middleware/authn.go:22,93` | 错误码常量 + `handleXxxError` 集中做 `errors.Is` → HTTP 状态码映射 |
| Errors | `internal/transport/http/responses/response.go` | 失败统一 `responses.Fail(c, status, code, message)`，request_id 自动一致 |
| Data access | `internal/adapters/postgres/user_repository.go:29,81` | GORM 模型（`xxxModel` + `TableName()`）只存在于 adapter，领域实体保持干净；`fromDomain`/`toDomain` 转换 |
| Data access | `internal/adapters/postgres/user_repository.go:45` | 查询未命中返回 `(nil, nil)`，未命中不是错误 |
| Data access | `migrations/000002_create_users.up.sql` | 约束落在数据库：显式命名唯一索引、CHECK 约束、down 文件可精确回滚 |
| Transport | `internal/transport/http/router.go:28-29` | 分组统一挂载中间件，避免个别路由漏挂造成越权 |
| Transport | `internal/transport/http/middleware/authn.go:110` | 从请求中提取信息的工具函数单独成函数并单测 |
| Bootstrap | `internal/bootstrap/auth.go:18,23,28` | 每个依赖一个 `NewXxx` 构造函数，返回 `ports` 接口或用例指针 |
| Bootstrap | `internal/bootstrap/app.go:33` | `New(cfg)` 顺序装配，任一步失败即返回；`App` 聚合依赖并实现 `Check(ctx)` |
| Tests | `internal/domain/user/user_test.go` | 领域单测：构造校验、幂等、空白值不覆盖 |
| Tests | `tests/integration/bootstrap_test.go:132,294` | 集成测试复用 `newDatabase` / `migrateUp`，每用例独立数据库 |
| Tests | `tests/integration/identity_test.go:32` | `newIdentityStack` 一次装配"真实 DB + OIDC 桩 + 路由"，用真实 HTTP 请求验证 |

## 本计划锁定的决策

| 决策 | 选择 | 理由 | 可逆性 |
| --- | --- | --- | --- |
| 租户创建准入 | **仅平台管理员**；普通认证用户 → `403 FORBIDDEN` | 用户明确要求 | 低 |
| **平台管理员判定** | **仅 ZITADEL 角色声明**：令牌 claim `urn:zitadel:iam:user:roles` 的 key 集合包含 `PLATFORM_ADMIN_ROLE_KEY`（默认 `platform_admin`） | 授予/撤销在身份平台完成，无需改配置或重启；单一来源避免权限口径分裂 | 中 |
| 白名单 / 应急通道 | **明确不提供** | 用户明确要求去掉白名单；避免绕过身份平台授予权限 | 低 |
| 平台管理员路由 | 独立前缀 `/api/v1/platform/...`，挂 `RequirePlatformAdmin` | TDD §7.6：平台权限独立、显式 | 中 |
| 平台管理员自动入租户 | **否**。创建时必须指定 `owner_email`，系统创建 pending 邀请 | 职责分离：平台管理员不应因平台权限自动获得租户内访问权 | 中 |
| slug 唯一范围 | 全局唯一（`idx_tenants_slug`），校验格式与保留字 | TDD §3.2 明确 `slug` 唯一 | 中 |
| owner 角色 | **本里程碑不建 `roles` 表**；M4 预置角色时把"首个接受邀请的成员"绑定为 owner | 保持里程碑边界；避免 M3 实现半成品授权 | 中 |
| 邀请绑定依据 | 接受时必须：已认证 **且** `email_verified=true` **且** `email` 与邀请邮箱一致 | "先认证、后绑定" | 低 |
| 邀请令牌 | 32 字节随机 token，**只存 SHA-256**；明文仅创建响应返回一次 | 防数据库泄露导致邀请被冒用；生产应改为邮件投递 | 中 |
| 重复邀请 / 重复接受 | 均幂等（复用 pending 邀请并刷新有效期 / 已接受则直接成功） | 幂等，避免重复提交产生不一致 | 高 |
| 过期语义 | 接受时判定 `expires_at`，过期返回 `409 INVITATION_EXPIRED` | 无需后台作业即可正确拒绝 | 高 |
| 非成员访问 | 返回 **404**（而非 403），隐藏资源存在性 | TDD §9.6 允许按防枚举策略隐藏存在性 | 中 |
| 成员删除 | 软删除：`status='removed'`，保留唯一约束 | TDD §8.2 明确软删除策略 | 低 |
| 事务 | 引入 `ports.Transactor`，"创建租户 + 创建 owner 邀请"、"接受邀请 + 创建成员"均在同一事务 | 避免半成品数据；也是 M4 角色/策略同事务的前置能力 | 中 |
| 授权粒度 | 租户级接口只校验 active membership，**不做** role/permission 判定 | TDD Phase 3 验收只要求成员资格与租户状态检查 | 高 |

## Files to Change

| File | Action | Why |
| --- | --- | --- |
| `migrations/000003_create_tenants.up/down.sql` | CREATE | 租户表 |
| `migrations/000004_create_memberships.up/down.sql` | CREATE | 成员关系表 + `(tenant_id, user_id)` 唯一 |
| `migrations/000005_create_invitations.up/down.sql` | CREATE | 邀请表 + pending 部分唯一索引 |
| `internal/domain/tenant/tenant.go` (+test) | CREATE | 租户实体与不变量 |
| `internal/domain/membership/membership.go` (+test) | CREATE | 成员关系实体与状态机 |
| `internal/domain/invitation/invitation.go` (+test) | CREATE | 邀请实体（token hash、有效期、状态） |
| `internal/application/platformadmin/policy.go` (+test) | CREATE | 平台管理员判定（仅 ZITADEL 角色） |
| `internal/transport/http/middleware/platform_admin.go` (+test) | CREATE | 平台管理员守卫中间件 |
| `internal/ports/repositories.go` | UPDATE | 增加租户/成员/邀请仓储接口 |
| `internal/ports/tx.go` | CREATE | `Transactor` 事务接口 |
| `internal/ports/identity.go` | UPDATE | `Claims` 增加 `Email`、`EmailVerified`、`Roles` |
| `internal/adapters/zitadel/verifier.go` | UPDATE | 提取 `email`/`email_verified`/`urn:zitadel:iam:user:roles` |
| `internal/domain/identity/principal.go` | UPDATE | `Principal` 增加 `Email`、`EmailVerified`、`Roles` |
| `internal/transport/http/middleware/authn.go` | UPDATE | 构造 Principal 时带上邮箱与角色 |
| `internal/application/tenant/create.go` (+test) | CREATE | 平台管理员创建租户 + owner 邀请（事务） |
| `internal/application/membership/service.go` (+test) | CREATE | 成员列表、状态变更、软删除 |
| `internal/application/invitation/service.go` (+test) | CREATE | 邀请、幂等、接受、撤销、列表 |
| `internal/adapters/postgres/tenant_repository.go` | CREATE | GORM 实现 |
| `internal/adapters/postgres/membership_repository.go` | CREATE | GORM 实现 |
| `internal/adapters/postgres/invitation_repository.go` | CREATE | GORM 实现 |
| `internal/adapters/postgres/tx.go` | CREATE | `Transactor` 实现 + 从 ctx 取事务句柄 |
| `internal/transport/http/middleware/tenant_guard.go` (+test) | CREATE | 租户守卫：成员资格 + 租户状态 |
| `internal/transport/http/handlers/platform.go` | CREATE | `POST /api/v1/platform/tenants` |
| `internal/transport/http/handlers/tenant.go` | CREATE | 租户查询与改名 |
| `internal/transport/http/handlers/member.go` | CREATE | 成员接口 |
| `internal/transport/http/handlers/invitation.go` | CREATE | 邀请与接受接口 |
| `internal/transport/http/router.go` | UPDATE | 挂载平台路由、租户路由与守卫 |
| `internal/config/config.go` (+test) | UPDATE | 新增 `PLATFORM_ADMIN_ROLE_KEY`、`INVITATION_TTL` |
| `internal/bootstrap/tenancy.go` | CREATE | 装配租户/成员/邀请/平台管理员依赖 |
| `internal/bootstrap/app.go` | UPDATE | 注入新依赖 |
| `internal/oidctest/stub.go` | UPDATE | 桩支持签发带邮箱与角色声明的令牌 |
| `tests/integration/tenancy_test.go` | CREATE | 平台管理员创建、租户隔离、成员管理 |
| `tests/integration/invitation_test.go` | CREATE | 邀请端到端与安全回归 |
| `.env.example` / `README.md` | UPDATE | 新环境变量与接口说明 |

## NOT Building

- **角色与权限**（`roles`、`permissions`、`role_permissions`、`membership_roles` 表与 Casbin 判定）—— Milestone 4；"不能移除最后一个有效 Owner"等依赖角色的保护也在 M4
- **`/authorize` 授权判定接口** —— Milestone 4
- **平台管理员白名单与应急通道**（`PLATFORM_ADMIN_SUBJECTS` 之类）—— 按用户要求明确不提供，平台管理员一律由 ZITADEL 角色授予
- **租户停用/删除/恢复等平台管理接口** —— 本里程碑只建 `status` 字段与"停用后拒绝访问"的校验，变更接口随 Milestone 5 补齐
- **审计日志表与查询** —— Milestone 5；本里程碑用 slog 记录平台管理操作，M5 落库
- **邮件投递邀请链接** —— MVP 通过响应返回 token，生产需改为邮件服务
- **Redis 缓存与策略失效** —— Phase 6
- **`/me/tenants`** —— 与 `GET /tenants`（我的租户）语义重复，不新增
- **租户删除、成员硬删除** —— 采用软删除，不提供物理删除接口

---

## Tasks

### Task 1: 三张业务表迁移
- **Action**: 新增 `000003_create_tenants`、`000004_create_memberships`、`000005_create_invitations`
- **Implement**（关键约束）：
  ```sql
  -- tenants
  status VARCHAR(32) NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended','deleted')),
  created_by UUID NOT NULL REFERENCES users(id),
  CREATE UNIQUE INDEX idx_tenants_slug ON tenants (slug);
  CREATE INDEX idx_tenants_created_by ON tenants (created_by);

  -- memberships
  status VARCHAR(32) NOT NULL DEFAULT 'active' CHECK (status IN ('active','pending','suspended','removed')),
  CREATE UNIQUE INDEX idx_memberships_tenant_user ON memberships (tenant_id, user_id);
  CREATE INDEX idx_memberships_tenant_status ON memberships (tenant_id, status);

  -- invitations
  token_hash BYTEA NOT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','accepted','cancelled','expired')),
  expires_at TIMESTAMPTZ NOT NULL,
  -- 幂等的数据库兜底：同一租户下同一邮箱只能有一条 pending 邀请
  CREATE UNIQUE INDEX idx_invitations_pending_unique
      ON invitations (tenant_id, lower(email)) WHERE status = 'pending';
  CREATE UNIQUE INDEX idx_invitations_token_hash ON invitations (token_hash);
  ```
- **Mirror**: Data access（`migrations/000002_create_users.up.sql`）
- **Gotcha**: `memberships` 唯一约束是软删除方案的前提——被移除后行仍存在，重新邀请走"重新激活"；`invitations` 的部分唯一索引是幂等最后防线
- **Validate**: `make migrate-up` 后 `\d tenants|memberships|invitations` 符合预期；`make migrate-down` 可精确回滚

### Task 2: 领域实体（tenant / membership / invitation）
- **Action**: 三个领域包，**先写失败测试**
- **Implement**:
  - `tenant`: `Status`（active/suspended/deleted）、`NewTenant(name, slug, createdBy uuid.UUID)`（校验 name 非空、slug 格式 `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`）、`IsActive()`、`Rename(string) bool`、`Suspend()`（供 M5 使用，本里程碑无 API）
  - `membership`: `Status`（active/pending/suspended/removed）、`NewMembership(tenantID, userID)`、`IsActive()`、`Suspend()`、`Activate()`、`Remove()`（软删除，幂等）、`Rejoin(now)`（重新激活并刷新 `joined_at`）
  - `invitation`: `Status`（pending/accepted/cancelled/expired）、`NewInvitation(tenantID, email, invitedBy, ttl) (*Invitation, string, error)`（生成 32 字节随机 token 返回明文，实体只存 sha256）、`IsPending(now)`、`IsExpired(now)`、`Matches(token)`、`Accept(userID, now) error`、`Cancel() error`、`Refresh(now, ttl)`
- **Mirror**: Naming（`internal/domain/user/user.go:16,29,45,80`）
- **Gotcha**: `email` 统一 `lower(trim)` 存储；`Matches` 用 `subtle.ConstantTimeCompare` 避免时序侧信道
- **Validate**: `go test ./internal/domain/...` 全绿

### Task 3: 扩展可信声明（邮箱、验证状态、平台角色）
- **Action**: 为"先认证后绑定"与平台管理员判定提供**唯一**数据依据
- **Implement**:
  - `ports.Claims` 增加 `Email string`、`EmailVerified bool`、`Roles []string`
  - `zitadel.verifier` 的 `tokenClaims` 增加 `email`、`email_verified`，并解析 `urn:zitadel:iam:user:roles`（取值形态通常为 `{"<role>":{"<orgDomain>":"<orgID>"}}`，只需取 key 集合）
  - `identity.Principal` 增加 `Email`、`EmailVerified`、`Roles`，由 `AuthN` 设置
  - `oidctest.Stub` 的 `Spec` 增加 `Email`、`EmailVerified`、`Roles`
- **Mirror**: Errors（`internal/ports/identity.go:12`）
- **Gotcha**:
  - `email_verified` 缺失或为 false 一律视为未验证（fail closed）
  - **roles claim 结构必须用真实 ZITADEL 令牌确认一次**：需确认 ZITADEL 是否已启用"在令牌中断言角色"；解析失败时降级为空角色集合并记 WARN，**不得**让解析错误变成 500，也不得降级为放行
  - 角色判定只做 key 匹配；若将来需要限定组织维度，再扩展 `Policy`，本里程碑不做
- **Validate**: `go test ./internal/adapters/zitadel/... ./internal/domain/...` 全绿

### Task 4: 平台管理员授权路径（仅 ZITADEL 角色）
- **Action**: `internal/application/platformadmin/policy.go` + `middleware/platform_admin.go`
- **Implement**:
  - `Policy{RoleKey string}`，`NewPolicy(cfg)` 校验 `RoleKey` 非空（配置 `PLATFORM_ADMIN_ROLE_KEY`，默认 `platform_admin`）
  - `IsPlatformAdmin(p identity.Principal) bool`：`slices.Contains(p.Roles, p.RoleKey)`；**不提供**任何白名单或环境变量通道
  - 中间件 `RequirePlatformAdmin(policy *Policy)`：非管理员 → `403 FORBIDDEN` + slog WARN（含 subject、request_id），必须 `c.Abort()`
  - 配置层：`PLATFORM_ADMIN_ROLE_KEY` 必填（有默认值，显式置空则启动失败）
  - 启动期 slog INFO 输出当前生效的平台管理员角色 key，便于排障
- **Mirror**: `internal/transport/http/middleware/authn.go:22,46,93`
- **Gotcha**:
  - 平台管理员判定**只基于已验证令牌的 roles**，绝不接受请求头或数据库字段（无白名单、无本地提权入口）
  - 不得把平台权限与租户 RBAC 混用，不得用通配租户或空租户 ID 隐式提权（TDD §7.6）
  - 若 ZITADEL 未下发 roles claim，结果就是**无人可创建租户**——这是预期的 fail closed 行为，需通过真实令牌验证并在 README 写明前置条件
- **Validate**: `go test ./internal/application/platformadmin/... ./internal/transport/http/middleware/...` 覆盖：角色命中、角色未命中、无 roles claim、角色 key 配置缺失时启动失败

### Task 5: 事务能力与仓储实现
- **Action**: `internal/ports/tx.go`、`internal/adapters/postgres/tx.go` + 三个仓储
- **Implement**:
  - `Transactor.WithinTx(ctx, fn)`：adapter 内 `db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return fn(withTx(ctx, tx)) })`；所有仓储统一通过 `dbFrom(ctx, r.db)` 取句柄
  - `TenantRepository`: `Create`、`FindByID`、`FindBySlug`、`ListByMemberUserID`（join memberships，带 user_id 条件）、`CountByCreator`、`Update`
  - `MembershipRepository`: `Create`、`FindByTenantAndUser`、`ListByTenant`（limit/offset + status 过滤）、`CountActiveByTenant`、`Update`
  - `InvitationRepository`: `Create`、`FindByID`、`FindPendingByTenantAndEmail`、`ListByTenant`、`ListPendingByEmail`、`Update`
- **Mirror**: Data access（`internal/adapters/postgres/user_repository.go:29,45,81`）
- **Gotcha**: 新增仓储方法必须走 `dbFrom(ctx)`，否则事务形同虚设；未命中返回 `(nil, nil)`；邮箱查询用 `lower(email)` 以命中部分唯一索引
- **Validate**: 集成测试覆盖唯一约束、跨租户查询隔离与事务回滚

### Task 6: 平台管理员创建租户
- **Action**: `internal/application/tenant/create.go`
- **Implement**: `CreateService.Create(ctx, actor *user.User, input{Name, Slug, OwnerEmail}) (*Result, error)`：
  1. `FindBySlug` 命中 → `ErrTenantSlugConflict`
  2. `tenant.NewTenant(...)` 构造并校验
  3. **同一事务内**：创建租户（created_by = actor.ID）→ 为目标邮箱创建 pending 邀请（ttl 来自配置）
  4. 返回租户与邀请信息（含明文 token，供邮件系统使用）
  5. slog INFO 记录平台管理操作（actor、tenant_id、owner_email、结果）
- **Mirror**: Errors（`internal/application/user/sync.go:17`）
- **Gotcha**: 平台管理员**不**写入 memberships（职责分离）；若 owner 已是 active 成员则 `ErrMemberAlreadyExists`；邀请创建复用 invitation 用例以保证幂等语义一致
- **Validate**: `go test ./internal/application/tenant/...` 覆盖创建成功、slug 冲突、事务回滚（邀请失败时租户不落库）

### Task 7: 成员管理用例
- **Action**: `internal/application/membership/service.go`
- **Implement**: `ListMembers`、`GetMember`、`UpdateStatus`（仅 `active`/`suspended`）、`RemoveMember`：
  - `CountActiveByTenant` == 1 且目标为 active → `ErrCannotRemoveLastMember`
  - 移除 = `membership.Remove()` + `Update`（软删除，幂等）
- **Mirror**: Errors（`internal/application/user/sync.go:17,54`）
- **Gotcha**: 不允许 PATCH 改回 `removed`（状态机：`active ⇄ suspended` → `removed`）；重新邀请已 removed 成员时走 `Rejoin` 而非插入
- **Validate**: `go test ./internal/application/membership/...` 覆盖列表、停用、移除、最后成员保护、幂等

### Task 8: 邀请用例（先认证、后绑定）
- **Action**: `internal/application/invitation/service.go`
- **Implement**:
  - `Invite(ctx, actor, tenantID, email)`：已是 active 成员 → `ErrMemberAlreadyExists`；存在未过期 pending 邀请 → 刷新有效期复用（幂等）；否则新建
  - `Accept(ctx, principal, invitationID, token)`：
    1. 存在且 pending，否则 `ErrInvitationNotPending`
    2. `IsExpired` → `ErrInvitationExpired`
    3. `Matches(token)` 失败 → `ErrInvalidInvitationToken`
    4. `principal.EmailVerified` 为 false → `ErrEmailNotVerified`
    5. `lower(principal.Email)` ≠ `invitation.Email` → `ErrInvitationEmailMismatch`
    6. 幂等：已 active 则直接返回；否则事务内创建 active membership + 邀请置 accepted
  - `Cancel(ctx, tenantID, invitationID)`：仅 pending 可取消，幂等
- **Mirror**: Errors（`internal/application/user/sync.go:17`）
- **Gotcha**: 校验顺序固定（状态 → 过期 → token → 邮箱验证 → 邮箱匹配），避免向未通过校验者泄漏邀请详情；接受必须事务化
- **Validate**: `go test ./internal/application/invitation/...` 覆盖创建、幂等复用、过期、邮箱不匹配、未验证、token 错误、撤销后接受、重复接受

### Task 9: 租户守卫中间件
- **Action**: `internal/transport/http/middleware/tenant_guard.go`
- **Implement**: `TenantGuard(access TenantAccessChecker)`：
  1. `c.Param("tenantID")` 解析 UUID，非法 → `404 NOT_FOUND`
  2. 租户不存在或调用者非成员 → **404**（防枚举）
  3. 租户 `suspended`/`deleted` → `403 TENANT_SUSPENDED`
  4. membership 非 active → `403 MEMBERSHIP_INACTIVE`
  5. 成功后把 tenantID 写入请求上下文
- **Mirror**: `internal/transport/http/middleware/authn.go:22,46,93`
- **Gotcha**: 只从路径参数取租户标识，不接受请求头（TDD §6.3/§10.3）；必须 `c.Abort()`
- **Validate**: `go test ./internal/transport/http/middleware/...` 覆盖 404/403 分支与成功注入

### Task 10: HTTP 处理器与路由装配
- **Action**: `handlers/platform.go`、`tenant.go`、`member.go`、`invitation.go` + 更新 `router.go`
- **Implement**（路由与语义）:
  ```
  # 平台管理（独立前缀，仅平台管理员）
  POST   /api/v1/platform/tenants                             AuthN + RequirePlatformAdmin

  # 租户（租户成员）
  GET    /api/v1/tenants                                      AuthN              我所属的租户
  GET    /api/v1/tenants/:tenantID                            AuthN + Guard
  PATCH  /api/v1/tenants/:tenantID                            AuthN + Guard      仅修改 name
  GET    /api/v1/tenants/:tenantID/members                    AuthN + Guard      limit/offset
  GET    /api/v1/tenants/:tenantID/members/:userID            AuthN + Guard
  PATCH  /api/v1/tenants/:tenantID/members/:userID            AuthN + Guard      仅 active/suspended
  DELETE /api/v1/tenants/:tenantID/members/:userID            AuthN + Guard      软删除
  POST   /api/v1/tenants/:tenantID/invitations                AuthN + Guard
  GET    /api/v1/tenants/:tenantID/invitations                AuthN + Guard
  DELETE /api/v1/tenants/:tenantID/invitations/:invitationID  AuthN + Guard      撤销

  # 邀请接受（不依赖邮件）
  GET    /api/v1/invitations/pending                          AuthN              给我的邀请
  POST   /api/v1/invitations/:invitationID/accept             AuthN
  ```
  错误码：`FORBIDDEN`、`TENANT_SLUG_CONFLICT`、`MEMBER_ALREADY_EXISTS`、`CANNOT_REMOVE_LAST_MEMBER`、`INVITATION_EXPIRED`、`INVITATION_EMAIL_MISMATCH`、`EMAIL_NOT_VERIFIED`、`TENANT_SUSPENDED`、`MEMBERSHIP_INACTIVE`
- **Mirror**: Transport（`internal/transport/http/router.go:28-29`）、`handlers/me.go`
- **Gotcha**: 平台路由与租户路由分属不同 Group，避免平台权限被租户路由误用；`GET /invitations/pending` 按已验证邮箱过滤，未验证返回空列表
- **Validate**: 集成测试逐条验证状态码

### Task 11: 集成与安全回归测试
- **Action**: `tests/integration/tenancy_test.go`、`tests/integration/invitation_test.go`
- **Implement**（复用 `newIdentityStack` 模式并扩展为含租户依赖的装配助手）：
  - **平台管理员**：带 `platform_admin` 角色的令牌创建租户成功；**普通用户令牌（无该角色）→ 403**；未认证 → 401
  - 创建租户后产生 pending 邀请，**平台管理员不在成员列表中**
  - slug 冲突 → 409
  - 租户隔离：A 成员读 B 成员 → 404；篡改路径 tenantID → 404；租户 suspended（测试置库）→ 403；成员 removed → 404/403
  - 邀请端到端：owner 以匹配且已验证邮箱接受 → 200 且成员 active；随后可见该租户
  - 安全回归：邮箱不匹配 → 403；`email_verified=false` → 403；过期 → 409；撤销后接受 → 409；重复接受 → 200 幂等；重复邀请 → 同一邀请
  - 事务：邀请创建失败时租户不被创建
- **Mirror**: Tests（`tests/integration/identity_test.go:32`）
- **Gotcha**: OIDC 桩需签发带 `email`/`email_verified`/`urn:zitadel:iam:user:roles` 的令牌；接受邀请与发起邀请用**不同 subject**；管理员用例通过给 `Spec.Roles` 注入 `platform_admin` 构造
- **Validate**: `go test ./tests/integration/... -count=1` 全绿

### Task 12: 配置、装配与文档
- **Action**: `config` 新增 `PLATFORM_ADMIN_ROLE_KEY`（默认 `platform_admin`，置空则启动失败）、`INVITATION_TTL`（默认 168h = 7 天）；`bootstrap/tenancy.go` 装配；更新 `.env.example` 与 README
- **Implement**: 沿用 `internal/config/config.go:84,167` 的聚合校验；`bootstrap.New` 注入 Transactor、三个仓储、三个用例与平台管理员策略；README 写明：
  - 平台管理员**只能**在 ZITADEL 侧通过授予 `platform_admin` 角色获得，服务端无白名单与应急通道
  - 前置条件：ZITADEL 必须在访问令牌中断言角色，否则无人可创建租户
  - 邀请 token 当前由响应返回，生产需改为邮件投递
- **Mirror**: Bootstrap（`internal/bootstrap/auth.go:18`、`internal/bootstrap/app.go:33`）
- **Gotcha**: `INVITATION_TTL` 必须为正；`PLATFORM_ADMIN_ROLE_KEY` 为空必须启动失败
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
go test ./tests/integration/... -count=1
# EXPECT: PASS

# 数据库
make migrate-up
docker compose exec postgres psql -U postgres -d iam -c '\d memberships'
# EXPECT: UNIQUE (tenant_id, user_id) + status CHECK
docker compose exec postgres psql -U postgres -d iam -c '\di idx_invitations_pending_unique'
# EXPECT: 部分唯一索引存在

# 冒烟（需真实 ZITADEL 令牌）
curl -H "Authorization: Bearer $ADMIN_TOKEN" -X POST localhost:8080/api/v1/platform/tenants \
  -d '{"name":"Acme","slug":"acme","owner_email":"owner@example.com"}'
# EXPECT: 创建成功且返回 invitation（含 token）
curl -H "Authorization: Bearer $USER_TOKEN" -X POST localhost:8080/api/v1/platform/tenants -d '...'
# EXPECT: 403 FORBIDDEN

# 平台管理员判定（真实令牌必查项）
# 解码访问令牌，确认存在 urn:zitadel:iam:user:roles 且包含 platform_admin
```

## Risks

| Risk | Likelihood | Mitigation |
| --- | --- | --- |
| **ZITADEL 未配置"在令牌中断言角色"，导致无人可创建租户** | 中 | 实施第一步用真实令牌确认 claim 存在；README 写明前置条件；启动日志输出生效的角色 key；fail closed（宁可无人可创建，也不隐式放权） |
| roles claim 结构与预期不符（`{role:{org:orgId}}` 之外的形态） | 中 | 解析为 `map[string]json.RawMessage` 取 key 集合，兼容多种取值；解析失败降级为空角色并 WARN，不产生 500 |
| 无白名单/应急通道，ZITADEL 侧角色配置丢失时租户创建完全不可用 | 中 | 明确接受该取舍（用户要求）；M5 审计与运维手册需覆盖"如何恢复平台管理员"的操作步骤 |
| 平台管理员角色命名冲突（与业务角色同名） | 低 | 角色 key 可配置（`PLATFORM_ADMIN_ROLE_KEY`）；平台角色与租户 RBAC 走完全独立路径，不共享 Casbin 策略 |
| 令牌缺少 `email`/`email_verified`，owner 无法接受邀请 | 中 | 视为未验证并拒绝（fail closed）；需回到 PRD 决策改用其他绑定依据 |
| 事务未真正生效，产生孤儿租户或半成品成员 | 中 | Task 5 要求所有仓储走 `dbFrom(ctx)`；Task 11 用失败注入验证回滚 |
| 邮箱大小写造成重复邀请或唯一索引失效 | 中 | 统一 `lower(trim)` 存储与查询 |
| 成员软删除后无法重新邀请 | 中 | 重新邀请命中 `removed` 时走 `Rejoin` 重新激活 |
| 平台管理操作暂无审计表 | 中 | 本里程碑用 slog 记录；M5 落 audit_logs 表 |

## Acceptance

- [ ] `gofmt -l .` 无输出、`go vet ./...` 无错误、`go build ./...` 通过
- [ ] `go test -short ./...` 全绿；`go test ./tests/integration/...` 全绿
- [ ] **仅持有 ZITADEL `platform_admin` 角色的身份可创建租户**；同一已认证但无该角色的用户返回 403；未认证返回 401
- [ ] 平台管理员判定不存在任何白名单或环境变量通道；不接受请求头传入的角色信息
- [ ] 平台管理员创建租户时自动为 `owner_email` 创建 pending 邀请，且本人不成为成员
- [ ] 创建租户与创建邀请在同一事务
- [ ] 所有租户级接口强制执行成员资格与租户状态校验；跨租户访问返回 404
- [ ] 邀请遵循"先认证、后绑定"：邮箱未验证或不匹配一律拒绝
- [ ] 邀请 7 天有效期；重复邀请与重复接受幂等；撤销与过期被正确拒绝
- [ ] 成员移除为软删除，且禁止移除最后一个 active 成员
- [ ] 新增表均通过版本化迁移创建，约束落在数据库层
- [ ] 未越界实现 NOT Building 中的任何一项
- [ ] Patterns mirrored, not reinvented：沿用既有命名、错误、数据访问与测试约定

---

**WAITING FOR CONFIRMATION**：是否按此计划实施？可回复 `yes` / `modify: <改动>` / `different approach: <方案>`。
