# Plan: IAM 服务身份接入（PRD Milestone 2）

**Source PRD**: `.claude/prds/iam-service.prd.md`
**Selected Milestone**: 2 — 身份接入
**Complexity**: Large

## Summary

在 Milestone 1 的可运行骨架之上接入 ZITADEL 身份：新增 `users` 表与迁移，定义 `Principal` 与用户领域实体，实现 Token 验证端口与 ZITADEL OIDC/JWKS 适配器，实现认证中间件与本地用户按需同步，暴露第一个受保护端点 `GET /api/v1/me`。

验收口径（PRD Milestone 2）：持有有效 ZITADEL 凭证的请求可访问受保护接口；伪造、过期、受众错误、签发方错误的凭证被拒绝。

## 前置依赖（来自 Milestone 1，必须先落地）

本计划**不重复**实现骨架，但直接依赖以下已规划的产物。若尚未实现，请先执行 `.claude/PRPs/plans/iam-service-bootstrap.plan.md`：

| 依赖 | 来源 | 本计划将如何使用 |
| --- | --- | --- |
| `config.Load() (Config, error)` | M1 `internal/config` | 扩展 ZITADEL/AUTH 字段与校验 |
| `responses.OK(c, data)` / `responses.Fail(c, status, code, msg)` / `responses.RequestIDContextKey` | M1 `internal/transport/http/responses` | 所有成功与失败响应 |
| `middleware.RequestID()` / `middleware.Logging()` | M1 `internal/transport/http/middleware` | 中间件链前置 |
| `router.NewRouter(app *bootstrap.App) *gin.Engine` | M1 `internal/transport/http` | 挂载认证中间件与 `/api/v1/me` |
| `bootstrap.App{DB *gorm.DB, Enforcer, Check(ctx)}` | M1 `internal/bootstrap` | 注入 UserRepository 与 TokenVerifier |
| `cmd/migrate` + `migrations/embed.go` | M1 | 新增 `000002_create_users` |
| 集成测试用 testcontainers PostgreSQL | M1 `tests/integration` | 复用同一套跳过/复用策略 |

---

## Patterns to Mirror

> **代码库现状说明**：仓库当前**仍无任何 Go 代码**（Milestone 1 尚未实施）。因此下表不虚构既有代码，模式来源为 TDD 的强制约定 + Milestone 1 计划确立的约定。

| Category | Source | Pattern |
| --- | --- | --- |
| Naming | `docs/TDD.md:355-409` | 分层目录 `internal/{domain,application,ports,adapters,transport,bootstrap}`；领域分包 user / tenant / role… |
| Naming | `docs/TDD.md:473-480` | `Principal{Subject, UserID, ClientID, Authenticated}` 字段名逐字沿用 |
| Errors | `docs/TDD.md:787-797` | 状态码语义：401 无凭证/无效凭证；403 身份有效但权限不足；503 依赖不可用 |
| Errors | `docs/TDD.md:605-613` | 区分"明确拒绝（false, nil）"与"决策无法完成（err != nil）"；依赖故障必须 **fail closed** |
| Logging | M1 计划 LOGGING_PATTERN | `log/slog`，production JSON；禁止记录完整 Token 与凭据 |
| Data access | `docs/TDD.md:650-663` | 数据库级唯一/非空约束由迁移保证，不靠应用层兜底；GORM 只做访问 |
| Tests | `docs/TDD.md:856-879` | 单元（mock ports）+ 集成（真实 PostgreSQL）+ 安全回归（跨租户/伪造身份）三层 |
| Tests | M1 计划 TEST_STRUCTURE | 表驱动 + testify；集成测试 `testing.Short()` + `SkipIfProviderIsNotHealthy` 双跳过 |

---

## 本计划锁定的决策

