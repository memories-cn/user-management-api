// Package config 负责读取环境变量并在启动阶段完成校验。
//
// 设计约定：
//   - 缺少必要配置或存在非法值时立即返回错误，由调用方决定退出，禁止 panic。
//   - 所有字段错误一次性聚合返回，便于一次修完而不是逐个试错。
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// 允许的环境与日志级别取值。
const (
	AppEnvDev        = "dev"
	AppEnvProduction = "production"

	// 默认值集中定义，避免散落在代码中。
	defaultHTTPAddr        = ":8080"
	defaultAppEnv          = AppEnvDev
	defaultLogLevel        = "info"
	defaultShutdownTimeout = 10 * time.Second
	defaultCasbinModelPath = "./casbin/model.conf"

	// 环境变量名集中定义，便于文档与测试引用。
	EnvDatabaseDSN     = "DATABASE_DSN"
	EnvAppEnv          = "APP_ENV"
	EnvHTTPAddr        = "APP_HTTP_ADDR"
	EnvRedisAddr       = "REDIS_ADDR"
	EnvCasbinModelPath = "CASBIN_MODEL_PATH"
	EnvLogLevel        = "LOG_LEVEL"
	EnvShutdownTimeout = "APP_SHUTDOWN_TIMEOUT"
)

// Config 保存服务运行所需的全部配置。
type Config struct {
	// AppEnv 取值为 dev 或 production，决定 Gin 模式与日志格式。
	AppEnv string
	// HTTPAddr 是 HTTP 服务监听地址。
	HTTPAddr string
	// DatabaseDSN 是 PostgreSQL 连接串，必填。
	DatabaseDSN string
	// RedisAddr 是可选缓存地址，本阶段仅解析不使用。
	RedisAddr string
	// CasbinModelPath 指向 Casbin 模型文件。
	CasbinModelPath string
	// LogLevel 取值为 debug、info、warn、error。
	LogLevel string
	// ShutdownTimeout 是优雅关闭的在途请求宽限期。
	ShutdownTimeout time.Duration
}

// Load 读取环境变量并返回校验后的配置。
// 任一字段非法都会在返回的 error 中体现，错误消息包含对应的环境变量名。
func Load() (Config, error) {
	var errs []error

	cfg := Config{
		AppEnv:          defaultAppEnv,
		HTTPAddr:        defaultHTTPAddr,
		DatabaseDSN:     env(EnvDatabaseDSN),
		RedisAddr:       env(EnvRedisAddr),
		CasbinModelPath: defaultCasbinModelPath,
		LogLevel:        defaultLogLevel,
		ShutdownTimeout: defaultShutdownTimeout,
	}

	if cfg.DatabaseDSN == "" {
		errs = append(errs, fmt.Errorf("%s 为必填项，不能为空", EnvDatabaseDSN))
	}

	if v := env(EnvAppEnv); v != "" {
		cfg.AppEnv = v
	}
	if cfg.AppEnv != AppEnvDev && cfg.AppEnv != AppEnvProduction {
		errs = append(errs, fmt.Errorf("%s 取值非法：%q，仅支持 %s 或 %s",
			EnvAppEnv, cfg.AppEnv, AppEnvDev, AppEnvProduction))
	}

	if v := env(EnvHTTPAddr); v != "" {
		cfg.HTTPAddr = v
	}
	if cfg.HTTPAddr == "" {
		errs = append(errs, fmt.Errorf("%s 不能为空", EnvHTTPAddr))
	}

	if v := env(EnvCasbinModelPath); v != "" {
		cfg.CasbinModelPath = v
	}
	if cfg.CasbinModelPath == "" {
		errs = append(errs, fmt.Errorf("%s 不能为空", EnvCasbinModelPath))
	}

	if v := env(EnvLogLevel); v != "" {
		cfg.LogLevel = v
	}
	if !isSupportedLogLevel(cfg.LogLevel) {
		errs = append(errs, fmt.Errorf("%s 取值非法：%q，仅支持 debug、info、warn、error",
			EnvLogLevel, cfg.LogLevel))
	}

	if v := env(EnvShutdownTimeout); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s 不是合法的时间段：%w", EnvShutdownTimeout, err))
		} else if d <= 0 {
			errs = append(errs, fmt.Errorf("%s 必须大于 0", EnvShutdownTimeout))
		} else {
			cfg.ShutdownTimeout = d
		}
	}

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("配置校验失败：%w", errors.Join(errs...))
	}
	return cfg, nil
}

// IsProduction 报告当前是否运行在生产环境。
func (c Config) IsProduction() bool {
	return c.AppEnv == AppEnvProduction
}

// LogLevelSlog 把配置中的日志级别映射为 slog 级别。
func (c Config) LogLevelSlog() slog.Level {
	switch c.LogLevel {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// env 读取环境变量并去除首尾空白。
func env(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}

// isSupportedLogLevel 判断日志级别是否被支持。
func isSupportedLogLevel(level string) bool {
	switch level {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}
