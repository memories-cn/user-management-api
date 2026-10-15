# Plan: 生产加固（PRD Milestone 6）

**Source PRD**: `.claude/prds/iam-service.prd.md`
**Selected Milestone**: 6 — 生产加固
**Complexity**: Large

## Summary

把服务从"功能正确"推到"可上线"：补上限流与监控两类缺失的运行期能力；用真实负载验证 TDD §12.1 的性能目标（授权判定 P95 ≤ 10ms、常规 API P95 ≤ 200ms、100 并发）；用 pg_dump/pg_restore 演练验证备份恢复；把 CI 从"构建门禁"扩展为含漏洞扫描、迁移校验与发布流水线的完整流程；顺带修掉 `SetTrustedProxies` 未配置这一影响限流与审计 IP 可信度的安全缺口。

验收口径（PRD Milestone 6）：性能目标经压测验证，核心安全测试全部通过，具备限流、监控、备份恢复与发布流程。

## Requirements Restatement

- **性能目标经压测验证**（PRD）：纯本地授权决策 P95 ≤ 10ms；常规业务 API P95 ≤ 200ms（不含交互式登录）；单实例至少 100 并发。TDD §12.1 明确这些是**首期工程目标，需在目标部署环境压测验证，并非当前实测结果**——因此本里程碑的交付物是"可复现的测量手段 + 结果记录"，而不是宣称达标。
- **核心安全测试全部通过**：跨租户越权、撤权时效、故障关闭三条线已有覆盖（M3–M5），需补齐限流行为、请求头伪造、错误信息不泄漏内部细节，并把多实例策略一致性窗口测出来（回答 PRD Open Question）。
- **限流**：TDD §10.4"对管理 API 和授权 API 实施限流"。
- **监控**：暴露可供容量规划与告警使用的指标。
- **备份与恢复**：TDD §16"数据库迁移、备份与恢复流程经过验证"。
- **发布流程**：TDD §13.3 的 CI/CD 九步流水线，含依赖漏洞扫描（ci.yml 中长期挂着的 TODO）。
- **配置与启动校验**：TDD §12.2"启动时校验配置、数据库迁移状态和必要依赖"；§13.2"所有生产配置应在启动时校验，缺少必要配置时快速失败"。

## Patterns to Mirror

> 以下均来自仓库中**已存在并通过测试**的代码，实施时必须沿用，不要另起风格。

| Category | Source | Pattern |
| --- | --- | --- |
| Naming | `internal/config/config.go:37-38,45-53,79-80` | 默认值集中在 `defaultXxx` 常量块；环境变量名集中在 `EnvXxx` 常量块（扁平命名、无前缀）；未使用的字段显式注释"仅解析不使用" |
| Naming | `internal/adapters/casbin/authorizer.go:26-37,88,118` | 适配器持有依赖 + 一个后台协程；协程 `defer recover` 兜底；错误经 `fmt.Errorf("中文动作失败：%w", err)` 包装；启动期校验依赖非空 |
| Naming | `internal/bootstrap/db.go:18-24` | 运行期参数集中在文件顶部 `const` 块并附注释说明取值理由 |
| Errors | `internal/transport/http/handlers/errors.go:47-84` | 错误码常量 + `writeError` 的 `switch { case errors.Is(...) }` 集中映射；`default` 记 ERROR 日志 + 500，不泄漏内部细节 |
| Transport | `internal/transport/http/middleware/require_permission.go:33` | 守卫中间件 = 窄接口 + `gin.HandlerFunc`；必须 `c.Abort()`；用 `slog.WarnContext` 记录拒绝原因（含 subject、request_id） |
| Transport | `internal/transport/http/router.go:23-30,42-49` | 全局中间件顺序固定；`/health` 不挂认证；分组统一挂载，避免个别路由漏挂 |
| Bootstrap | `internal/bootstrap/app.go:78,167` | `New(cfg)` 顺序装配、任一步失败即 `closeDB` 返回；`Check(ctx)` 汇总全部依赖的就绪状态 |
| Tests | `tests/integration/tenancy_stack_test.go:43` | `newTenancyStack` 装配"真实 DB + OIDC 桩 + 路由"，装配顺序与 `bootstrap.New` 逐行对应 |
| Tests | `tests/integration/rbac_routes_test.go:42` | 遍历 `router.Routes()` 做覆盖断言——本项目已两次用该手法防漏挂（权限、OpenAPI） |
| Tests | `internal/oidctest/stub.go:28-36` | 测试桩支持注入故障（`jwksBroken`/`discoveryBroken`）；故障开关用 `atomic.Bool` |
| Tests | `internal/rbactest/fakes.go` | 共享内存替身包（对齐 `oidctest` 先例），避免各测试包重复实现替身 |
| CI | `.github/workflows/ci.yml:18-46` | 步骤带编号注释说明意图；失败信息可读；`# TODO:` 标注未接入项 |