| 决策 | 选择 | 理由 | 可逆性 |
| --- | --- | --- | --- |
| Token 验证方式 | **MVP 实现 JWT 本地验签**（`AUTH_TOKEN_MODE=jwt`），不透明 Token 自省**延后** | TDD §13.2 环境变量示例即 `AUTH_TOKEN_MODE=jwt`；端口抽象保证后续替换 adapter 不改应用层 | 中（见 Risks） |
| 验证库 | `github.com/coreos/go-oidc/v3`（OIDC Discovery + JWKS 缓存/轮换）+ 自补 `nbf`/`sub` 校验 | 官方库处理 discovery、kid 轮换、alg 白名单；比手写 JWKS 少出错 | 中 |
| 测试签名库 | `github.com/golang-jwt/jwt/v5`（仅测试用签发）+ 标准库手搓 JWKS JSON | 避免为测试引入第二个重量级依赖 | 高 |
| 用户同步来源 | 用**已验证 Token 的 claims**（`sub` 为主键，`name`/`nickname` 等仅作展示副本）建立本地映射；MVP **不**调用 ZITADEL 管理 API | TDD §6.4 轻量同步；避免引入服务凭据与 zitadel-go 重依赖 | 高 |
| 用户同步触发 | 认证中间件内按需创建/更新 | TDD §6.4 "第一次访问时按需" | 中 |
| 禁用用户 | `403 USER_DISABLED` | 身份有效但不允许访问；与 401（凭证问题）区分 | 高 |
| 认证开关 | **不提供** `AUTH_ENABLED=false` 之类绕过开关 | 防止本地/测试配置削弱生产安全性；测试用 fake verifier 而非开关 | 高 |

---

## Files to Change

| File | Action | Why |
| --- | --- | --- |
| `migrations/000002_create_users.up.sql` | CREATE | `users` 表：业务用户映射 |
| `migrations/000002_create_users.down.sql` | CREATE | 可逆 |
| `internal/domain/user/user.go` | CREATE | 用户实体与不变量（不依赖 gorm/gin） |
| `internal/domain/user/user_test.go` | CREATE | 领域单测 |
| `internal/domain/identity/principal.go` | CREATE | `Principal` + context 存取 |
| `internal/domain/identity/principal_test.go` | CREATE | 单测 |
| `internal/ports/identity.go` | CREATE | `TokenVerifier` 接口与哨兵错误 |
| `internal/ports/repositories.go` | CREATE | `UserRepository` 接口 |
| `internal/application/user/sync.go` | CREATE | 按需同步用例 |
| `internal/application/user/sync_test.go` | CREATE | mock 端口单测 |
| `internal/adapters/zitadel/verifier.go` | CREATE | OIDC Discovery + JWKS 验证 |
| `internal/adapters/zitadel/verifier_test.go` | CREATE | httptest OIDC 桩 + 各类无效 Token |
| `internal/adapters/postgres/user_repository.go` | CREATE | GORM 实现 |
| `internal/transport/http/middleware/authn.go` | CREATE | 认证中间件（401/403/503 语义） |
| `internal/transport/http/middleware/authn_test.go` | CREATE | 中间件分支单测 |
| `internal/transport/http/handlers/me.go` | CREATE | `GET /api/v1/me` |
| `internal/transport/http/handlers/me_test.go` | CREATE | 单测 |
| `internal/config/config.go` | UPDATE | 新增 ZITADEL_* / AUTH_* 字段与校验 |
| `internal/config/config_test.go` | UPDATE | 新增配置用例 |
| `internal/bootstrap/auth.go` | CREATE | 构造 TokenVerifier 与 UserRepository |
| `internal/bootstrap/app.go` | UPDATE | 注入认证依赖 |
| `internal/transport/http/router.go` | UPDATE | 挂载中间件与 `/api/v1/me` |
| `tests/integration/identity_test.go` | CREATE | 真实 PostgreSQL + 桩 OIDC 端到端 |
| `.env.example` / `README.md` / `docker-compose.yml` | UPDATE | 新环境变量与冒烟说明 |

## NOT Building

