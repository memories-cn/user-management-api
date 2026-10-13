# Plan: IAM 服务可运行骨架（PRD Milestone 1）

## Summary

在空仓库中从零搭建基于 ZITADEL 的 IAM 微服务的可运行骨架：Go 模块与分层目录、环境变量配置与启动校验、PostgreSQL/GORM 连接与连接池、版本化 SQL 迁移机制、Casbin（带 Tenant Domain 的 RBAC 模型）从数据库加载策略、Gin 路由与统一响应/请求 ID、`/health/live` 与 `/health/ready`、优雅关闭、Docker 与 CI。

本里程碑只交付"能跑起来、能被测试证明、依赖缺失时快速失败"的地基，**不含**任何认证、租户、成员、角色业务功能。

## User Story

As a 后端开发者 / 平台运维,
I want 一个能在本地与容器环境中一键启动、健康检查可用、测试可重复执行的 IAM 服务骨架,
So that 后续每个业务里程碑都能在同一套配置、迁移、日志、测试与发布流程上叠加，而不必反复返工基础设施。

## Problem → Solution

**现状**：仓库只有 `docs/TDD.md` 与 PRD，没有任何 Go 代码；数据库无版本化迁移；没有服务可启动、没有健康检查、没有测试与 CI。

**目标**：`make up` 或 `go run ./cmd/api` 能启动服务，`GET /health/live` 返回 200、`GET /health/ready` 真实校验数据库与策略加载状态；`gofmt`/`go vet`/`go test` 全绿；迁移通过独立命令执行且可被集成测试验证。

## Metadata

- **Complexity**: Large
- **Source PRD**: `.claude/prds/iam-service.prd.md`
- **PRD Phase**: Milestone 1 — 可运行的服务骨架
- **Estimated Files**: 22

### 本计划已锁定的决策（如需变更请在此改，再实施）

| 决策 | 选择 | 理由 | 可逆性 |
| --- | --- | --- | --- |
| Go 模块位置 | 工作区根目录（`/private/tmp/userManager`），不新建 `iam-service/` 嵌套层 | 仓库根已有 `docs/` 与 `.claude/`，嵌套会造成两套文档路径 | 低（仅 `go.mod` 一行 + import 前缀） |
| Go module path | `github.com/example/iam-service` | 占位；**若有真实仓库地址，先改 `go.mod` 的 module 行再写代码**，本计划所有 import 均以此为前缀 | 低 |
| 迁移工具 | `github.com/golang-migrate/migrate/v4`（SQL 文件 + `source/iofs` 内嵌，独立 `cmd/migrate` 执行） | TDD §13.3 要求迁移"独立、可审计的步骤执行"；SQL 文件比 Go 代码更可审计 | 中 |
| 配置读取 | 标准库 `os.Getenv` + 显式校验，不引入 viper | TDD §4 只要求"环境变量 + 配置校验"，少一个依赖 | 高 |
| 日志 | 标准库 `log/slog`（production 用 JSON handler） | TDD §4 允许 `slog` 或 `zap`，slog 无外部依赖 | 高 |
| Casbin 主版本 | **v3**（`github.com/casbin/casbin/v3`） | gorm-adapter v3.41.0 的 `go.mod` 明确依赖 `casbin/v3 v3.8.1`，选 v2 会不兼容 | 低 |
| `casbin_rule` 表 | 由迁移创建，并对 adapter 调用 `TurnOffAutoMigrate` | 避免表结构脱离版本控制 | 中 |
| 业务表建表 | **本里程碑不做**，留到租户/RBAC 里程碑 | 保持里程碑边界，见 NOT Building | 高 |

---

## UX Design

### Before
```
┌──────────────────────────────────────┐
│ 仓库：docs/TDD.md + .claude/prds/    │
│ 无 Go 代码 / 无服务 / 无健康检查      │
│ 开发者：无法启动、无法验证任何东西    │
└──────────────────────────────────────┘
```

### After
```
┌──────────────────────────────────────────────────────┐
│ docker compose up  →  postgres:16 + iam-api          │
│                                                      │
│ GET /health/live   → 200 {"data":{"status":"ok"},    │
│                             "request_id":"..."}      │
│ GET /health/ready  → 200 DB+schema+policy 全部就绪   │
│                    → 503 {"error":{"code":"NOT_READY"│
│                             ,"message":"..."},...}   │
│                                                      │
│ make migrate-up / down  → schema_migrations 版本可控 │
│                                                      │
│ 每个响应带 X-Request-ID；依赖不可用 → 启动失败退出   │
└──────────────────────────────────────────────────────┘
```

### Interaction Changes

| Touchpoint | Before | After | Notes |
| --- | --- | --- | --- |
| 服务启动 | 不存在 | `go run ./cmd/api` 或 `docker compose up` | 缺 `DATABASE_DSN` 立即退出码 1，不进入半可用状态 |
| 健康检查 | 不存在 | `/health/live`、`/health/ready` | ready 走真实依赖校验，不用固定 200 糊弄 |
| 数据库变更 | 无 | `make migrate-up` / `cmd/migrate` | 服务启动只校验迁移状态，不执行迁移 |
| 排障 | 无日志 | 结构化 slog + 全链路 `request_id` | 响应体与响应头同一 request_id |

---

## Mandatory Reading

实施前必读（行号对应当前 `docs/TDD.md`）：