## 本计划锁定的决策

| 决策 | 选择 | 理由 | 可逆性 |
| --- | --- | --- | --- |
| **限流存储** | 进程内令牌桶（`golang.org/x/time/rate`），通过 `ports.RateLimiter` 抽象隔离 | Redis 未部署（`config.go:79` 仅解析不使用），为其引入一套适配层与故障模式不成比例；多实例下每实例各自限流对滥用防护已足够 | 高 |
| **限流故障语义** | 限流器内部错误 → **放行并记 ERROR 日志** | 限流是可用性保护而非授权判定；PRD"0 次自动放行"针对的是授权依赖。内存限流器结构上不会因 DB/Redis 故障失败，唯一现实故障是 OOM——此时把服务整体打死比放过请求更糟 | 中 |
| **限流键** | 已认证 → `subject:<userID>`；未认证 → `ip:<clientIP>` | 与身份模型一致；未认证请求无 subject 可用 | 中 |
| **限流分类** | `/authorize` 走授权类；其余 `/api/v1` 走管理类 | TDD §10.4 明确区分"管理 API 和授权 API"；授权判定在业务热路径上，阈值必须更宽 | 高 |
| **`SetTrustedProxies`** | 新增 `TRUSTED_PROXIES` 配置，**默认为空 = 不信任任何代理**；启动日志打印生效列表 | 现状是 Gin 默认信任全部代理，`ClientIP()` 可被 `X-Forwarded-For` 伪造，而该值同时用于限流键与审计 `ip_address` | 中 |
| 默认值的坑 | 若部署在反代后却未配置 `TRUSTED_PROXIES`，全部请求会共享同一个 IP 限流桶 → 集体 429。**生产环境且列表为空时启动告警 + README 显著说明** | 宁可吵一点，也不要静默把所有用户限死 | 高 |
| **监控方案** | `prometheus/client_golang` + `/metrics` 端点（`METRICS_ENABLED` 默认开、`METRICS_PATH` 默认 `/metrics`） | 手写 Prometheus 文本格式属于"重新发明轮子"，违背本项目既有约定；该库是 Go 生态事实标准且为纯 Go 无 CGO | 中 |
| 指标暴露面 | 与业务同端口，但**必须由边缘网关阻断**；README 与 compose 均显式说明 | 单独起监听端口会让容器探针与编排复杂化，收益不匹配 | 中 |
| **备份恢复验证** | 脚本（`pg_dump --format=custom` / `pg_restore`）+ **在 CI 中跑真实演练的集成测试** | TDD §16 要求"经过验证"而非"有文档"；演练测试是最强证据 | 中 |
| 演练测试的降级路径 | 若 testcontainers 的 `Exec` 在此环境不可用，退化为"仅交付脚本 + 手动演练清单"，并把该测试标记为可跳过而非删掉 | 避免为一个测试引入脆弱性 | 高 |
| **压测定位** | 两层：`-bench` 微基准验证 Casbin 裸判定（对应 10ms 目标）；`IAM_LOAD_TEST=1` 门控的 HTTP 负载测试验证端到端（对应 200ms 目标） | 两个目标在 TDD §12.1 中本就是分开定义的；benchmark 进 CI 防劣化，重量级负载测试留作按需执行 | 高 |
| 压测结论的口径 | README 中把结果标注为"在 <环境/规格> 上测得，目标部署环境需复测" | PRD 风险表明确警告"性能与并发指标被误读为已达成承诺" | 低 |
| **多实例一致性** | 不引入 Redis watcher；用"双 Authorizer 实例共享一个库"的测试测出实际传播窗口并写入文档 | 版本号 + 轮询机制 M4 已实现，本里程碑的职责是**验证并量化**它，回答 PRD Open Question | 高 |
| **真实 ZITADEL 令牌测试** | 无法在 CI 自动化（需要可用实例）→ 提供手动核对清单 + 校验脚本，README 标注为上线前人工门禁 | 不假装能自动化一个依赖外部实例的验证 | 高 |
| 漏洞扫描 | 接入 `govulncheck`（消除 ci.yml:48 的 TODO）；静态分析沿用 `go vet` | TDD §13.3 的"依赖漏洞扫描与静态分析"分别由两者满足，不额外引入 staticcheck 工具链 | 中 |
| 发布流程 | 新增 tag 触发的 `release.yml`：多架构镜像推 GHCR + 发布说明；配 `docs/release.md` 运行手册（含回滚） | TDD §13.3 第 9 步"生产部署与健康检查" | 中 |