- **不透明 Token 自省（Introspection）adapter** —— 待确认实际 Token 类型；端口已预留，后续加一个 adapter 即可，不改应用层
- **ZITADEL 管理 API 调用**（用户资料拉取、用户禁用事件处理）—— 需要 `ZITADEL_SERVICE_CREDENTIALS_FILE` 与服务身份，延后
- **用户事件订阅/撤权**（禁用、删除、身份失效的撤权策略）—— Milestone 4/6
- **`/me/tenants`、`/me/permissions`** —— Milestone 3/4（本里程碑只做最小 `/me`）
- **平台管理员授权路径** —— Milestone 4；本里程碑不引入任何"超级管理员"隐式逻辑
- **租户、成员、角色、权限相关表与 API** —— Milestone 3/4
- **Redis 缓存 Token 验证结果** —— 可选组件，Phase 6
- **Vue 前端** —— PRD 明确不在范围

---

## Tasks

### Task 1: `users` 表迁移
- **Action**: 新增 `000002_create_users` up/down
- **Implement**:
  ```sql
  CREATE TABLE users (
      id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
      zitadel_user_id VARCHAR(255) NOT NULL,
      display_name    VARCHAR(255) NOT NULL DEFAULT '',
      avatar_url      TEXT         NOT NULL DEFAULT '',
      status          VARCHAR(32)  NOT NULL DEFAULT 'active'
                      CHECK (status IN ('active','disabled')),
      created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
      updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
  );
  CREATE UNIQUE INDEX idx_users_zitadel_user_id ON users (zitadel_user_id);
  ```
  down：`DROP INDEX IF EXISTS idx_users_zitadel_user_id; DROP TABLE IF EXISTS users;`
- **Mirror**: Data access（`docs/TDD.md:650-663`：约束落在数据库）
- **Gotcha**: `gen_random_uuid()` 需 PostgreSQL 13+（compose 用 16）；`zitadel_user_id` 唯一索引必须显式命名，便于 down 精确回滚
- **Validate**: `make migrate-up` → `psql -c '\d users'` 列与唯一索引符合预期；`make migrate-down` 后表消失

### Task 2: Domain —— 用户实体与 Principal
- **Action**: 建 `internal/domain/user` 与 `internal/domain/identity`，**先写失败测试**
- **Implement**:
  - `user.User{ID uuid.UUID; ZitadelUserID string; DisplayName string; AvatarURL string; Status Status; CreatedAt, UpdatedAt time.Time}`
  - `type Status string`：`StatusActive` / `StatusDisabled`；`NewUser(zitadelUserID string) (*User, error)` 校验 `zitadelUserID` 非空；`IsActive() bool`；`Disable()`；`Rename(displayName string)`（空串不覆盖）
  - `identity.Principal` 严格按 `docs/TDD.md:473-480`：`{Subject, UserID, ClientID string; Authenticated bool}`
  - `identity.WithPrincipal(ctx, Principal) context.Context` / `identity.FromContext(ctx) (Principal, bool)`；提供 `principalCtxKey` 私有类型避免冲突
- **Mirror**: Naming（`docs/TDD.md:473-480`）、分层（Domain 不得 import gin/gorm/casbin）
- **Gotcha**: `Authenticated` 只允许中间件设置，不提供任何从请求参数构造 Principal 的入口（TDD §6.3）
- **Validate**: `go test ./internal/domain/...` 全绿

### Task 3: Ports —— TokenVerifier 与 UserRepository
- **Action**: 定义依赖倒置接口与哨兵错误
- **Implement**:
  ```go
  // internal/ports/identity.go
  var ErrInvalidToken = errors.New("invalid access token")
  var ErrIdentityProviderUnavailable = errors.New("identity provider unavailable")

  type Claims struct {
      Subject, ClientID, DisplayName, AvatarURL string
  }
  type TokenVerifier interface {
      Verify(ctx context.Context, rawToken string) (*Claims, error)
  }

  // internal/ports/repositories.go
  type UserRepository interface {
      FindByZitadelUserID(ctx context.Context, zitadelUserID string) (*user.User, error)
      Create(ctx context.Context, u *user.User) error
      Update(ctx context.Context, u *user.User) error
  }
  ```
  文档注释明确：`Verify` 返回 `ErrInvalidToken` → 401；返回 `ErrIdentityProviderUnavailable` → 503（fail closed）
