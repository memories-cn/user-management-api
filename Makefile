.PHONY: fmt vet test test-integration build migrate-up migrate-down migrate-version up down ci bench load-test vulncheck backup restore drill

# 格式化全部 Go 代码
fmt:
	gofmt -l -w .

# 静态检查
vet:
	go vet ./...

# 单元测试，不需要外部服务
test:
	go test -short -race ./...

# 集成测试，需要可用的 Docker 守护进程
test-integration:
	go test ./tests/integration/... -count=1

# 编译 API 与迁移命令
build:
	go build -o bin/iam-api ./cmd/api
	go build -o bin/iam-migrate ./cmd/migrate

# 应用全部待执行迁移
migrate-up:
	go run ./cmd/migrate -cmd up

# 回滚全部迁移
migrate-down:
	go run ./cmd/migrate -cmd down

# 打印当前迁移版本与 dirty 状态
migrate-version:
	go run ./cmd/migrate -cmd version

# 本地容器编排
up:
	docker compose up -d --build

down:
	docker compose down

# CI 门禁：格式、静态检查、单元测试、构建
ci: vet test build
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "以下文件未通过 gofmt："; echo "$$unformatted"; exit 1; \
	fi

# 授权判定微基准（对应 docs/TDD.md 12.1 的 10ms 目标）。
# 进 CI：用于防止判定路径发生数量级级别的劣化。
bench:
	go test ./internal/adapters/casbin/ -run '^$$' -bench BenchmarkEnforce -benchtime=2000x

# 端到端负载测试（对应 200ms 目标与 100 并发基线）。
# 默认不运行：需要 Docker 且结果受机器负载影响大，按需执行。
# 结果记录到 docs/performance.md。
load-test:
	IAM_LOAD_TEST=1 go test ./tests/integration/... -run TestLoad -count=1 -v -timeout 30m

# 依赖漏洞扫描（需要网络访问漏洞库）
vulncheck:
	go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./...

# 逻辑备份到 ./backups（需设置 DATABASE_DSN）
backup:
	./deployments/scripts/backup.sh ./backups

# 从备份还原（会 DROP 目标库对象，务必二次确认）
restore:
	./deployments/scripts/restore.sh $(DUMP) $(DATABASE_DSN)

# 备份恢复演练：备份 → 还原到独立库 → 校验版本与行数
drill:
	DATABASE_DSN=$(DATABASE_DSN) ./deployments/scripts/drill.sh