## Files to Change

| File | Action | Why |
| --- | --- | --- |
| `internal/ports/rate_limit.go` | CREATE | `RateLimiter` 抽象 |
| `internal/adapters/ratelimit/memory.go` (+test) | CREATE | 进程内令牌桶实现 |
| `internal/ports/metrics.go` | CREATE | `Recorder` 抽象，避免传输层直接依赖 Prometheus |
| `internal/adapters/metrics/prometheus.go` (+test) | CREATE | Prometheus 指标注册与 exposition |
| `internal/transport/http/middleware/rate_limit.go` (+test) | CREATE | 限流守卫 |
| `internal/transport/http/middleware/metrics.go` (+test) | CREATE | 请求计数/延迟采集 |
| `internal/transport/http/handlers/metrics.go` | CREATE | `/metrics` 处理器 |
| `internal/transport/http/router.go` | UPDATE | 挂载限流/指标中间件与 `/metrics` 路由；`SetTrustedProxies` |
| `internal/transport/http/responses/response.go` | UPDATE | 新增 `CodeRateLimited` |
| `internal/transport/http/handlers/errors.go` | UPDATE | 429 映射 |
| `internal/bootstrap/metrics.go` | CREATE | 装配指标与限流 |
| `internal/bootstrap/app.go` | UPDATE | `App` 增字段、`New` 接线、`Check` 增补 |
| `internal/config/config.go` (+test) | UPDATE | `RATE_LIMIT_*`、`METRICS_*`、`TRUSTED_PROXIES` |
| `tests/integration/tenancy_stack_test.go` | UPDATE | 栈装配新依赖 |
| `tests/integration/security_test.go` | CREATE | 限流、请求头伪造、错误不泄漏、一致性窗口 |
| `tests/integration/backup_restore_test.go` | CREATE | pg_dump/pg_restore 演练 |
| `tests/load/load_test.go` | CREATE | 门控的 HTTP 负载测试 |
| `internal/adapters/casbin/authorizer_bench_test.go` | CREATE | 裸授权判定微基准 |
| `tests/integration/bootstrap_test.go` | UPDATE | 暴露 postgres 容器句柄供演练测试使用 |
| `api/openapi.yaml` | UPDATE | 登记 `/metrics` 与 429 响应 |
| `deployments/docker/docker-compose.yml` | CREATE | 生产编排：traefik、资源限制、探针、重启策略 |
| `deployments/scripts/{backup,restore,drill}.sh` | CREATE | 备份恢复脚本 |
| `deployments/scripts/verify-token.sh` | CREATE | 真实令牌人工核对 |
| `.github/workflows/ci.yml` | UPDATE | govulncheck、Casbin 策略测试、迁移校验 |
| `.github/workflows/release.yml` | CREATE | tag 触发的镜像发布 |
| `docs/release.md` | CREATE | 发布与回滚运行手册 |
| `docs/performance.md` | CREATE | 压测方法与结果记录模板 |
| `README.md` / `.env.example` / `Makefile` | UPDATE | 新配置项、演练与压测说明、新 target |

## NOT Building

- **Redis 分布式限流与策略 watcher** —— Redis 未部署；版本号 + 轮询的多实例失效已由 M4 实现，本里程碑只做验证与量化
- **OpenTelemetry 分布式追踪** —— 指标已覆盖容量与告警需求，追踪需先有采集端
- **日志聚合（Loki/ELK）与告警规则（PrometheusRule）** —— 本里程碑只暴露指标；告警阈值依赖目标环境的容量基线，由运维侧决定
- **容量规划结论** —— 压测只在当前环境测得数据，"单实例支撑多少租户"需在目标部署环境复测
- **业务服务接入 SDK** —— PRD Open Question"远程 API 还是共享 SDK"未决
- **多区域部署、数据 residency** —— PRD Out of scope
- **真实 ZITADEL 实例的自动化 E2E** —— 无可用实例，降级为人工门禁