| Priority | File | Lines | Why |
|---|---|---|---|
| P0 | `docs/TDD.md` | 411-435 | 分层规则：Domain 不得依赖 Gin/GORM/Casbin；决定包边界 |
| P0 | `docs/TDD.md` | 355-409 | 目标项目结构（`cmd/`、`internal/`、`migrations/`、`casbin/`） |
| P0 | `docs/TDD.md` | 532-558 | Casbin 模型（`r = sub, dom, obj, act`，`dom` 即 Tenant ID）必须逐字落地 |
| P0 | `docs/TDD.md` | 764-797 | 统一响应格式与状态码约定 |
| P1 | `docs/TDD.md` | 961-988 | 环境变量清单与"缺少必要配置快速失败"要求 |
| P1 | `docs/TDD.md` | 1006-1016 | Phase 1 验收标准（本里程碑的定义） |
| P1 | `docs/TDD.md` | 924-931 | 可用性：启动校验、就绪检查必须反映真实依赖状态 |
| P1 | `docs/TDD.md` | 635-663 | 表清单与约束（本里程碑只建 `casbin_rule`） |
| P2 | `docs/TDD.md` | 856-879 | 测试分层与 TDD 循环（先写失败测试） |
| P2 | `docs/TDD.md` | 990-1004 | CI/CD 流水线建议步骤 |
| P2 | `.claude/prds/iam-service.prd.md` | Scope / Milestone 1 | 需求边界与验收口径 |

---

## External Documentation

| Topic | Source | Key Takeaway |
|---|---|---|
| gorm-adapter | `raw.githubusercontent.com/casbin/gorm-adapter/master/README.md` | `NewAdapterByDB(db)` 默认表名 **`casbin_rule`**（v3.0.3 及以前是 `casbin_rules`）；`TurnOffAutoMigrate(db)` 关闭自动建表；事务场景用 `NewTransactionalAdapterByDB` + `casbin.NewTransactionalEnforcer`（**本里程碑不用，Phase 4 用**） |
| casbin 版本组合 | 同上 `README` + `go.mod` | gorm-adapter v3.41.0 依赖 `casbin/v3 v3.8.1`，因此必须用 casbin v3，不能 v2 |
| 依赖真实版本 | 本地 `go list -m -versions`（2026-10 实测） | gin v1.12.0 / gorm v1.31.2 / driver-postgres v1.6.3 / casbin v3.11.0 / gorm-adapter v3.41.0 / golang-migrate v4.20.1 / testcontainers-go v0.44.0（含 `modules/postgres` v0.44.0）/ testify v1.12.1 / uuid v1.6.0 |
| 本地工具链 | 本机 | Go go1.27.1 darwin/arm64；Docker CLI 存在但**守护进程当前不可用**（集成测试必须可跳过） |

---

## Patterns to Mirror

> **Greenfield 说明**：仓库当前**没有任何 Go 代码**，因此不存在可镜像的既有代码模式。以下模式来自 TDD §5.1 的强制分层规则与目标库官方示例，请将其视为"本项目将要确立的约定"，后续所有代码必须与之一致。

### LAYERING_RULE
// SOURCE: docs/TDD.md:411-435（TDD 强制约束，非代码）
// Domain：实体、值对象、业务不变量 —— 不得 import gin / gorm / redis / casbin
// Application：编排用例，通过 interface 访问 DB、身份平台、授权引擎
// Ports：接口定义（依赖倒置）
// Adapters：PostgreSQL/GORM、Casbin、Redis、ZITADEL 的实现
// Transport：Gin 路由、绑定、状态码、认证中间件、请求上下文
// Bootstrap：配置、DB、Casbin、依赖注入、HTTP Server 启动
// ⚠ 本里程碑不创建空目录占位（Go 中空目录无意义），只创建当下有内容的包；后续阶段按需新增

### CONFIG_PATTERN（本计划确立）
```go
// 目标形态：internal/config/config.go
type Config struct {
    AppEnv         string // dev | production，默认 dev
    HTTPAddr       string // 默认 :8080
    DatabaseDSN    string // 必填
    RedisAddr      string // 可选，本里程碑仅解析
    CasbinModelPath string // 默认 ./casbin/model.conf
    LogLevel       string // debug|info|warn|error，默认 info
    ShutdownTimeout time.Duration // 默认 10s
}

func Load() (Config, error) // 读取 os.Getenv；收集**全部**字段错误后一次性返回，便于一次修完
```
- 必填缺失 / 枚举非法 → 返回 error，`cmd/api` 打印后 `os.Exit(1)`（TDD §13.2 "缺少必要配置时快速失败"）
- 禁止使用 `panic` 处理配置错误

### RESPONSE_PATTERN
// SOURCE: docs/TDD.md:768-785（统一响应格式）
```json
{"data": {}, "request_id": "req-uuid"}
{"error": {"code": "FORBIDDEN", "message": "Permission denied"}, "request_id": "req-uuid"}
```
- 成功一律走 `{"data":..., "request_id":...}`；失败一律走 `{"error":{"code","message"},"request_id":...}`
- `request_id` 在**同一请求**的响应头 `X-Request-ID` 与响应体中必须一致

### LOGGING_PATTERN
- 使用 `log/slog`；`APP_ENV=production` → `slog.NewJSONHandler`，否则 `slog.NewTextHandler`
- 进程启动时 `slog.SetDefault(...)`，业务代码只用 `slog.Default()` / `slog.ErrorContext(ctx, ...)`
- **禁止**记录完整 Token、DSN 密码、任何凭据（TDD §10.4）

### TEST_STRUCTURE
// SOURCE: docs/TDD.md:856-868（测试分层）
| 层 | 位置 | 依赖 |
|---|---|---|
| 单元 | 与被测包同目录 `*_test.go` | 无外部服务 |
| 集成 | `tests/integration/` | testcontainers PostgreSQL |
- 集成测试开头必须能优雅跳过：`if testing.Short() { t.Skip(...) }` **且** `testcontainers.SkipIfProviderIsNotHealthy(t)`
- 断言库 `testify`：`require` 用于致命断言，`assert` 用于软断言
- 表驱动 + `t.Run(name, ...)`

