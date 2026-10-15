# 发布与回滚运行手册

## 发布前检查

- [ ] `make ci` 全绿（格式、静态检查、单元测试、构建）
- [ ] CI 的 `integration tests` 与 `vulncheck` 两个 job 均通过
- [ ] `go test ./tests/integration/... -run TestOpenAPI -count=1` 通过（文档与路由一致）
- [ ] 已确认目标环境的 `ZITADEL_ISSUER` / `ZITADEL_API_AUDIENCE` 与真实令牌一致
- [ ] **已执行真实 ZITADEL 令牌核对**（见下节）——CI 中无 ZITADEL 实例，这是人工门禁
- [ ] 已确认 `TRUSTED_PROXIES` 配置为边缘网关所在网络的 CIDR
- [ ] 如需容量承诺，已在目标环境复测性能（见 `docs/performance.md`）

### 真实 ZITADEL 令牌核对（人工门禁）

docs/TDD.md 16 要求"ZITADEL Token 验证通过真实 Token 集成测试"。
桩只能验证我们的验签逻辑，验证不了 Issuer / Audience / 角色声明与真实
ZITADEL 配置一致——后者只能由真实令牌确认。

```bash
IAM_BASE_URL=https://iam.example.com \
ZITADEL_TOKEN=<真实访问令牌> \
PLATFORM_ADMIN_TOKEN=<真实平台管理员令牌> \
  ./deployments/scripts/verify-token.sh
```

脚本会核对无令牌 401、有效令牌 200、篡改令牌 401，并打印需人工确认的
claim 清单（iss / aud / alg / roles）。

## 发布步骤

```bash
# 1. 打 tag（语义化版本）
git tag -a v1.2.0 -m "发布说明"
git push origin v1.2.0

# 2. CI 自动构建多架构镜像并推送到 GHCR（.github/workflows/release.yml）
#    等待 workflow 完成，记录镜像 tag 与 digest

# 3. 数据库迁移——独立、可审计的一步，不随 API 启动执行
docker compose -f deployments/docker/docker-compose.yml run --rm migrate

# 4. 滚动部署 API
docker compose -f deployments/docker/docker-compose.yml up -d --no-deps api

# 5. 冒烟验证
curl -fsS https://iam.example.com/health/ready
curl -fsS -H "Authorization: Bearer $TOKEN" https://iam.example.com/api/v1/me

# 6. 观察指标一段时间（至少覆盖一个 POLICY_REFRESH_INTERVAL）
curl -fsS http://iam-internal:8080/metrics | grep -E 'iam_http_requests_total|iam_policy_version'
```

关注指标：

- `iam_http_requests_total{status="5xx"}` 是否上升
- `iam_rate_limited_total` 是否异常升高（可能说明阈值过紧或 `TRUSTED_PROXIES` 配错）
- `iam_policy_version` 是否与预期一致、是否在刷新后推进
- `iam_policy_reloads_total{result="failed"}` 是否为 0

## 回滚

### 应用回滚（推荐）

镜像 tag 回退到上一个已知良好版本：

```bash
# 1. 用上一个 tag 重新部署
export IMAGE_TAG=v1.1.0
docker compose -f deployments/docker/docker-compose.yml up -d --no-deps api

# 2. 验证
curl -fsS https://iam.example.com/health/ready
```

### 数据库迁移不回滚

**默认不回滚迁移。** 原因：

- 迁移已提交的结构变更可能已被新版本代码产生的数据依赖；
- `migrate down` 会 DROP 表，属于破坏性操作，只应在确认无数据依赖时执行。

如果必须回滚结构变更：

1. 先从备份恢复（`./deployments/scripts/restore.sh`），而不是 `migrate down`；
2. 恢复前确认目标库迁移版本不高于备份；
3. 恢复完成后重新执行到目标版本。

### 需要回滚数据时

```bash
# 演练（在独立库上验证备份可用）
DATABASE_DSN=$PROD_DSN ./deployments/scripts/drill.sh

# 确认无误后恢复（会 DROP 目标库对象，务必二次确认）
./deployments/scripts/restore.sh ./backups/latest $PROD_DSN
```

## 定期演练

备份恢复不是"配了就是可用"。建议：

- CI 中已包含 `TestBackupRestoreDrill`（真实 pg_dump / pg_restore + 逐表比对）；
- 生产环境按季度执行一次 `drill.sh`，确认备份文件真的能还原；
- 每次演练后更新 `docs/performance.md` 或单独的演练记录。