---

## Tasks

### Task 1: 可信代理配置（先做：限流与审计都依赖它）
- **Action**: `TRUSTED_PROXIES` 配置 + `router.go` 调用 `SetTrustedProxies`
- **Implement**:
  - `config.TrustedProxies []string`，env `TRUSTED_PROXIES`（逗号分隔 CIDR/IP）
  - 解析用 `net.ParseCIDR`/`netip.ParseAddr`，非法条目在启动时报错并指明是第几项
  - `NewRouter` 入口调用 `r.SetTrustedProxies(cfg.TrustedProxies)`；空切片 = 不信任任何代理
  - 生产环境且列表为空时启动 `slog.Warn`：说明后果是"全部请求共享同一 IP 限流桶 + 审计 IP 不可信"
  - 启动 INFO 打印生效的代理列表，便于排障
- **Mirror**: Bootstrap（`internal/bootstrap/app.go:78` 的启动期校验与日志）
- **Gotcha**:
  - `router.go` 目前只接收 `*bootstrap.App`，需要把配置带下去——优先从 `App` 暴露（`App.Config`），避免改 `NewRouter` 签名牵连全部测试
  - 顺序：必须在任何读取 `ClientIP()` 的中间件之前设置
- **Validate**: `go test ./internal/config/...`；单测覆盖非法 CIDR、空值、生产环境告警

### Task 2: 限流端口、实现与中间件
- **Action**: `internal/ports/rate_limit.go`、`internal/adapters/ratelimit/memory.go`、`middleware/rate_limit.go`
- **Implement**:
  ```go
  type Decision struct {
      Allowed    bool
      RetryAfter time.Duration
  }
  type RateLimiter interface {
      // Take 尝试消耗一个令牌；实现必须是并发安全的。
      Take(ctx context.Context, key string) (Decision, error)
  }
  ```
  - 实现基于 `golang.org/x/time/rate`，每 key 一个 `*rate.Limiter`，`rate.Every(interval)` + `burst`；带 key 驱逐（`sync.Map` + 定期清理，避免无限增长）
  - `RequireRateLimit(limiter, keyFn func(*gin.Context) string, rps float64, burst int)`
    - 拒绝 → `429` + `Retry-After` 秒数 + `CodeRateLimited` + `slog.WarnContext`（含 key、request_id）
    - `Take` 返回 error → **放行**并 `slog.ErrorContext`（见决策表）
    - 必须 `c.Abort()`
  - keyFn：已认证取 `subject:<UserID>`，未认证取 `ip:<ClientIP>`
- **Mirror**: Transport（`internal/transport/http/middleware/require_permission.go:33`）
- **Gotcha**:
  - key 驱逐周期必须长于限流窗口，否则会出现"清理后瞬间放行一批"的抖动
  - `/authorize` 与管理类必须用**不同的 rps**，否则要么授权判定被误限，要么管理接口形同不限
  - 429 响应体必须走 `responses.Fail` 统一信封，不能手写
- **Validate**: 单测覆盖放行、限流、并发安全（`-race`）、key 驱逐、故障放行；集成测试验证 429 与 `Retry-After`

### Task 3: 指标端口、实现与中间件
- **Action**: `internal/ports/metrics.go`、`internal/adapters/metrics/prometheus.go`、`middleware/metrics.go`、`handlers/metrics.go`
- **Implement**:
  - `ports.MetricsRecorder` 窄接口：`ObserveRequest(method, route string, status int, d time.Duration)`、`IncRateLimited(route string)`、`IncDecision(result string)`、`SetPolicyVersion(int64)`、`IncPolicyReload(result string)`
  - Prometheus 实现（`prometheus/client_golang`）：
    - `iam_http_requests_total{method,route,status}` counter
    - `iam_http_request_duration_seconds{method,route}` histogram（ buckets 覆盖 1ms–1s）
    - `iam_rate_limited_total{route}` counter
    - `iam_authorization_decisions_total{result}` counter
    - `iam_policy_reloads_total{result}` counter、`iam_policy_version` gauge
  - `middleware/metrics.go`：在 `RequestID`/`Logging` 之后采集，`route` 用 `c.FullPath()`（**不是** `c.Request.URL.Path`，否则高基数标签会打爆 Prometheus）
  - `/metrics` 路由挂在认证组**之外**（与 `/health` 同级）
  - 启动期校验 `METRICS_PATH` 不与任何业务路由冲突
