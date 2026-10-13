# IAM Service

基于 ZITADEL 的多租户用户管理与授权微服务。当前处于 **Milestone 1：可运行的服务骨架** 阶段，
只提供可运行地基（配置、数据库、迁移、Casbin 装配、健康检查），尚未包含认证与业务功能。

## 技术栈

Go · Gin · GORM · PostgreSQL · Casbin（RBAC + Tenant Domain）· golang-migrate

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
curl -i http://localhost:8080/health/live    # 恒返回 200
curl -i http://localhost:8080/health/ready   # 数据库与迁移就绪时返回 200，否则 503
```

## 环境变量

| 变量 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `DATABASE_DSN` | 是 | — | PostgreSQL 连接串 |
| `APP_ENV` | 否 | `dev` | `dev` 或 `production`，决定 Gin 模式与日志格式 |
| `APP_HTTP_ADDR` | 否 | `:8080` | HTTP 监听地址 |
| `APP_SHUTDOWN_TIMEOUT` | 否 | `10s` | 优雅关闭宽限期 |
| `REDIS_ADDR` | 否 | — | 可选缓存地址，当前仅解析不使用 |
| `CASBIN_MODEL_PATH` | 否 | `./casbin/model.conf` | Casbin 模型文件 |
| `LOG_LEVEL` | 否 | `info` | `debug` / `info` / `warn` / `error` |

缺少必要配置或取值非法时，进程会**立即退出**并在日志中列出全部问题字段。

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
```

集成测试使用 testcontainers 启动 PostgreSQL 16；当使用 `-short` 或 Docker 不可用时会自动跳过，
不会让本地构建失败。

## 容器部署

```bash
make up    # docker compose up -d --build（postgres → migrate → api）
make down
```

`docker-compose.yml` 中的 `migrate` 服务以 `restart: "no"` 运行一次，
`api` 服务等待其成功完成后才启动。

## 关键约定

- **统一响应格式**：成功 `{"data":..., "request_id":...}`；失败 `{"error":{"code","message"},"request_id":...}`。
- **request_id**：每个响应都携带，响应头 `X-Request-ID` 与响应体 `request_id` 一致；客户端提供的非法值（超长、含换行）会被丢弃并重新生成。
- **健康检查**：`/health/live` 不触碰任何依赖，`/health/ready` 真实校验数据库、迁移状态与 Casbin 策略加载。依赖不可用返回 `503`，绝不放行。
- **默认拒绝**：Casbin 模型中 `dom` 即业务租户 ID，未定义权限一律拒绝。
- **迁移不随启动执行**：见上文"数据库迁移"。

## 目录结构

```text
cmd/api          HTTP 服务入口（启动、优雅关闭）
cmd/migrate      独立迁移命令
internal/config  环境变量解析与校验
internal/bootstrap 依赖装配（数据库、迁移校验、Casbin）
internal/transport/http  Gin 路由、中间件、处理器
casbin/model.conf        Casbin 模型（sub, dom, obj, act）
migrations              版本化 SQL 迁移
tests/integration       依赖真实 PostgreSQL 的集成测试
```