---

## Files to Change

| File | Action | Justification |
|---|---|---|
| `go.mod` / `go.sum` | CREATE | Go 模块与依赖锁定 |
| `internal/config/config.go` | CREATE | 环境变量解析与启动校验 |
| `internal/config/config_test.go` | CREATE | 配置校验单测（先写失败测试） |
| `internal/transport/http/responses/response.go` | CREATE | 统一成功/失败响应封装 |
| `internal/transport/http/middleware/request_id.go` | CREATE | 生成/透传 request_id |
| `internal/transport/http/middleware/request_id_test.go` | CREATE | 单测 |
| `internal/transport/http/middleware/logging.go` | CREATE | slog 访问日志（替换 gin 默认 Logger） |
| `internal/transport/http/handlers/health.go` | CREATE | live/ready handler + 探针接口 |
| `internal/transport/http/handlers/health_test.go` | CREATE | 单测（200 / 503 两条路径） |
| `internal/transport/http/router.go` | CREATE | 路由装配与中间件链 |
| `internal/bootstrap/db.go` | CREATE | GORM 打开、连接池、Ping 超时 |
| `internal/bootstrap/schema.go` | CREATE | 启动期迁移状态校验（只校验，不执行） |
| `internal/bootstrap/casbin.go` | CREATE | Casbin Enforcer + adapter（关闭自动迁移） |
| `internal/bootstrap/app.go` | CREATE | 聚合 DB/Enforcer，提供 `Close()` |
| `casbin/model.conf` | CREATE | TDD §7.2 的 dom 模型，逐字落地 |
| `migrations/000001_init_casbin.up.sql` | CREATE | 建 `casbin_rule` |
| `migrations/000001_init_casbin.down.sql` | CREATE | 可逆 |
| `migrations/embed.go` | CREATE | `//go:embed *.sql`，供 `cmd/migrate` 使用 |
| `cmd/api/main.go` | CREATE | 启动、优雅关闭 |
| `cmd/migrate/main.go` | CREATE | 独立迁移命令 |
| `tests/integration/bootstrap_test.go` | CREATE | 真实 PostgreSQL 验证迁移 + 默认拒绝 |
| `Dockerfile` | CREATE | 多阶段构建、非 root |
| `docker-compose.yml` | CREATE | postgres + iam-api 本地编排 |
| `.dockerignore` | CREATE | 减小构建上下文 |
| `.env.example` | CREATE | TDD §13.2 变量清单 |
| `Makefile` | CREATE | fmt/vet/test/build/migrate/up 统一入口 |
| `.github/workflows/ci.yml` | CREATE | CI |
| `README.md` | CREATE | 启动、迁移、测试说明 |

## NOT Building

- **业务表迁移**：`users` / `tenants` / `memberships` / `roles` / `permissions` / `role_permissions` / `membership_roles` / `audit_logs` / `idempotency_keys` —— 属于租户与 RBAC 里程碑（PRD Milestone 3/4）
- **ZITADEL 接入**：OIDC Issuer、Token 验证、认证中间件、Principal、用户按需同步 —— Milestone 2
- **租户/成员/角色/权限的业务用例与 API** —— Milestone 3/4/5
- **`/authorize` 授权判定接口** —— Milestone 4
- **审计日志写入** —— Milestone 5
- **Redis 客户端与限流/缓存** —— 可选组件，Phase 6
- **Casbin Watcher / 多实例策略失效** —— Phase 4/6
- **OpenAPI 3.0 文档** —— Milestone 5
- **Vue 管理后台前端** —— PRD 明确不在本次交付范围
- **traefik、服务间 Client Credentials 认证** —— 部署/集成阶段
- **Casbin 事务性 Enforcer** —— 本里程碑只用普通 Enforcer；`NewTransactionalAdapterByDB` 留到 Milestone 4

---

## Step-by-Step Tasks

> TDD 顺序（TDD §11.2）：先写失败测试 → 最小实现 → 重构 → 补异常路径。每个任务都按此顺序执行。

### Task 1: 初始化 Go 模块与依赖
- **ACTION**: 在工作区根目录创建 `go.mod`，`go mod init github.com/example/iam-service`，然后 `go get` 锁定版本
- **IMPLEMENT**:
  ```bash
  go mod init github.com/example/iam-service
  go get github.com/gin-gonic/gin@v1.12.0 \
         gorm.io/gorm@v1.31.2 gorm.io/driver/postgres@v1.6.3 \
         github.com/casbin/casbin/v3@v3.11.0 github.com/casbin/gorm-adapter/v3@v3.41.0 \
         github.com/golang-migrate/migrate/v4@v4.20.1 \
         github.com/google/uuid@v1.6.0 github.com/stretchr/testify@v1.12.1 \
         github.com/testcontainers/testcontainers-go@v0.44.0 \
         github.com/testcontainers/testcontainers-go/modules/postgres@v0.44.0
  ```
  `go.mod` 首行 `go 1.27`（gorm-adapter 要求 ≥1.24，本机 1.27.1）
- **MIRROR**: LAYERING_RULE（目录按分层建，不建空包）
- **IMPORTS**: 无
- **GOTCHA**:
  - **必须选 casbin v3**：gorm-adapter v3.41.0 依赖 `casbin/v3`，混用 v2 会编译失败
  - testcontainers `modules/postgres` 是**独立 module**，必须单独 `go get`
  - `go mod tidy` 会把未使用的依赖移除，**在所有代码写完之后再执行 tidy**
- **VALIDATE**: `go version` 输出 go1.27.x；`cat go.mod` 含全部依赖；`go build ./...` 通过（此时应无包或仅有后续任务产物）