- **Mirror**: Errors（`docs/TDD.md:605-613`：区分拒绝与无法决策）
- **Gotcha**: 哨兵错误定义在 `ports`，adapter 用 `fmt.Errorf("...: %w", ports.ErrInvalidToken)` 包裹，中间件用 `errors.Is` 判定
- **Validate**: `go build ./...` 通过；后续 Task 单测覆盖两类错误分支

### Task 4: Application —— 用户按需同步
- **Action**: `internal/application/user/sync.go`，mock 端口单测先写
- **Implement**:
  ```go
  type SyncService struct { repo ports.UserRepository; now func() time.Time }
  func (s *SyncService) Ensure(ctx context.Context, c *ports.Claims) (*user.User, error)
  ```
  1. `repo.FindByZitadelUserID(ctx, c.Subject)`
  2. 命中 → 若 `u.Status != active` 返回 `ErrUserDisabled`；若 `c.DisplayName` 非空且变化 → `Rename` + `Update`（**仅在变化时写库**，避免每请求写放大）
  3. 未命中 → `user.NewUser(c.Subject)` + 填充展示资料 + `Create`
  4. `repo` 错误原样上抛（中间件映射为 503）
- **Mirror**: 分层（Application 只依赖 ports，不 import gorm/casbin）、TDD §6.4
- **Gotcha**:
  - 主键是 **`sub`（ZITADEL 用户 ID）**，禁止用邮箱做关联键（TDD §6.4）
  - 展示资料只能来自**已验证 Token 的 claims**；不信任任何请求头/请求体字段
  - 不要在这里做任何租户/角色逻辑（Milestone 3/4）
- **Validate**: `go test ./internal/application/...` 覆盖：首次创建 / 命中不重建 / 资料变化才更新 / 禁用返回 `ErrUserDisabled` / repo 错误上抛

### Task 5: Adapter —— ZITADEL JWT 验证器
- **Action**: `internal/adapters/zitadel/verifier.go` + httptest OIDC 桩测试
- **Implement**:
  - `NewTokenVerifier(ctx, cfg) (*Verifier, error)`：`oidc.NewProvider(ctx, cfg.ZitadelIssuer)` → `provider.Verifier(&oidc.Config{ClientID: cfg.APIAudience, SupportedSigningAlgs: []string{"RS256","RS384","RS512","ES256","ES384","ES512","EdDSA"}})`
  - `Verify`：调 `oidcVerifier.Verify(ctx, raw)` 得 `*oidc.IDToken`；自补校验：`tok.Subject` 非空、若存在 `nbf` 则 `now >= nbf`（含 60s 时钟偏移容忍）；映射为 `ports.Claims`（`ClientID` 仅在 `aud` 校验通过后从 claim 提取）
  - 错误映射：签名/iss/aud/exp 失败 → `ErrInvalidToken`；discovery 或 JWKS 不可达 → `ErrIdentityProviderUnavailable`
  - 测试桩：`httptest.Server` 提供 `/.well-known/openid-configuration`（含 `issuer`、`jwks_uri`、以及 go-oidc 解析所需的端点字段）与 `/jwks`（用 `golang-jwt/jwt/v5` 签发的 RSA 公钥手搓 JWKS JSON）
- **Mirror**: Tests（M1 TEST_STRUCTURE：表驱动 + testify）
- **Gotcha**:
  - go-oidc 会**强校验 discovery 返回的 `issuer` 必须与构造时的 issuer 完全一致**，桩里写错会报 "oidc: issuer did not match"
  - go-oidc 的 `ClientID` 即期望 `aud`；若 ZITADEL 访问令牌的 `aud` 是列表，需确认包含 `ZITADEL_API_AUDIENCE`；若两者语义不同，改用 `SkipClientIDCheck: true` 并自行校验 `aud` —— **实施时必须用真实 Token 确认一次**
  - `AUTH_JWKS_CACHE_TTL` 解析但**当前不生效**：go-oidc 的 remote key set 不暴露 TTL 配置（自带缓存与 kid 轮换）。README 需注明；如需精确控制再切 `lestrrat-go/jwx/v3` 的 `jwk.Cache`
  - 禁止开启 `InsecureSkipSignatureCheck`；`SupportedSigningAlgs` 必须显式白名单（杜绝 `alg: none` / HS256 混淆）
  - 不要缓存"验证结果"，只缓存密钥
