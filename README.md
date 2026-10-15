# IAM Service

基于 ZITADEL 的多租户用户管理与授权微服务。当前已完成 **Milestone 1–6 全部六个里程碑**：

服务可启动、可执行迁移、可校验 JWT 访问令牌并按需同步本地用户；支持平台管理员创建租户、
"先认证、后绑定"的成员邀请、租户内角色与权限配置、统一授权判定
（`POST /api/v1/authorize`）；高风险管理操作全部落库留痕；具备限流、Prometheus 指标、
备份恢复演练与完整的 CI/CD 发布流水线。

性能数字见 [`docs/performance.md`](docs/performance.md)——那是**开发机实测值**，
不是生产容量承诺，目标环境需复测。

## 技术栈

Go · Gin · GORM · PostgreSQL · Casbin（RBAC + Tenant Domain）· golang-migrate ·
Prometheus（指标）· golang.org/x/time（限流）

## 快速开始

```bash
# 1. 准备配置
cp .env.example .env
# 至少修改 DATABASE_DSN

# 2. 启动 PostgreSQL（需要 Docker）
docker compose up -d postgres

# 3. 执行数据库迁移（独立步骤，服务启动不会自动执行）
make migrate-up

# 4. 启动服务
go run ./cmd/api
```

验证：

```bash
curl -i http://localhost:8080/health/live    # 恒返回 200（不需要认证）
curl -i http://localhost:8080/health/ready   # 数据库与迁移就绪时返回 200，否则 503
curl -i http://localhost:8080/api/v1/me      # 无令牌时返回 401
curl -i -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/me
```

> `/api/v1/me` 需要真实的 ZITADEL 令牌。本地冒烟可指向一个自建的 OIDC/JWKS 桩服务，
> 也可以直接运行集成测试（它们内置了桩服务）。

## 环境变量

| 变量 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `DATABASE_DSN` | 是 | — | PostgreSQL 连接串 |
| `ZITADEL_ISSUER` | 是 | — | OIDC Issuer，生产环境必须是 `https`，尾斜杠自动去除 |
| `ZITADEL_API_AUDIENCE` | 是 | — | 预期受众，必须与令牌中的 `aud` 匹配，不能猜测 |
| `APP_ENV` | 否 | `dev` | `dev` 或 `production`，决定 Gin 模式与日志格式 |
| `APP_HTTP_ADDR` | 否 | `:8080` | HTTP 监听地址 |
| `APP_SHUTDOWN_TIMEOUT` | 否 | `10s` | 优雅关闭宽限期 |
| `REDIS_ADDR` | 否 | — | 可选缓存地址，当前仅解析不使用 |
| `ZITADEL_CLIENT_ID` | 否 | — | 客户端标识，当前仅解析 |
| `ZITADEL_SERVICE_CREDENTIALS_FILE` | 否 | — | 访问 ZITADEL 管理 API 的服务身份，当前不使用 |
| `AUTH_TOKEN_MODE` | 否 | `jwt` | 仅支持 `jwt`；`introspect` 会显式报错（尚未实现） |
| `AUTH_JWKS_CACHE_TTL` | 否 | `15m` | **仅解析不生效**，密钥缓存与轮换由 go-oidc 内部策略决定 |
| `PLATFORM_ADMIN_ROLE_KEY` | 否 | `platform_admin` | 平台管理员对应的 ZITADEL 角色名，置空则启动失败 |
| `INVITATION_TTL` | 否 | `168h`（7 天） | 成员邀请有效期 |
| `POLICY_REFRESH_INTERVAL` | 否 | `5s` | 跨实例策略失效检查间隔，必须为正；见下方"撤权时效" |
| `DEFAULT_MEMBER_ROLE_KEY` | 否 | `member` | 非首个成员加入租户时绑定的默认角色，必须是系统预置角色之一 |
| `CASBIN_MODEL_PATH` | 否 | `./casbin/model.conf` | Casbin 模型文件 |
| `LOG_LEVEL` | 否 | `info` | `debug` / `info` / `warn` / `error` |
| `TRUSTED_PROXIES` | 否 | 空（不信任任何代理） | 可信代理 CIDR/IP 列表；**部署在反代后必须配置**，见下方"可信代理" |
| `RATE_LIMIT_ENABLED` | 否 | `true` | 是否启用限流 |
| `RATE_LIMIT_MANAGE_RPS` | 否 | `10` | 管理类接口每秒请求数上限 |
| `RATE_LIMIT_AUTHORIZE_RPS` | 否 | `100` | 授权判定接口每秒请求数上限（在业务热路径上，必须更宽） |
| `RATE_LIMIT_BURST` | 否 | `2` | 令牌桶容量倍率，`burst = rps × 该值` |
| `METRICS_ENABLED` | 否 | `true` | 是否暴露 `/metrics` |
| `METRICS_PATH` | 否 | `/metrics` | 指标端点路径 |