- **Mirror**: Naming（`internal/adapters/casbin/authorizer.go:26-37` 的启动期依赖校验）
- **Gotcha**:
  - 标签基数：`route` 必须是路由模板；`status` 只取三位状态码；**绝不**把 tenant_id/user_id 放进标签
  - `/metrics` 必须登记进 `api/openapi.yaml`，否则 `TestOpenAPICoversEveryRoute` 会失败（这是好事）
  - `METRICS_PATH` 为空字符串应视为关闭而非注册到 `/`
- **Validate**: 单测覆盖计数器/直方图写入；集成测试抓取 `/metrics` 断言关键指标存在且标签正确

### Task 4: 配置与装配
- **Action**: `internal/config/config.go`、`internal/bootstrap/metrics.go`、`internal/bootstrap/app.go`
- **Implement**:
  - `RATE_LIMIT_ENABLED`（默认 `true`）、`RATE_LIMIT_MANAGE_RPS`（默认 `10`）、`RATE_LIMIT_AUTHORIZE_RPS`（默认 `100`）、`RATE_LIMIT_BURST`（默认 `2`）
  - `METRICS_ENABLED`（默认 `true`）、`METRICS_PATH`（默认 `/metrics`）
  - `TRUSTED_PROXIES`（默认空）
  - 校验：RPS/BURST 必须为正；`METRICS_PATH` 必须以 `/` 开头；`TRUSTED_PROXIES` 条目必须可解析
  - `bootstrap/metrics.go`：`NewRateLimiter(cfg)`、`NewMetricsRecorder(cfg)`、`NewMetricsHandler(rec)`
  - `App` 增 `RateLimiter`、`Metrics`、`Config`；`Check` 增补"指标已启用"与"限流器已装配"两项（均为廉价检查，但保证装配遗漏在就绪检查暴露）
- **Mirror**: Naming（`internal/config/config.go:37-38,45-53`）、Bootstrap（`internal/bootstrap/app.go:167`）
- **Gotcha**: 默认值必须与 `.env.example`、README、compose 三处一致——不一致是本项目最容易出现的文档腐化点
- **Validate**: `go test ./internal/config/... ./internal/bootstrap/...`；启动期非法配置快速失败

### Task 5: 安全测试补强
- **Action**: `tests/integration/security_test.go`
- **Implement**:
  - **限流**：连续打爆管理接口 → 429 + `Retry-After`；随后放行恢复；**已认证用户之间互不受累**（不同 subject 不同桶）；未认证按 IP 限流
  - **请求头伪造**：携带 `X-User-ID`、`X-Tenant-ID`、伪造 roles claim 的请求头 → 身份与权限判定完全不受影响（TDD §10.3）
  - **错误不泄漏**：数据库断开时的 503/500 响应体与日志中**不含** DSN、SQL、堆栈；`/authorize` 在引擎故障时不返回 `allowed=true`
  - **多实例一致性窗口**：两个 `Authorizer` 实例共享一个库（一个由栈持有，一个新建），把 `interval` 设为 100ms；在实例 A 上撤销权限，轮询实例 B 直到判定翻转，**断言实际耗时并写入文档**——这就是 PRD Open Question"多实例策略一致性的可接受窗口"的答案
  - **撤权时效**：成员移除 / 用户禁用后，用带旧令牌的请求立即验证被拒（已有用例，此处汇总为一张核对表）
- **Mirror**: Tests（`tests/integration/rbac_routes_test.go:42`、`rbac_failclosed_test.go:15`）
- **Gotcha**:
  - 一致性窗口测试**不能**断言"零延迟"——轮询机制决定了下界是刷新间隔；断言"≤ 2×interval + 重载耗时"并记录实测值
  - 限流测试必须用小 rps（如 1）且用例独立，避免拖慢整个集成套件
  - 伪造 roles claim 需桩支持签发带 `urn:zitadel:iam:user:roles` 的令牌，同时另发一个"仅请求头声明角色"的令牌做对照