- **Validate**: `go test ./internal/adapters/zitadel/...` 覆盖：有效 / 签发方不符 / 受众不符 / 已过期 / `alg=none` 伪造 / kid 未知触发轮换 / JWKS 端点 500 且无缓存 → `ErrIdentityProviderUnavailable`

### Task 6: Adapter —— PostgreSQL UserRepository
- **Action**: `internal/adapters/postgres/user_repository.go`（GORM）+ 集成测试
- **Implement**: `UserRepository{db *gorm.DB}`；`FindByZitadelUserID` 用 `Where("zitadel_user_id = ?", id).Take(&m)`，`gorm.ErrRecordNotFound` → 返回 `(nil, nil)`（未命中不是错误）；`Create`/`Update` 直接落库；`Update` 显式设置 `updated_at`
- **Mirror**: Data access（`docs/TDD.md:650-663`）
- **Gotcha**: 未命中必须返回 `(nil, nil)` 而非 error，否则同步用例会把"首次登录"当故障；并发下重复创建依赖数据库唯一索引兜底，错误需能被上层识别为冲突（M1 已启用 `TranslateError` 时可用 `gorm.ErrDuplicatedKey`）
- **Validate**: 集成测试：创建 → 按 `zitadel_user_id` 查询命中；重复插入同 `zitadel_user_id` → 唯一约束报错；`status` 非法值被 CHECK 拒绝

### Task 7: 认证中间件
- **Action**: `internal/transport/http/middleware/authn.go`
- **Implement**:
  - 取 `Authorization: Bearer <token>`；缺失或格式错误 → `responses.Fail(c, 401, "UNAUTHENTICATED", "missing or invalid credentials")`
  - `verifier.Verify`：`errors.Is(err, ports.ErrInvalidToken)` → 401；`ErrIdentityProviderUnavailable` → 503 `DEPENDENCY_UNAVAILABLE`；其他 → 500
  - `sync.Ensure`：domain `ErrUserDisabled` → 403 `USER_DISABLED`；其他错误 → 503
  - 成功后构造 `identity.Principal{Subject, UserID: u.ID.String(), ClientID, Authenticated: true}`，写入 **`c.Request.Context()`**（`c.Request = c.Request.WithContext(...)`），`c.Next()`
  - 用 `slog.WarnContext` 记录失败原因，**不记录 Token 本身**
- **Mirror**: Errors（`docs/TDD.md:787-797`）、Logging（M1）
- **Gotcha**:
  - 中间件顺序必须是 `RequestID → Logging → AuthN`，否则日志与错误响应拿不到 `request_id`
  - 失败分支必须 `c.Abort()`
  - 身份只有这一个来源：忽略任何 `X-User-ID` 之类的请求头（TDD §10.3）
- **Validate**: `go test ./internal/transport/http/middleware/...` 覆盖 401/403/503/成功注入 Principal 四条路径，且失败响应体 `request_id` 与响应头一致

### Task 8: 受保护端点与路由装配
- **Action**: `handlers/me.go` + 更新 `router.go`
- **Implement**:
  - `GET /api/v1/me`：`Principal` 缺失 → 401（防御性）；命中 → `responses.OK(c, MeResponse{UserID, ZitadelUserID, DisplayName})`
  - `router.go`：`v1 := r.Group("/api/v1")`；`v1.Use(middleware.AuthN(verifier, syncSvc))`；`v1.GET("/me", ...)`；`/health/*` 保持**无认证**