缺少必要配置或取值非法时，进程会**立即退出**并在日志中列出全部问题字段。

## API 文档

完整接口契约见 [`api/openapi.yaml`](api/openapi.yaml)（OpenAPI 3.0）。

文档由测试守护：`tests/integration/openapi_test.go` 会遍历已注册路由，
双向校验"每个路由都在文档中、文档中没有幽灵端点"。新增或删除路由却忘记更新文档时，CI 会直接失败。

## 数据库迁移

迁移是**独立、可审计的步骤**，`cmd/api` 启动只校验迁移状态（版本非 0 且非 dirty），**不会执行迁移**，
以避免多实例并发修改表结构。

```bash
make migrate-up        # 应用全部待执行迁移
make migrate-down      # 回滚全部迁移
make migrate-version   # 查看当前版本与 dirty 状态

# 迁移中断导致 dirty 时，人工确认数据状态后强制修正版本号：
go run ./cmd/migrate -cmd force -steps 1
```

迁移文件位于 `migrations/`，通过 `go:embed` 编入 `iam-migrate` 二进制。

## 测试

```bash
make test              # 单元测试，不需要外部服务
make test-integration  # 集成测试，需要可用的 Docker 守护进程
make ci                # 格式 + 静态检查 + 单测 + 构建
make bench             # 授权判定微基准（进 CI，防劣化）
make vulncheck         # 依赖漏洞扫描
```

集成测试使用 testcontainers 启动 PostgreSQL 16；当使用 `-short` 或 Docker 不可用时会自动跳过，
不会让本地构建失败。集成测试同时覆盖：真实数据库约束、事务一致性、跨租户隔离、
OpenAPI 与路由的双向覆盖、备份恢复演练。

性能测量见 [`docs/performance.md`](docs/performance.md)。

## 容器部署

```bash
make up    # docker compose up -d --build（postgres → migrate → api）
make down
```

`docker-compose.yml` 中的 `migrate` 服务以 `restart: "no"` 运行一次，
`api` 服务等待其成功完成后才启动。

生产形态编排见 `deployments/docker/docker-compose.yml`：

```bash
docker compose -f deployments/docker/docker-compose.yml up -d
```

与开发版的区别：增加 traefik 边缘网关（并显式阻断 `/metrics` 对外暴露）、
资源限制与重启策略、健康检查指向 `/health/ready`、多副本部署。
发布与回滚步骤见 [`docs/release.md`](docs/release.md)。

## 运行期加固

### 限流

对管理 API 与授权 API 分别限流（TDD §10.4），阈值为
`RATE_LIMIT_MANAGE_RPS`（默认 10）与 `RATE_LIMIT_AUTHORIZE_RPS`（默认 100）。

- 限流键：已认证按 `subject:<用户ID>`，未认证按 `ip:<客户端IP>`
- 超限返回 `429` + `Retry-After` 头 + 统一错误信封（错误码 `RATE_LIMITED`）
- **故障语义**：限流器自身故障时**放行**并记录 ERROR 日志。限流是可用性保护而非授权判定，
  因保护组件故障而拒绝全部请求比放过请求更糟——这与授权判定的 fail closed 是两类不同风险。
- 多实例部署时每个实例各自限流，累计容量随实例数线性增长；分布式限流需引入 Redis，尚未实现。

### 可信代理（**部署在反代后必须配置**）

`TRUSTED_PROXIES` 决定 `ClientIP()` 的可信度，而该值同时用于：

- 未认证请求的限流键（按 IP 分桶）
- 审计记录的 `ip_address`

空值表示不信任任何代理，此时 `ClientIP()` 只取自直接连接对端。
**若部署在反代后却不配置它，全部请求会共享同一个 IP 限流桶，导致所有用户被连带限流**，
且审计 IP 不可信。生产环境必须设置为边缘网关所在网络的 CIDR。

同理，`ClientIP()` 可被 `X-Forwarded-For` 伪造，因此审计中的 IP 仅作辅助线索，
**不作为任何授权依据**。

