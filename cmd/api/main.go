// Command api 启动 IAM HTTP 服务。
//
// 启动顺序遵循快速失败原则：配置校验 → 日志初始化 → 数据库与迁移校验 → 授权引擎 → HTTP 服务。
// 进程不执行数据库迁移，迁移由独立的 cmd/migrate 步骤完成。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/example/iam-service/internal/bootstrap"
	"github.com/example/iam-service/internal/config"
	iamhttp "github.com/example/iam-service/internal/transport/http"
)

// HTTP 服务器超时参数，防止慢连接耗尽服务资源。
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 15 * time.Second
	idleTimeout       = 60 * time.Second

	// healthcheckPath 是就绪检查路径。
	healthcheckPath = "/health/ready"
	// healthcheckTimeout 是探针单次请求的超时。
	healthcheckTimeout = 5 * time.Second
)

func main() {
	// -healthcheck 供容器探针使用：运行镜像是 distroless，
	// 没有 shell 也没有 curl，无法在 compose 里写 CMD 探针，
	// 因此由二进制自己提供这个子命令。
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		os.Exit(runHealthcheck())
	}

	if err := run(); err != nil {
		slog.Error("service failed to start", "error", err)
		os.Exit(1)
	}
}

// runHealthcheck 请求本地就绪检查端点，返回进程退出码。
func runHealthcheck() int {
	addr := os.Getenv("APP_HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	host := addr
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	}

	url := "http://" + host + healthcheckPath
	client := &http.Client{Timeout: healthcheckTimeout}

	resp, err := client.Get(url)
	if err != nil {
		slog.Error("healthcheck request failed", "error", err, "url", url)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		slog.Warn("healthcheck not ready", "status", resp.StatusCode)
		return 1
	}
	return 0
}

// run 完成服务装配、启动与优雅关闭。
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	setupLogger(cfg)

	if cfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.DebugMode)
	}

	app, err := bootstrap.New(cfg)
	if err != nil {
		return err
	}
	defer func() {
		if err := app.Close(); err != nil {
			slog.Error("failed to close app resources", "error", err)
		}
	}()

	// 跨实例策略失效：按固定间隔比对全局策略版本号并重载本地内存策略；
	// 限流桶的惰性驱逐也在此启动。协程生命周期与进程一致，由 shutdownCtx 取消。
	jobsCtx, cancelJobs := context.WithCancel(context.Background())
	defer cancelJobs()
	app.StartBackgroundJobs(jobsCtx)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           iamhttp.NewRouter(app),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("http server listening", "addr", cfg.HTTPAddr, "env", cfg.AppEnv)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-serverErr:
		if err != nil {
			return fmt.Errorf("HTTP 服务异常退出：%w", err)
		}
		return nil
	case <-ctx.Done():
		slog.Info("shutdown signal received", "grace_period", cfg.ShutdownTimeout.String())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("优雅关闭失败：%w", err)
		}
		slog.Info("http server stopped")
		return nil
	}
}

// setupLogger 根据运行环境选择结构化日志格式。
func setupLogger(cfg config.Config) {
	opts := &slog.HandlerOptions{Level: cfg.LogLevelSlog()}
	var handler slog.Handler
	if cfg.IsProduction() {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(handler))
}