- **Validate**: `go test ./tests/integration/... -run TestSecurity -count=1` 全绿

### Task 6: 备份恢复脚本与 CI 演练
- **Action**: `deployments/scripts/{backup,restore,drill}.sh`、`tests/integration/backup_restore_test.go`
- **Implement**:
  - `backup.sh <output-dir>`：`pg_dump --format=custom --no-owner`，输出 `iam-<UTC时间戳>.dump`，保留最近 N 份
  - `restore.sh <dump> <target-dsn>`：`pg_restore --clean --if-exists --no-owner`，**先校验目标库的 schema_migrations 版本不高于备份**，避免把新结构还原到旧库
  - `drill.sh`：备份 →  drop/重建 → 还原 → 校验版本与行数，输出 PASS/FAIL
  - 集成测试：`newDatabase` + `migrateUp` + 直接 SQL 播种（users/tenants/memberships/roles/permissions/casbin_rule/audit_logs 各若干行）→ 容器内 `pg_dump` → 建新库 `pg_restore` → **逐表比对行数**并断言 `schema_migrations` 版本一致
  - `bootstrap_test.go` 暴露 postgres 容器句柄（`TestMain` 中已持有，加一个 `execInPostgres(t, args...)` 助手）
- **Mirror**: Tests（`tests/integration/bootstrap_test.go:132,294` 的 `newDatabase`/`migrateUp`）
- **Gotcha**:
  - `pg_restore --clean` 会先 DROP，**绝不允许**指向生产库——脚本内加显式确认与 DSN 前缀检查
  - 备份文件含全量业务数据，脚本输出路径必须可配置且不得进 git；`.gitignore` 补一条
  - 若 testcontainers `Exec` 在此环境不可用，测试降级为 `t.Skip` 并打印原因，**不删除**——脚本与手动演练清单仍是交付物
- **Validate**: `go test ./tests/integration/... -run TestBackupRestore -count=1`；本地手动跑一次 `drill.sh`

### Task 7: 压测与性能记录
- **Action**: `internal/adapters/casbin/authorizer_bench_test.go`、`tests/load/load_test.go`、`docs/performance.md`
- **Implement**:
  - 微基准：`BenchmarkEnforce_Hit` / `BenchmarkEnforce_Miss` / `BenchmarkEnforce_CrossTenant`，用 `b.ReportMetric` 输出均值；把裸判定 P95 与 10ms 目标对照
  - 负载测试（`IAM_LOAD_TEST=1` 才运行，`-count=1`）：
    - 预置 1 个租户 + owner + viewer
    - 场景 A：`POST /api/v1/authorize` 100 并发 × 固定请求数 → 统计 P50/P95/P99，对照 200ms
    - 场景 B：`GET /api/v1/tenants/:id/members` 100 并发 → 同上
    - 输出 Markdown 表格到 stdout，并写明"目标部署环境需复测"
  - `docs/performance.md`：测量方法、环境规格（CPU/内存/DB 位置）、结果记录表、复现命令
- **Mirror**: Tests（`tests/integration/tenancy_stack_test.go:43`）
- **Gotcha**:
  - 负载测试**绝不能**进常规 CI：它会显著拉长流水线且结果受 Runner 负载影响而无意义
  - 100 并发必须用带缓冲的 worker 池 + `sync.WaitGroup`，不能用"开 100 个 goroutine 各自跑完全部请求"（那会叠加成 100×N 并发）
  - P95 的计算要排除连接建立与首批预热，否则数字虚高
- **Validate**: `go test ./tests/load/... -count=1`（未设门控时应 skip）；`IAM_LOAD_TEST=1 go test ./tests/load/... -count=1 -v` 输出报告

