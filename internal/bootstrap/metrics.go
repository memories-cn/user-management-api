package bootstrap

import (
	"log/slog"

	"github.com/example/iam-service/internal/adapters/metrics"
	"github.com/example/iam-service/internal/adapters/ratelimit"
	"github.com/example/iam-service/internal/config"
)

// NewRateLimiter 构造管理类接口的进程内限流器。
//
// rps 与 burst 分别来自配置：burst = rps × 倍率，允许短时突发而不放开持续速率。
// 未启用时限流返回 nil，路由层据此跳过中间件装配。
func NewRateLimiter(cfg config.Config) (*ratelimit.MemoryLimiter, error) {
	if !cfg.RateLimitEnabled {
		return nil, nil
	}

	burst := int(cfg.ManageRPS * cfg.RateLimitBurst)
	if burst < 1 {
		burst = 1
	}

	limiter, err := ratelimit.NewMemoryLimiter(cfg.ManageRPS, burst)
	if err != nil {
		return nil, err
	}
	slog.Info("管理接口限流已启用", "rps", cfg.ManageRPS, "burst", burst)
	return limiter, nil
}

// NewAuthorizeLimiter 构造授权判定接口的进程内限流器。
//
// 与管理接口分开配置：授权判定在业务热路径上，
// 用同一个阈值要么误限业务调用，要么让管理接口形同不限。
func NewAuthorizeLimiter(cfg config.Config) (*ratelimit.MemoryLimiter, error) {
	if !cfg.RateLimitEnabled {
		return nil, nil
	}

	burst := int(cfg.AuthorizeRPS * cfg.RateLimitBurst)
	if burst < 1 {
		burst = 1
	}

	limiter, err := ratelimit.NewMemoryLimiter(cfg.AuthorizeRPS, burst)
	if err != nil {
		return nil, err
	}
	slog.Info("授权接口限流已启用", "rps", cfg.AuthorizeRPS, "burst", burst)
	return limiter, nil
}

// NewMetricsRecorder 构造指标采集器；未启用时返回 nil，中间件据此跳过采集。
func NewMetricsRecorder(cfg config.Config) (*metrics.Recorder, error) {
	if !cfg.MetricsEnabled {
		slog.Info("指标端点未启用")
		return nil, nil
	}

	recorder, err := metrics.NewRecorder()
	if err != nil {
		return nil, err
	}
	slog.Info("指标端点已启用", "path", cfg.MetricsPath)
	return recorder, nil
}