### Task 2: 配置包（先写 `config_test.go`）
- **ACTION**: 实现 `internal/config`，严格按 TDD §13.2 的变量名与"缺少必要配置快速失败"
- **IMPLEMENT**:
  - `Config` 结构见 CONFIG_PATTERN
  - `Load() (Config, error)`：`os.Getenv` 读取；用 `errors.Join` 聚合所有字段错误（一次返回全部问题，而非首个就退出）
  - 校验规则：`AppEnv ∈ {dev, production}`（为空取 `dev`）；`LogLevel ∈ {debug, info, warn, error}`；`DatabaseDSN` 非空；`HTTPAddr` 非空（默认 `:8080`）
  - 所有错误用 `fmt.Errorf("config: %w", err)` 包裹，消息含字段名
- **MIRROR**: CONFIG_PATTERN
- **IMPORTS**: `errors`, `fmt`, `os`, `strings`, `time`
- **GOTCHA**: TDD §13.2 中 ZITADEL_* 与 AUTH_* 变量**本里程碑不解析**（Milestone 2 再加），现在引入会导致所有环境启动失败
- **VALIDATE**: `go test ./internal/config/...` 全绿（测试先红后绿）

### Task 3: 统一响应 + 请求 ID 中间件（先写测试）
- **ACTION**: 落地 TDD §9.6 的响应信封；实现 request_id 中间件与 slog 访问日志
- **IMPLEMENT**:
  - `responses.RequestIDContextKey = "request_id"`；`responses.OK(c, data)` / `responses.Fail(c, status, code, message)`；两者都从 `c.GetString(RequestIDContextKey)` 取 id 写入响应体；`Fail` 同时设置 `X-Request-ID`
  - `middleware.RequestID()`：无 `X-Request-ID` 头 → `uuid.NewString()`；有且长度 ≤128 且只含 `[A-Za-z0-9-_.]` → 原样透传；否则丢弃重新生成（**防日志注入与超长头**）；`c.Set(key, id)` + `c.Writer.Header().Set("X-Request-ID", id)`
  - `middleware.Logging()`：`c.Next()` 后记录 method、path、`status`、`latency`、`request_id`；**不记录 Authorization 头与 body**
- **MIRROR**: RESPONSE_PATTERN、LOGGING_PATTERN
- **IMPORTS**: `github.com/gin-gonic/gin`, `github.com/google/uuid`, `net/http`, `log/slog`, `time`, `github.com/example/iam-service/internal/transport/http/responses`
- **GOTCHA**: 依赖方向必须是 `middleware → responses`；**禁止** `responses` 反向 import `middleware`（会成环），所以 context key 常量定义在 `responses` 包内
- **VALIDATE**: `go test ./internal/transport/...` 全绿；用例见 Testing Strategy

### Task 4: 健康检查 handler（先写测试）
- **ACTION**: 实现 live/ready，ready 必须反映真实依赖状态（TDD §12.2）
- **IMPLEMENT**:
  - `handlers.ReadinessProbe interface { Check(ctx context.Context) error }`（定义在 handlers 包，便于用 fake 测试）
  - `NewHealthHandler(probe ReadinessProbe) *HealthHandler`
  - `Live`：`responses.OK(c, map[string]string{"status":"ok"})` → 恒 200
  - `Ready`：`probe.Check(ctx)`；nil → 200 `{"status":"ok"}`；err → `responses.Fail(c, 503, "NOT_READY", "service not ready")` **且** `slog.WarnContext` 记录原因（不向客户端泄漏 DSN 等细节）
- **MIRROR**: RESPONSE_PATTERN
- **IMPORTS**: `context`, `net/http`, `log/slog`, `github.com/gin-gonic/gin`, `.../responses`
- **GOTCHA**: `Live` 不得触碰 DB（存活探针挂了会导致容器被误杀）；只有 `Ready` 走依赖
- **VALIDATE**: `go test ./internal/transport/http/handlers/...` 覆盖 200 与 503 两条路径

### Task 5: Casbin 模型与首个迁移
- **ACTION**: 逐字落地 TDD §7.2 的 `casbin/model.conf`；用 golang-migrate 建 `casbin_rule`
- **IMPLEMENT**:
  - `casbin/model.conf`（**与 TDD 完全一致**）：
    ```ini
    [request_definition]
    r = sub, dom, obj, act

    [policy_definition]
    p = sub, dom, obj, act

    [role_definition]
    g = _, _, _

    [policy_effect]
    e = some(where (p.eft == allow)) && !some(where (p.eft == deny))

    [matchers]
    m = r.dom == p.dom && \
        r.obj == p.obj && \
        r.act == p.act && \
        (r.sub == p.sub || g(r.sub, p.sub, r.dom))
    ```
  - `migrations/000001_init_casbin.up.sql`：
    ```sql
    CREATE TABLE IF NOT EXISTS casbin_rule (
        id     BIGSERIAL PRIMARY KEY,
        ptype  VARCHAR(512) NOT NULL,
        v0     VARCHAR(512) NOT NULL DEFAULT '',
        v1     VARCHAR(512) NOT NULL DEFAULT '',
        v2     VARCHAR(512) NOT NULL DEFAULT '',
        v3     VARCHAR(512) NOT NULL DEFAULT '',
        v4     VARCHAR(512) NOT NULL DEFAULT '',
        v5     VARCHAR(512) NOT NULL DEFAULT ''
    );
    CREATE UNIQUE INDEX IF NOT EXISTS idx_casbin_rule_unique
        ON casbin_rule (ptype, v0, v1, v2, v3, v4, v5);
    ```
  - `migrations/000001_init_casbin.down.sql`：`DROP INDEX IF EXISTS idx_casbin_rule_unique; DROP TABLE IF EXISTS casbin_rule;`
  - `migrations/embed.go`：`//go:embed *.sql` → `var FS embed.FS`