- **Mirror**: RESPONSE_PATTERN（M1）
- **Gotcha**: 健康检查必须留在认证之外，否则容器探针会 401；`/api/v1` 分组只加认证，不放权限逻辑（Casbin 在 Milestone 4 接入）
- **Validate**: `curl` 无 Token → 401；带桩签发的有效 Token → 200

### Task 9: 配置扩展与启动校验
- **Action**: 更新 `internal/config`
- **Implement**: 新增 `ZITADEL_ISSUER`（必填，必须是 https URL）、`ZITADEL_API_AUDIENCE`（必填）、`ZITADEL_CLIENT_ID`（可选）、`ZITADEL_SERVICE_CREDENTIALS_FILE`（可选，本里程碑不使用）、`AUTH_TOKEN_MODE`（`jwt`，默认；`introspect` 暂不支持 → 启动时明确报错提示未实现）、`AUTH_JWKS_CACHE_TTL`（默认 15m，解析但实际不生效，见 Task 5）
- **Mirror**: CONFIG_PATTERN（M1：聚合全部字段错误一次性返回）
- **Gotcha**: `AUTH_TOKEN_MODE=introspect` 必须**显式失败**而不是静默降级为 jwt；issuer 必须校验 scheme 与无尾斜杠导致的 discovery 路径拼接错误（`oidc.NewProvider` 会拼 `${issuer}/.well-known/openid-configuration`）
- **Validate**: `go test ./internal/config/...` 新增用例：缺 issuer/audience → 错误含字段名；`AUTH_TOKEN_MODE=introspect` → 明确错误

### Task 10: bootstrap 依赖注入
- **Action**: `internal/bootstrap/auth.go` + 更新 `app.go`
- **Implement**: `NewTokenVerifier(ctx, cfg)`（构造并**预热** discovery，失败即启动失败）、`NewUserRepository(db)`、`NewUserSync(repo)`；`App` 增加 `Verifier`、`Users`、`UserSync` 字段，`Close()` 不变
- **Mirror**: LAYERING_RULE（bootstrap 是唯一装配层）
- **Gotcha**: discovery 在**启动时**完成并失败退出（TDD §13.2 快速失败），不要延迟到首个请求才发现问题
- **Validate**: 配置缺失时 `go run ./cmd/api` 立即退出码 1 并打印缺失字段

### Task 11: 集成与安全回归测试
- **Action**: `tests/integration/identity_test.go`
- **Implement**（复用 M1 的 testcontainers 策略）：
  1. 端到端有效 Token → `/api/v1/me` 200 且 `users` 表新增一行（`zitadel_user_id == sub`）
  2. 同一 `sub` 二次请求 → 不重复创建（行数仍为 1）
  3. 篡改签名 / 过期 / 错误 audience / 错误 issuer → 401
  4. 请求头携带 `X-User-ID: <他人>` → 身份仍来自 Token（安全回归，TDD §11.3）
  5. 用户置为 `disabled` → 403
  6. JWKS 端点不可达 → **503 而非放行**（fail closed）
- **Mirror**: Tests（`docs/TDD.md:883-895` 核心验收测试）
- **Gotcha**: 集成测试仍需 Docker；本机守护进程当前不可用 → 必须同时加 `testing.Short()` 与 `SkipIfProviderIsNotHealthy`，保证 `go test -short ./...` 不失败
- **Validate**: Docker 可用时 `go test ./tests/integration/... -count=1` 全绿；不可用时不失败

### Task 12: 文档与编排更新
- **Action**: 更新 `.env.example`、`README.md`、`docker-compose.yml`
- **Implement**: 补齐 ZITADEL/AUTH 变量与说明；README 写明：认证无绕过开关、本地冒烟需真实 ZITADEL 或 JWKS 桩、`AUTH_JWKS_CACHE_TTL` 当前不生效、introspect 未实现
- **Gotcha**: compose 中 `ZITADEL_ISSUER` 指向外部/已部署的 ZITADEL，不要把 ZITADEL 塞进本服务的 compose 强制依赖（TDD §13.1）
- **Validate**: `docker compose config` 无语法错误；README 步骤可照做

