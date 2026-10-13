.PHONY: fmt vet test test-integration build migrate-up migrate-down migrate-version up down ci

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