### Task 8: CI/CD 与部署配置
- **Action**: `.github/workflows/ci.yml`、`.github/workflows/release.yml`、`deployments/docker/docker-compose.yml`、`docs/release.md`
- **Implement**:
  - CI 增补（对齐 TDD §13.3）：
    1. `govulncheck ./...`（替换 ci.yml:48 的 TODO）
    2. Casbin 策略测试独立步骤（`go test ./internal/... -run TestEnforcer -count=1` 或专门的策略测试包）
    3. 迁移校验：`migrate up` 后 `migrate version` 断言非 dirty
    4. 备份恢复演练（`-run TestBackupRestore`）
  - `release.yml`（`on: push: tags: ["v*"]`）：多架构构建推 GHCR + 生成发布说明 + 附镜像 digest
  - `deployments/docker/docker-compose.yml`：traefik（含 `/metrics` 与 `/health` 的路由规则，**`/metrics` 不对外暴露**）、api 资源限制、`healthcheck` 指向 `/health/ready`、`restart: unless-stopped`、日志轮转、`TRUSTED_PROXIES` 显式配置
  - `docs/release.md`：发布前检查 → 迁移 → 滚动部署 → 冒烟 → 观察指标 → 回滚步骤（镜像 tag 回退 + 迁移不回滚的理由说明）
- **Mirror**: CI（`.github/workflows/ci.yml:18-46`）
- **Gotcha**:
  - `govulncheck` 需要网络访问漏洞库，CI 失败时要能区分"真的 vulnerable"与"网络问题"——用 `continue-on-error` 不合适，改为单独 job 并在日志中说明
  - compose 中 `/metrics` 的阻断必须靠 traefik 规则显式实现，不能只靠 README 一句话
  - 迁移在发布流程中是**独立、可审计的一步**（TDD §13.3），不能塞进 api 容器的 entrypoint
- **Validate**: CI 全绿；本地 `docker compose -f deployments/docker/docker-compose.yml config` 通过

### Task 9: 文档与口径统一
- **Action**: `README.md`、`.env.example`、`Makefile`、`api/openapi.yaml`
- **Implement**:
  - `.env.example`：新增 6 个配置项，每个都写清默认值与"生产环境必须显式设置"的标记
  - `Makefile`：`bench`、`load-test`、`backup`、`restore`、`drill`、`vulncheck` target
  - `api/openapi.yaml`：登记 `/metrics`；为管理接口补 `429` 响应
  - README：新增"运行时可观测性"（指标清单与抓取方式）、"限流"（阈值、键、故障语义）、"备份与恢复"（演练命令）、"生产部署"（compose + `TRUSTED_PROXIES` 必配 + `/metrics` 必须阻断）、"上线前人工门禁"（真实 ZITADEL 令牌核对清单）
  - README 顶部状态行更新到 Milestone 6
- **Mirror**: Naming（`internal/config/config.go:79-80` 的"仅解析不使用"注释风格）
- **Validate**: `go test ./tests/integration/... -run TestOpenAPI -count=1`；人工核对三处默认值一致

---

## Validation

```bash
# 静态与构建
gofmt -l .                 # EXPECT: 无输出
go vet ./...               # EXPECT: 无输出
go build ./...             # EXPECT: exit 0

# 单元测试（不依赖 Docker）
make test                  # go test -short -race ./...   EXPECT: 全部 PASS

# 集成与安全回归（需 Docker）
make test-integration      # EXPECT: PASS
go test ./tests/integration/... -count=1 -race   # EXPECT: 无 data race

# 备份恢复演练（需 Docker）
go test ./tests/integration/... -run TestBackupRestore -count=1 -v
# EXPECT: PASS，且日志显示逐表行数比对一致

# 微基准（进 CI，防劣化）
make bench
# EXPECT: 输出 BenchmarkEnforce_* 的 ns/op，与 docs/performance.md 的记录对照

# 负载测试（默认不跑；需 Docker）
IAM_LOAD_TEST=1 go test ./tests/load/... -count=1 -v
# EXPECT: 输出 authorize 与管理接口的 P50/P95/P99 表格

# 漏洞扫描
make vulncheck             # go install golang.org/x/vuln/cmd/govulncheck@latest && govulncheck ./...
# EXPECT: 无已知漏洞

# 部署配置
docker compose -f deployments/docker/docker-compose.yml config   # EXPECT: 合法
# EXPECT: traefik 规则中不存在把 /metrics 暴露到外部的 router

# 冒烟：限流与指标
for i in $(seq 1 30); do curl -s -o /dev/null -w "%{http_code}\n" \
  -H "Authorization: Bearer $TOKEN" localhost:8080/api/v1/tenants/$TENANT_ID/members; done
# EXPECT: 前若干个 200，随后出现 429，且响应体含 RATE_LIMITED 与 Retry-After 头
curl -s localhost:8080/metrics | grep iam_http_requests_total
# EXPECT: 指标存在，标签为 method/route/status（无 tenant_id、无 user_id）
```