- **IMPORTS**: `embed`（embed.go）
- **GOTCHA**:
  - 表名必须是 **`casbin_rule`**（单数）；gorm-adapter ≤v3.0.3 用的 `casbin_rules` 是历史遗留
  - 列用 `NOT NULL DEFAULT ''`：adapter 对未使用的 v 列写入空字符串而非 NULL，若允许 NULL 会让唯一索引失效；同时 `TurnOffAutoMigrate` 后表由我们掌控，结构必须与 adapter 的读写预期一致
  - 迁移文件内**不要**手写 `BEGIN/COMMIT`（golang-migrate 自行处理）
  - 文件命名必须是 `{version}_{title}.up.sql` / `.down.sql` 成对
- **VALIDATE**: `make migrate-up` 后 `psql -c '\d casbin_rule'` 列与索引符合预期

### Task 6: bootstrap（DB / schema 校验 / Casbin）
- **ACTION**: 完成 `internal/bootstrap` 四个文件
- **IMPLEMENT**:
  - `db.go`：`OpenDB(dsn string) (*gorm.DB, error)` → `postgres.New(postgres.Config{DSN: dsn})` + `gorm.Open`；取 `*sql.DB` 设置 `SetMaxOpenConns(25)`、`SetMaxIdleConns(10)`、`SetConnMaxLifetime(30*time.Minute)`；用 5s 超时的 ctx 做 `PingContext`，失败返回 error
    - `gorm.Config{TranslateError: true}`（为后续把唯一约束冲突映射成 409 做准备；**若当前 gorm 版本无该字段则删除此项**）
  - `schema.go`：`VerifyMigrations(ctx, db) error` —— 校验 `schema_migrations` 表存在、`dirty = false`、版本 > 0、`casbin_rule` 表存在；任一不满足返回明确错误。**只校验，不执行迁移**（TDD §13.3）
  - `casbin.go`：`NewEnforcer(db *gorm.DB, modelPath string) (*casbin.Enforcer, error)`：
    ```go
    db = gormadapter.TurnOffAutoMigrate(db)
    a, err := gormadapter.NewAdapterByDB(db)
    e, err := casbin.NewEnforcer(modelPath, a)
    if err := e.LoadPolicy(); err != nil { return nil, err }
    ```
  - `app.go`：`type App struct { DB *gorm.DB; Enforcer *casbin.Enforcer }` + `New(cfg) (*App, error)`（按 配置→DB→校验迁移→Casbin 顺序，任一步失败即返回）+ `Close() error`（关 DB）
  - `App.Check(ctx) error`：DB Ping + `VerifyMigrations` + Enforcer 已加载；供 `/health/ready` 使用
- **MIRROR**: LAYERING_RULE（bootstrap 是唯一允许 import gorm/casbin 的装配层；Domain/Application 不碰）
- **IMPORTS**: `context`, `database/sql`, `errors`, `fmt`, `time`, `gorm.io/gorm`, `gorm.io/driver/postgres`, `github.com/casbin/casbin/v3`, `gormadapter "github.com/casbin/gorm-adapter/v3"`, `.../internal/config`
- **GOTCHA**:
  - **必须** `TurnOffAutoMigrate`，否则 adapter 会自动建表，与迁移脱节且可能结构不一致
  - `gormadapter.NewAdapterByDB` 返回 `(adapter, error)`，两个返回值都要处理
  - casbin v3 的 `NewEnforcer(modelPath, adapter)` 会内部 `LoadPolicy`；仍显式调用一次以便错误可见
  - **不要**在这里引入 Redis（本里程碑不需要）
- **VALIDATE**: 集成测试（Task 9）中 `App.Check(ctx) == nil`，且空策略下 `Enforce` 返回 false

### Task 7: 路由与 API 入口（优雅关闭）
- **ACTION**: 装配 Gin 路由；`cmd/api/main.go` 完成启动与优雅关闭
- **IMPLEMENT**:
  - `router.go`：`NewRouter(app *bootstrap.App) *gin.Engine`：
    ```go
    gin.SetMode(gin.ReleaseMode) // AppEnv == production
    r := gin.New()
    r.Use(gin.Recovery(), middleware.RequestID(), middleware.Logging())
    r.GET("/health/live", handlers.NewHealthHandler(app).Live)
    r.GET("/health/ready", handlers.NewHealthHandler(app).Ready)
    ```
    （**不加** gin 默认 Logger）
  - `main.go`：
    1. `config.Load()` 失败 → `slog.Error` + `os.Exit(1)`
    2. `slog.SetDefault` 按 AppEnv 选 JSON/Text handler
    3. `bootstrap.New(cfg)` 失败 → 退出 1
    4. `srv := &http.Server{Addr, Handler: router, ReadHeaderTimeout: 5s, ReadTimeout: 15s, WriteTimeout: 15s, IdleTimeout: 60s}`
    5. goroutine `srv.ListenAndServe()`（非 `ErrServerClosed` 记为 error）
    6. `ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)`；`<-ctx.Done()`；`shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)`；`srv.Shutdown(shutdownCtx)`；`app.Close()`
- **MIRROR**: LAYERING_RULE、LOGGING_PATTERN
- **IMPORTS**: `context`, `errors`, `net/http`, `os`, `os/signal`, `syscall`, `log/slog`, `time`, `github.com/gin-gonic/gin`, `.../internal/bootstrap`, `.../internal/config`, `.../internal/transport/http`, `.../internal/transport/http/handlers`, `.../internal/transport/http/middleware`
- **GOTCHA**:
  - 必须设置 `ReadHeaderTimeout`（否则易被 Slowloris 打），TDD §12.1 要求合理超时
  - 用 `signal.NotifyContext` 而非裸 `signal.Notify` + channel
  - `Shutdown` 之后才 `app.Close()`，顺序反了会中断在途请求
  - 启动期**不**执行数据库迁移（多实例并发迁移会打架，TDD §13.3）