### 监控

`/metrics` 暴露 Prometheus 指标（`METRICS_ENABLED`、`METRICS_PATH` 可配）：

| 指标 | 用途 |
| --- | --- |
| `iam_http_requests_total{method,route,status}` | 请求量与错误率 |
| `iam_http_request_duration_seconds{method,route}` | 延迟分布 |
| `iam_rate_limited_total{route}` | 限流触发情况（异常升高可能说明阈值过紧或代理配置错误） |
| `iam_authorization_decisions_total{result}` | 授权判定分布（allowed / denied / error） |
| `iam_policy_version` | 本地策略版本，观察跨实例失效是否及时 |
| `iam_policy_reloads_total{result}` | 策略重载成功/失败次数 |

`route` 标签使用路由模板而非具体路径；**标签中不含 tenant_id 与 user_id**，
避免同时造成标签基数爆炸与租户维度泄漏。

> `/metrics` 是内部端点，**必须由边缘网关阻断，不对公网暴露**。
> 生产 compose 中已通过 traefik 路由规则实现。

### 备份与恢复

```bash
make backup    # pg_dump 逻辑备份到 ./backups
make drill     # 演练：备份 → 还原到独立库 → 校验迁移版本与关键表行数
make restore   # 从备份还原（会 DROP 目标库对象，务必二次确认）
```

备份恢复**不是"配了就是可用"**：CI 中包含 `TestBackupRestoreDrill`
（真实 pg_dump / pg_restore + 逐表行数比对），生产环境建议按季度执行一次 `make drill`。

### 上线前人工门禁

CI 中没有可用的 ZITADEL 实例，因此"真实令牌验证"是人工步骤：

```bash
IAM_BASE_URL=https://iam.example.com \
ZITADEL_TOKEN=<真实访问令牌> \
PLATFORM_ADMIN_TOKEN=<真实平台管理员令牌> \
  ./deployments/scripts/verify-token.sh
```

它会核对无令牌 401、有效令牌 200、篡改令牌 401，并打印需人工确认的 claim 清单。

## 关键约定

- **统一响应格式**：成功 `{"data":..., "request_id":...}`；失败 `{"error":{"code","message"},"request_id":...}`。
- **request_id**：每个响应都携带，响应头 `X-Request-ID` 与响应体 `request_id` 一致；客户端提供的非法值（超长、含换行）会被丢弃并重新生成。
- **健康检查**：`/health/live` 不触碰任何依赖，`/health/ready` 真实校验数据库、迁移状态与 Casbin 策略加载。依赖不可用返回 `503`，绝不放行。
- **认证语义**：`401` 缺少/无效凭证，`403` 用户已停用，`503` 身份平台或数据库不可用（fail closed）。身份唯一来源是 `Authorization: Bearer` 中已验证令牌的 `sub`，忽略 `X-User-ID` 之类的请求头。
- **无认证绕过开关**：不存在 `AUTH_ENABLED=false` 之类的开关，测试通过替身而非开关实现。
- **用户按需同步**：首次访问依据 `sub` 创建本地映射；展示资料仅在变化时更新；用户停用后返回 `403`。
- **租户创建**：**仅平台管理员可创建**，判定依据是已验证令牌中的 ZITADEL 角色（`PLATFORM_ADMIN_ROLE_KEY`），**没有白名单或应急通道**。平台管理员创建租户后不会成为其成员，成员关系只能通过邀请建立。
- **成员邀请：先认证、后绑定**：邀请只绑定已认证且 `email_verified=true` 且邮箱匹配的身份；不会因为一个邮箱字符串创建可登录用户。有效期 7 天，重复邀请与重复接受均幂等。
- **租户隔离**：非成员与租户不存在都返回 `404`（防枚举）；租户停用返回 `403 TENANT_SUSPENDED`；成员非 active 返回 `403 MEMBERSHIP_INACTIVE`。租户标识只从路径参数读取，不信任任何请求头。
- **软删除**：成员移除只改状态（`removed`），保留 `(tenant_id, user_id)` 唯一约束，重新邀请走"重新激活"。
- **租户内 RBAC**：授权判定四元组为 `(subject, tenant, resource, action)`，Casbin 模型的 `dom` 即业务租户 ID。**未定义权限一律拒绝**；判定前必须先通过"用户有效 + 租户有效 + 成员关系有效"的数据库校验，Casbin 的 Allow 不能替代租户隔离。
- **权限目录由系统维护**：权限集合随迁移种子（`000006`）变更，租户只能在既有集合内组合自定义角色，**没有新建权限的接口**。
- **系统预置角色**：租户创建时自动预置 `owner`/`admin`/`member`/`viewer`。它们不可删除、不可改 `key`，只能调整展示信息与权限集合；首个接受邀请的成员自动绑定为 `owner`，其余成员按 `DEFAULT_MEMBER_ROLE_KEY` 绑定。
- **管理员保护**：不能移除最后一个有效 Owner，不能删除仍被成员使用的角色，不能通过批量接口绕过单项权限检查。
- **撤权时效（重要）**：
  - 成员移除、成员停用、租户停用 → **立即生效**，因为这些路径每次请求都直查数据库，不依赖缓存；
  - 角色权限矩阵变更 → 本实例提交后立即生效，其他实例最长滞后 `POLICY_REFRESH_INTERVAL`（默认 5s）；
  - 多实例部署当前依赖"策略版本号 + 轮询"，Redis pub/sub watcher 属于后续阶段。
