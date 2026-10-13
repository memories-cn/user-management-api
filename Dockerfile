# 构建阶段：编译 API 与迁移两个二进制。
FROM golang:1.27-alpine AS build

WORKDIR /src

# 先复制依赖清单，充分利用 Docker 层缓存。
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# 迁移文件已通过 go:embed 编入二进制，镜像中无需单独复制 migrations 目录。
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/iam-api ./cmd/api \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/iam-migrate ./cmd/migrate

# 运行阶段：使用无 shell 的最小镜像并以非 root 运行。
FROM gcr.io/distroless/static:nonroot

COPY --from=build /out/iam-api /iam-api
COPY --from=build /out/iam-migrate /iam-migrate
COPY casbin/model.conf /casbin/model.conf

USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["/iam-api"]