- **VALIDATE**: `go run ./cmd/api` 后 `curl -i localhost:8080/health/live` → 200 且含 `X-Request-ID`；`Ctrl+C` 后进程 10s 内干净退出

### Task 8: 独立迁移命令
- **ACTION**: `cmd/migrate/main.go`，用 embed 的 SQL 文件 + golang-migrate
- **IMPLEMENT**:
  - flag：`-cmd`（`up|down|version|force`，默认 `up`）、`-steps int`（默认 0 = 全部）
  - `migrate.NewWithSourceInstance("iofs", iofs.New(migrations.FS), dsn)`（DSN 取自 `DATABASE_DSN`，**不用** gorm）
  - `up`：steps==0 → `m.Up()`，否则 `m.Steps(steps)`；`down`：`m.Down()`；`version`：打印 version/dirty；`force`：仅用于修复 dirty 状态
  - `ErrNoChange` 视为成功（不是错误）
- **IMPORTS**: `database/sql`, `errors`, `flag`, `fmt`, `os`, `log/slog`, `github.com/golang-migrate/migrate/v4`, `_ "github.com/golang-migrate/migrate/v4/database/postgres"`, `github.com/golang-migrate/migrate/v4/source/iofs`, `.../migrations`
- **GOTCHA**:
  - 需要 blank import 注册 postgres 驱动，漏了会报 "source driver not found / database driver unknown"
  - `iofs` 的 embed FS 根目录就是 `migrations/`，文件名须以 `.sql` 结尾且符合 `{ver}_{title}.{up|down}.sql`
  - `m.Up()` 在已是最新时返回 `migrate.ErrNoChange`，必须单独判断，否则 CI 会误报失败
  - 迁移失败会置 dirty，需要 `force` 修复；README 要写明
- **VALIDATE**: `make migrate-up` → `schema_migrations` version=1；`make migrate-down` → 表消失且无错误

### Task 9: 集成测试（真实 PostgreSQL）
- **ACTION**: `tests/integration/bootstrap_test.go`，用 testcontainers 验证迁移、schema 校验、Casbin 默认拒绝、ready 语义
- **IMPLEMENT**:
  ```go
  func TestMain(m *testing.M) { /* 启动一次 postgres 容器，供本包所有用例复用 */ }
  ```
  - 容器：`postgres:16-alpine`，`WaitForLog`/`wait.ForListeningPort("5432/tcp")`
  - 用例：
    1. `TestMigrationsUpIdempotent`：up 两次 → 无错（第二次 `ErrNoChange`）→ `casbin_rule` 存在且列齐全
    2. `TestVerifyMigrationsFailsWhenTableMissing`：down 之后 `VerifyMigrations` 返回 error
    3. `TestEnforcerDefaultDeny`：加载空策略后 `Enforce("user-1","tenant-1","member","invite") == false, nil`（TDD §7.5 默认拒绝）
    4. `TestReadyProbeFailsWhenDatabaseUnreachable`：关闭 DB 后 `App.Check(ctx) != nil` → handler 返回 **503**（不得放行）
  - 跳过条件：`if testing.Short() { t.Skip("integration") }` + `testcontainers.SkipIfProviderIsNotHealthy(t)`
- **MIRROR**: TEST_STRUCTURE
- **IMPORTS**: `context`, `database/sql`, `testing`, `time`, `github.com/stretchr/testify/require`, `github.com/testcontainers/testcontainers-go`, `.../modules/postgres`, `github.com/example/iam-service/internal/bootstrap`
- **GOTCHA**:
  - **本机 Docker 守护进程当前不可用** → 未加跳过逻辑会让 `go test ./...` 直接失败；务必两个跳过条件都加
  - 容器启动慢（数十秒），用 `TestMain` 复用一个容器，不要每个用例起一个
  - 集成测试里的 DSN 用容器映射出的主机端口，别写死 5432
  - Casbin `Enforce` 在模型/策略异常时可能返回 error，需断言 `allowed==false` 且按要求区分 error
- **VALIDATE**: 启动 Docker 后 `go test ./tests/integration/... -count=1` 全绿；未启动 Docker 时 `go test -short ./...` 不失败

### Task 10: Docker 与本地编排
- **ACTION**: `Dockerfile`、`docker-compose.yml`、`.dockerignore`、`.env.example`
- **IMPLEMENT**:
  - `Dockerfile`：多阶段
    ```
    FROM golang:1.27-alpine AS build
    WORKDIR /src
    COPY go.mod go.sum ./
    RUN go mod download
    COPY . .
    RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/iam-api ./cmd/api \
     && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/iam-migrate ./cmd/migrate
    FROM gcr.io/distroless/static:nonroot
    COPY --from=build /out/iam-api /iam-api
    COPY --from=build /out/iam-migrate /iam-migrate
    COPY casbin/model.conf /casbin/model.conf
    USER 65532:65532
    EXPOSE 8080
    ENTRYPOINT ["/iam-api"]
    ```
  - `docker-compose.yml`：`postgres:16-alpine`（带 healthcheck）+ `iam-api`（`depends_on: service_healthy`，`APP_ENV=production`、`DATABASE_DSN=postgres://...`、`CASBIN_MODEL_PATH=/casbin/model.conf`）
  - `.env.example`：按 TDD §13.2 列出全部变量（ZITADEL_*/AUTH_* 加注释说明"Milestone 2 启用"）