- **策略与业务数据的原子性**：`casbin_rule` 是业务关系的投影，由 `PolicyRepository` 在**同一个事务**内写入；事务回滚后不会残留策略（即不会出现"业务已回滚但授权仍放行"）。
- **审计与业务数据的原子性**：审计记录与业务写入、策略投影在同一事务提交。**审计写失败会使整个高风险操作失败**——"改了权限但没留痕"比"操作失败"更不可接受。
- **审计覆盖范围**：成员移除、成员状态变更、角色分配、角色创建/修改/删除、角色权限变更、成员邀请、租户创建、租户停用与恢复。被拒绝的请求只记结构化日志，不落审计表（避免审计表被越权尝试淹没）。
- **审计表只追加**：没有更新与删除接口，也没有软删除字段——任何修改都会破坏审计记录本身的可信度。`metadata` 只接受标量值，且键名包含 `token`/`password`/`secret` 等片段时整条拒绝。
- **审计归属**：针对具体租户的平台操作（停用/恢复）会带上 `tenant_id`，租户管理员能看到"我的租户为什么被停用"；只有真正与租户无关的平台操作（创建租户）使用空 `tenant_id`，且空值只用于审计归属，不作为任何隐式提权通道。
- **审计查询**：受独立权限 `audit:read` 保护，查询强制限定在当前租户，不接受跨租户参数；非法 `action` 与非法时间区间返回 400（静默忽略会让管理员误以为"没有这类操作"）。
- **分页响应格式**：所有分页列表统一返回 `{"items": [...], "pagination": {"limit", "offset", "total"}}`。
- **`ip_address` 的可信度**：取自 `c.ClientIP()`，Gin 默认信任所有代理，因此该字段**可被伪造**，仅作审计的辅助线索、不作为授权依据。生产环境必须调用 `gin.Engine.SetTrustedProxies` 配置可信代理。

## 接口一览

| 方法 | 路径 | 权限 |
| --- | --- | --- |
| GET | `/health/live`、`/health/ready` | 无需认证 |
| GET | `/api/v1/me` | 已认证 |
| POST | `/api/v1/platform/tenants` | 平台管理员（`platform_admin` 角色） |
| GET | `/api/v1/tenants` | 已认证（返回我所属的租户） |
| GET | `/api/v1/permissions` | 已认证（系统权限目录，只读） |
| POST | `/api/v1/authorize` | 已认证（统一授权判定） |
| GET | `/api/v1/tenants/:tenantID` | 租户成员 + `tenant:read` |
| PATCH | `/api/v1/tenants/:tenantID` | 租户成员 + `tenant:update` |
| GET | `/api/v1/tenants/:tenantID/members` | 租户成员 + `member:read` |
| GET | `/api/v1/tenants/:tenantID/members/:userID` | 租户成员 + `member:read` |
| PATCH | `/api/v1/tenants/:tenantID/members/:userID` | 租户成员 + `member:update` |
| DELETE | `/api/v1/tenants/:tenantID/members/:userID` | 租户成员 + `member:remove` |
| GET | `/api/v1/tenants/:tenantID/members/:userID/roles` | 租户成员 + `role:read` |
| PUT | `/api/v1/tenants/:tenantID/members/:userID/roles` | 租户成员 + `role:assign` |
| POST | `/api/v1/tenants/:tenantID/invitations` | 租户成员 + `member:invite` |
| GET | `/api/v1/tenants/:tenantID/invitations` | 租户成员 + `member:read` |
| DELETE | `/api/v1/tenants/:tenantID/invitations/:invitationID` | 租户成员 + `member:invite` |
| GET/POST | `/api/v1/tenants/:tenantID/roles` | 租户成员 + `role:read` / `role:create` |
| GET/PATCH/DELETE | `/api/v1/tenants/:tenantID/roles/:roleID` | 租户成员 + `role:read` / `role:update` / `role:delete` |
| PUT | `/api/v1/tenants/:tenantID/roles/:roleID/permissions` | 租户成员 + `permission:assign` |
| GET | `/api/v1/tenants/:tenantID/audit-logs` | 租户成员 + `audit:read` |
| PATCH | `/api/v1/platform/tenants/:tenantID/status` | 平台管理员（停用/恢复租户） |
| GET | `/api/v1/invitations/pending` | 已认证（按已验证邮箱过滤） |
| POST | `/api/v1/invitations/:invitationID/accept` | 已认证 |