## Risks

| Risk | Likelihood | Mitigation |
| --- | --- | --- |
| **`TRUSTED_PROXIES` 未配置导致全部用户共享一个 IP 限流桶** | **高** | 生产环境且列表为空时启动告警；README 与 compose 显著标注；默认值文档化 |
| 限流故障放行被误读为"又一处自动放行" | 中 | 决策表与 README 明确区分"可用性保护"与"授权判定"；代码注释写明理由 |
| `/metrics` 与业务同端口导致信息泄漏 | 中 | compose 中由 traefik 规则显式阻断；README 标注为内部端点；指标标签不含租户/用户标识 |
| 压测结果被当成生产容量承诺 | 中 | README 与 `docs/performance.md` 统一标注"在 <环境> 测得，目标环境需复测"（PRD 风险表明确要求） |
| 负载测试进 CI 导致流水线劣化或结果无意义 | 中 | `IAM_LOAD_TEST=1` 门控；CI 只跑微基准 |
| 多实例一致性窗口超出可接受值 | 中 | Task 5 实测并记录；若超出预期，再评估引入 Redis watcher（当前明确不做） |
| testcontainers `Exec` 不可用导致演练测试失效 | 中 | 降级为 `t.Skip` + 保留脚本与手动演练清单，不删测试 |
| `pg_restore --clean` 误指向生产库 | 低 | 脚本内 DSN 前缀检查 + 显式确认；README 警告 |
| 备份文件含全量业务数据被误提交 | 中 | `.gitignore` 补规则；脚本输出目录可配置 |
| 新增 3 个依赖扩大供应链面 | 中 | `go mod tidy` 后锁定版本；`govulncheck` 纳入 CI；每个依赖都在决策表中说明为何不自研 |
| 配置项三处（代码/`.env.example`/compose）不一致 | 中 | Task 9 专人核对；`.env.example` 作为唯一人类可读来源 |
| 真实 ZITADEL 令牌验证仍无法自动化 | 中 | 降级为上线前人工门禁清单 + 校验脚本；不假装覆盖 |

## Acceptance

- [ ] `gofmt -l .` 无输出、`go vet ./...` 无错误、`go build ./...` 通过
- [ ] `go test -short -race ./...` 全绿；`go test ./tests/integration/... -count=1` 全绿
- [ ] `TRUSTED_PROXIES` 可配置、启动期校验、生产环境空值时告警；`ClientIP()` 不再无条件可信
- [ ] 管理 API 与授权 API 分别限流，超限返回 `429` + `Retry-After` + 统一错误信封；不同 subject 互不受累
- [ ] 限流器故障时放行并记 ERROR 日志，且该语义在代码注释与 README 中明确区别于授权的 fail closed
- [ ] `/metrics` 暴露 HTTP 延迟/计数、限流计数、授权判定计数、策略版本与重载结果；标签不含租户/用户标识
- [ ] `/metrics` 已在 `api/openapi.yaml` 登记，且部署配置中由边缘网关阻断
- [ ] 备份与恢复脚本可用，且 CI 中跑通一次真实演练（逐表行数与迁移版本一致）
- [ ] `make bench` 进 CI；`docs/performance.md` 记录了裸授权判定实测值并与 10ms 目标对照
- [ ] 负载测试可复现，输出 authorize 与管理接口的 P50/P95/P99，并与 200ms 目标对照
- [ ] 多实例策略一致性窗口已实测并写入文档（回答 PRD Open Question）
- [ ] CI 含 `govulncheck`、Casbin 策略测试、迁移校验；tag 触发镜像发布流水线
- [ ] 安全测试覆盖：限流行为、请求头伪造不影响身份与权限、错误响应不泄漏内部细节
- [ ] README 更新配置项、限流、指标、备份恢复、生产部署与上线前人工门禁
- [ ] 未越界实现 NOT Building 中的任何一项（尤其：未引入 Redis）
- [ ] Patterns mirrored, not reinvented：沿用既有命名、错误、装配与测试约定

---

**WAITING FOR CONFIRMATION**：是否按此计划实施？可回复 `yes` / `modify: <改动>` / `different approach: <方案>`。