- **GOTCHA**:
  - 迁移文件已 embed 进二进制，**无需** COPY `migrations/`；`casbin/model.conf` 必须 COPY（路径由 `CASBIN_MODEL_PATH` 指定）
  - distroless 无 shell，排查时不方便；如需调试可临时改用 `alpine` 基础镜像
  - `golang:1.27-alpine` 若不存在则改用 `golang:1.27.1-alpine`
  - compose 里 `DATABASE_DSN` 的 host 用服务名 `postgres`，不是 `localhost`
- **VALIDATE**: `docker build -t iam-service:dev .` 成功（需先启动 Docker）；`docker compose up` 后 `curl localhost:8080/health/ready` → 200

### Task 11: Makefile 与 CI
- **ACTION**: 统一入口 + GitHub Actions
- **IMPLEMENT**:
  - `Makefile`：`fmt`（`gofmt -l -w .`）、`vet`、`test`（`go test -short ./...`）、`test-integration`、`build`、`migrate-up`、`migrate-down`、`up`/`down`（docker compose）、`ci`（fmt-check + vet + test）
  - `.github/workflows/ci.yml`：`on: [push, pull_request]`；steps 按 TDD §13.3：
    1. `gofmt -l .`（有输出即失败）
    2. `go vet ./...`
    3. `go test -short -race ./...`
    4. `go build ./...`
    5. `go test ./tests/integration/...`（GH Runner 带 Docker）
    6. `docker build`
- **GOTCHA**:
  - `gofmt -l .` 在 CI 中必须判空：`test -z "$(gofmt -l .)"`
  - 仓库**尚未 `git init`**（本机 `git rev-parse` 报 NO_GIT）；CI 文件可以先加，但推送前需初始化仓库
  - 依赖漏洞扫描（TDD 13.3 第 5 步）建议用 `govulncheck`，本里程碑先留 TODO 注释，不阻塞
- **VALIDATE**: `make ci` 本地全绿；CI 文件 YAML 语法正确

### Task 12: README 与里程碑收尾
- **ACTION**: 写 README（启动、迁移、测试、排障），跑全量校验
- **IMPLEMENT**: README 包含：本地启动步骤、环境变量表、`make migrate-up/down`、dirty 状态修复方式（`cmd/migrate -cmd force -steps N`）、测试命令与 Docker 依赖说明、健康检查端点
- **GOTCHA**: 明确写出"服务启动不自动执行迁移"这一约定，避免后续误用
- **VALIDATE**: 见 Validation Commands 全量

---

## Testing Strategy

### Unit Tests

| Test | Input | Expected Output | Edge Case? |
|---|---|---|---|
| `TestConfigLoad_MissingDatabaseDSN` | 清空 `DATABASE_DSN` | error 含 `DATABASE_DSN` | 否 |
| `TestConfigLoad_InvalidAppEnv` | `APP_ENV=staging` | error 含 `APP_ENV` | 是 |
| `TestConfigLoad_InvalidLogLevel` | `LOG_LEVEL=trace` | error 含 `LOG_LEVEL` | 是 |
| `TestConfigLoad_Defaults` | 仅设 `DATABASE_DSN` | `HTTPAddr=":8080"`、`AppEnv="dev"`、`LogLevel="info"`、`CasbinModelPath="./casbin/model.conf"` | 否 |
| `TestConfigLoad_MultipleErrors` | `DATABASE_DSN` 空 + `APP_ENV` 非法 | 单个 error 同时含两个字段名（一次修完） | 是 |
| `TestRequestID_Generated` | 无请求头 | 响应头 `X-Request-ID` 非空、合法 UUID | 否 |
| `TestRequestID_PassThrough` | `X-Request-ID: abc-123` | 响应头回显 `abc-123` | 否 |
| `TestRequestID_RejectsMalicious` | 含换行/超长(>128) | 丢弃并重新生成，响应头不含注入内容 | 是 |
| `TestRequestID_InBody` | 任意请求 | 响应体 `request_id` == 响应头 `X-Request-ID` | 否 |
| `TestHealth_Live` | GET `/health/live` | 200，`data.status=ok` | 否 |
| `TestHealth_Ready_OK` | 探针返回 nil | 200，`data.status=ok` | 否 |
| `TestHealth_Ready_NotReady` | 探针返回 error | **503**，`error.code=NOT_READY`，且 `request_id` 一致 | 是 |
| `TestHealth_LiveIgnoresProbe` | 探针恒返回 error | live 仍 200 | 是 |

### Integration Tests（`tests/integration`，需 Docker）

| Test | Setup | Expected |
|---|---|---|
| `TestMigrationsUpIdempotent` | postgres 容器 | up 一次建表；再 up 返回 `ErrNoChange` 且不报错 |
| `TestVerifyMigrationsFailsWhenTableMissing` | 执行 down | `VerifyMigrations` 返回 error |
| `TestEnforcerDefaultDeny` | 空策略 | `Enforce("user-1","tenant-1","member","invite")` → `(false, nil)` |
| `TestReadyProbeFailsWhenDatabaseUnreachable` | 关闭 DB 连接 | `App.Check` 返回 error → HTTP **503**（fail closed） |

### Edge Cases Checklist
- [x] 空输入（无请求头、无配置）
- [x] 非法枚举值（APP_ENV / LOG_LEVEL）
- [x] 恶意/超长请求头（request_id 注入）
- [x] 依赖不可用（DB 不可达 → 503，不自动放行）
- [x] 迁移重复执行（幂等）
- [x] 迁移回滚（down 可逆）
- [ ] 并发访问 —— 本里程碑无写路径，留到业务里程碑
- [ ] 权限拒绝 —— 无受保护接口，Milestone 2 起覆盖

---

## Validation Commands