统一授权判定：

```bash
curl -X POST http://localhost:8080/api/v1/authorize \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"tenant_id":"<uuid>","resource":"member","action":"invite"}'
# 200 {"data":{"allowed":true,...}}
# 业务性拒绝（非成员、无权限、租户停用）返回 200 + allowed=false，避免枚举租户是否存在；
# 依赖不可用返回 503 —— 调用方必须失败关闭，不能把 503 当成"无权限"后自行放行。
```

> 请求体**只能**声明要判断的租户、资源与操作，调用者身份一律来自已验证令牌。

## 已知限制（本阶段）

- `/members` 只返回 `user_id`，尚未提供邮箱/展示名映射，该能力随用户目录接口在后续里程碑补齐。
- **分布式限流未实现**：限流为进程内令牌桶，多实例下每实例各自限流。
  需要全局一致限额时需引入 Redis（当前未部署）。
- **Redis 未部署**：`REDIS_ADDR` 仅解析不使用。跨实例策略失效靠版本号 + 轮询，
  传播上界为一个 `POLICY_REFRESH_INTERVAL`。
- **性能数字为开发机实测值**，非生产容量承诺；目标环境需按 `docs/performance.md` 复测。
- **真实 ZITADEL 令牌验证为人工门禁**：CI 无可用实例，上线前需执行
  `deployments/scripts/verify-token.sh`。
- 租户停用/删除接口与平台管理员管理界面尚未提供，当前只能在数据库侧变更状态。
- 邀请令牌通过创建响应返回，生产环境需改为邮件投递。
- 平台管理与角色/权限变更的审计已落库；审计日志的**导出与归档**尚未实现。
- **默认拒绝**：Casbin 模型中 `dom` 即业务租户 ID，未定义权限一律拒绝。
- **多实例策略失效**：当前使用"策略版本号 + 轮询"，最长滞后 `POLICY_REFRESH_INTERVAL`；Redis pub/sub watcher 属于后续阶段。
- **迁移不随启动执行**：见上文"数据库迁移"。

## 目录结构

```text
cmd/api             HTTP 服务入口（启动、优雅关闭、容器健康检查子命令）
cmd/migrate         独立迁移命令（只依赖数据库配置）
internal/config     环境变量解析与启动期校验
internal/bootstrap  依赖装配（数据库、迁移校验、Casbin、限流、指标、身份链路）
internal/domain     领域实体与不变量，不依赖 Gin/GORM/Casbin
internal/application 业务用例编排，只依赖 ports
internal/ports      依赖倒置接口与哨兵错误
internal/adapters   外部依赖实现（postgres、casbin、zitadel、ratelimit、metrics）
internal/rbactest   仅供测试使用的授权域内存替身
internal/transport/http  Gin 路由、中间件（request_id、日志、认证、租户守卫、权限、限流、指标）、处理器
internal/oidctest  仅供测试使用的 OIDC 桩
casbin/model.conf  Casbin 模型（sub, dom, obj, act）
migrations         版本化 SQL 迁移
tests/integration  依赖真实 PostgreSQL 的集成、安全回归与备份恢复演练测试
api/openapi.yaml   OpenAPI 3.0 接口契约（由测试守护，不与路由脱节）
deployments        生产编排（docker）与运维脚本（backup/restore/drill/verify-token）
docs               TDD、性能测量、发布运行手册
```