---

## Validation

```bash
# 静态与构建
gofmt -l .                       # EXPECT: 无输出
go vet ./...                     # EXPECT: 无输出
go build ./...                   # EXPECT: exit 0

# 单元测试（不依赖 Docker）
go test -short -race ./...       # EXPECT: 全部 PASS

# 集成与安全回归（需 Docker）
go test ./tests/integration/... -count=1 -v
# EXPECT: PASS；Docker 不可用时必须 SKIP 而非 FAIL

# 数据库
make migrate-up
docker compose exec postgres psql -U postgres -d iam -c '\d users'
# EXPECT: 7 列 + idx_users_zitadel_user_id 唯一索引 + status CHECK

# 手动冒烟（需真实 ZITADEL 或 OIDC 桩）
curl -i http://localhost:8080/api/v1/me
# EXPECT: 401 UNAUTHENTICATED（无 Token）
curl -i -H "Authorization: Bearer $VALID_TOKEN" http://localhost:8080/api/v1/me
# EXPECT: 200 + data.user_id
curl -i http://localhost:8080/health/ready
# EXPECT: 200（健康检查不要求认证）
```

## Risks

| Risk | Likelihood | Mitigation |
| --- | --- | --- |
| **实际 Token 是不透明的，JWT 验签方案返工**（PRD Open Question #1 未决） | 中 | 端口抽象已隔离：新增 introspect adapter 即可，应用层/中间件零改动；**建议在实施前用真实客户端确认 Token 类型**；`AUTH_TOKEN_MODE=introspect` 显式报错而非静默降级 |
| 真实 Token 的 `aud` 与 `ZITADEL_API_AUDIENCE` 语义不一致，导致全部请求 401 | 中 | 实施第一步就用一个真实 Token 打印 claims 确认；必要时 `SkipClientIDCheck` + 自行校验 `aud` 列表 |
| go-oidc 的 `iat`/`exp`/`nbf` 校验行为与 TDD §6.2 要求不完全一致 | 中 | 在 `Verify` 内自补 `sub` 非空与 `nbf` 校验；把差异写进测试而非假设 |
| Milestone 1 骨架未落地，本计划依赖的文件不存在 | 中 | 前置依赖表已列明；先执行 M1 计划 |
| Docker 守护进程不可用，集成/安全回归无法本地验证 | 高（本机已确认） | 双跳过策略；冒烟列为手动步骤，需在 Docker 启动后补跑 |
| 认证中间件被绕过（如误把 `/api/v1/me` 挂在认证之外） | 低 | 路由以 Group 为单位统一加中间件；集成测试含"无 Token 必须 401"用例 |
| JWKS 缓存导致密钥轮换后短暂失败 | 低 | go-oidc 内置 kid 轮换；测试覆盖"未知 kid 触发重新拉取" |

## Acceptance

- [ ] `gofmt -l .` 无输出、`go vet ./...` 无错误、`go test -short ./...` 全绿
- [ ] Docker 可用时集成与安全回归测试全绿；不可用时跳过而非失败
- [ ] 有效 Token → `/api/v1/me` 200；无 Token / 伪造签名 / 过期 / 错误 issuer / 错误 audience → 401
- [ ] 禁用用户 → 403 `USER_DISABLED`；JWKS 不可达 → **503**（fail closed，绝不放行）
- [ ] 请求头伪造身份无效，身份唯一来源是 Token 的 `sub`
- [ ] `users` 表由迁移创建，`zitadel_user_id` 唯一约束在数据库层
- [ ] 健康检查仍无需认证
- [ ] 未提供任何认证绕过开关
- [ ] 未越界实现 NOT Building 中的任何一项（尤其：不建租户/角色表、不加 Casbin 判定）
- [ ] Patterns mirrored, not reinvented：Domain 层不 import gin/gorm/casbin；错误语义符合 TDD §9.6

---

**WAITING FOR CONFIRMATION**：是否按此计划实施？可回复 `yes` / `modify: <改动>` / `different approach: <方案>`。