### 静态分析
```bash
gofmt -l .                 # EXPECT: 无输出
go vet ./...               # EXPECT: 无输出、exit 0
```
### 构建
```bash
go build ./...             # EXPECT: exit 0
go run ./cmd/migrate -cmd version   # EXPECT: 打印当前迁移版本
```
### 单元测试
```bash
go test -short -race ./... # EXPECT: 全部 PASS（无需 Docker）
```
### 集成测试（需 Docker 守护进程）
```bash
go test ./tests/integration/... -count=1 -v
# EXPECT: PASS；Docker 不可用时必须 SKIP 而非 FAIL
```
### 数据库校验
```bash
docker compose up -d postgres
make migrate-up
docker compose exec postgres psql -U postgres -d iam -c '\d casbin_rule'
# EXPECT: 7 列 + idx_casbin_rule_unique 唯一索引
make migrate-down
docker compose exec postgres psql -U postgres -d iam -c '\dt'
# EXPECT: casbin_rule 已删除，仅剩 schema_migrations
```
### 手动冒烟
```bash
go run ./cmd/api &
curl -i http://localhost:8080/health/live    # EXPECT: 200 + X-Request-ID
curl -i http://localhost:8080/health/ready   # EXPECT: 200（DB 已迁移）
# 停掉 postgres 后再次 ready → EXPECT: 503 + error.code=NOT_READY
```

### Manual Validation
- [ ] 未设 `DATABASE_DSN` 启动 → 立即退出，日志明确指出缺失字段
- [ ] `Ctrl+C` → 10s 内完成优雅关闭，退出码 0
- [ ] 所有响应体 `request_id` 与响应头一致
- [ ] 日志中不含 DSN 密码、Token

---

## Acceptance Criteria
- [ ] `gofmt -l .` 无输出、`go vet ./...` 无错误
- [ ] `go test -short ./...` 全绿（不依赖 Docker）
- [ ] Docker 可用时 `go test ./tests/integration/...` 全绿；不可用时跳过
- [ ] `/health/live` 恒 200；`/health/ready` 真实反映 DB + 迁移 + 策略状态，失败返回 503
- [ ] `casbin_rule` 由版本化迁移创建，且 adapter 已 `TurnOffAutoMigrate`
- [ ] 服务启动**不**执行迁移，只校验迁移状态
- [ ] 响应格式与 TDD §9.6 一致；request_id 全链路一致
- [ ] Dockerfile 可构建、`docker compose up` 可启动并通过 health
- [ ] 无业务表、无认证、无 Redis、无 OpenAPI（严格符合 NOT Building）

## Completion Checklist
- [ ] 遵循 LAYERING_RULE（Domain 层尚未创建，Adapters 仅 bootstrap 装配）
- [ ] 错误处理：`fmt.Errorf("...: %w", err)` 包裹，配置错误聚合返回
- [ ] 日志走 `slog`，production 为 JSON
- [ ] 测试表驱动 + testify，集成测试可跳过
- [ ] 无硬编码 DSN/端口（走配置，默认值集中定义）
- [ ] README 更新（含"启动不执行迁移"约定）
- [ ] 未越界实现 NOT Building 中的任何一项
- [ ] 自包含：实施时无需再搜索代码库或提问

## Risks
| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| casbin v2/v3 版本混淆导致编译失败 | 中 | 高 | 已确认 gorm-adapter v3.41.0 依赖 casbin/v3；`go.mod` 固定 `casbin/v3 v3.11.0`，禁止引入 v2 |
| Docker 守护进程不可用，集成测试无法在本地验证 | 高（本机已确认） | 中 | 双重跳过（`testing.Short()` + `SkipIfProviderIsNotHealthy`）；冒烟验证列为手动步骤，需在 Docker 启动后补跑 |
| `TurnOffAutoMigrate` 后表结构与 adapter 预期不符 | 中 | 高 | 迁移列用 `NOT NULL DEFAULT ''`，集成测试断言 `Enforce` 行为与列齐全 |
| gorm `TranslateError` 字段在当前版本不存在 | 低 | 低 | 编译报错即删除该配置项，不影响其余逻辑 |
| `golang:1.27-alpine` 镜像标签不存在 | 低 | 中 | 改用 `golang:1.27.1-alpine` |
| 迁移置 dirty 后阻塞后续迁移 | 中 | 中 | `cmd/migrate -cmd force -steps N`；README 写明修复步骤 |
| 误把业务表建表塞进本里程碑 | 中 | 中 | NOT Building 已明确；Task 5 只有一个迁移文件 |
| 空目录占位包导致 `go build ./...` 失败 | 中 | 低 | 明确"不创建空目录"，只建有内容的包 |

## Notes
- **版本来源**：所有依赖版本由本机 `go list -m -versions` 于 2026-10 实测获得，非记忆值；`go get` 时请按上表锁定，不要 `go get -u`。
- **后续阶段的接口预留（本里程碑不实现）**：TDD §7.5 的 `Authorizer.Enforce(ctx, subject, tenantID, resource, action)` 与 §6.3 的 `Principal` 将在 Milestone 2/4 于 `internal/ports` 定义；本计划不预先创建 `internal/ports`、`internal/domain`、`internal/application` 空包。
- **Casbin 事务**：gorm-adapter 提供 `NewTransactionalAdapterByDB` + `casbin.NewTransactionalEnforcer`，这正是 TDD §8.3 要求的"策略与业务表同事务"能力；**留到 Milestone 4 引入**，届时需注意本里程碑的 `NewEnforcer` 需替换为事务版构造方式（预留 `bootstrap.NewEnforcer` 单一出口以便替换）。
- **仓库未初始化 Git**：`.github/workflows/ci.yml` 可先创建，推送前需 `git init`。
- 本计划对应 PRD Milestone 1；完成后将状态置为 `in-progress`，验收通过后置 `complete`，再执行 `/plan` 取 Milestone 2（身份接入）——该阶段开始前需先回答 PRD Open Questions 中的"令牌验证方式""平台管理员边界""租户创建权限"。
