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
	"net/url"
	"os"
	"strings"
	"time"
)

// 允许的环境与日志级别取值。
const (
	AppEnvDev        = "dev"
	AppEnvProduction = "production"

	// TokenModeJWT 表示本地 JWT 验签。
	TokenModeJWT = "jwt"

	// 默认值集中定义，避免散落在代码中。
	defaultHTTPAddr        = ":8080"
	defaultAppEnv          = AppEnvDev
	defaultLogLevel        = "info"
	defaultShutdownTimeout = 10 * time.Second
	defaultCasbinModelPath = "./casbin/model.conf"
	defaultTokenMode       = TokenModeJWT
	defaultJWKSCacheTTL    = 15 * time.Minute

	// defaultPlatformAdminRoleKey 是平台管理员角色名默认值。
	defaultPlatformAdminRoleKey = "platform_admin"
	// defaultInvitationTTL 是成员邀请默认有效期：7 天。
	defaultInvitationTTL = 7 * 24 * time.Hour

	// 环境变量名集中定义，便于文档与测试引用。
	EnvDatabaseDSN     = "DATABASE_DSN"
	EnvAppEnv          = "APP_ENV"
	EnvHTTPAddr        = "APP_HTTP_ADDR"
	EnvRedisAddr       = "REDIS_ADDR"
	EnvCasbinModelPath = "CASBIN_MODEL_PATH"
	EnvLogLevel        = "LOG_LEVEL"
	EnvShutdownTimeout = "APP_SHUTDOWN_TIMEOUT"

	EnvZitadelIssuer                 = "ZITADEL_ISSUER"
	EnvZitadelClientID               = "ZITADEL_CLIENT_ID"
	EnvZitadelAPIAudience            = "ZITADEL_API_AUDIENCE"
	EnvZitadelServiceCredentialsFile = "ZITADEL_SERVICE_CREDENTIALS_FILE"
	EnvAuthTokenMode                 = "AUTH_TOKEN_MODE"
	EnvAuthJWKSCacheTTL              = "AUTH_JWKS_CACHE_TTL"

	// EnvPlatformAdminRoleKey 指定平台管理员对应的 ZITADEL 角色名。
	EnvPlatformAdminRoleKey = "PLATFORM_ADMIN_ROLE_KEY"
	// EnvInvitationTTL 指定成员邀请的有效期。
	EnvInvitationTTL = "INVITATION_TTL"
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

	// ZITADEL 与令牌校验相关配置。
	ZitadelIssuer   string
	ZitadelClientID string
	// ZitadelAPIAudience 必须与令牌中的 aud 匹配，不能猜测（见 docs/TDD.md 13.2）。
	ZitadelAPIAudience string
	// ZitadelServiceCredentialsFile 仅用于访问 ZITADEL 管理 API 的服务身份，本阶段不使用。
	ZitadelServiceCredentialsFile string
	// AuthTokenMode 目前仅支持 jwt；introspect 尚未实现，必须显式报错而非静默降级。
	AuthTokenMode string
	// AuthJWKSCacheTTL 当前仅解析不生效：go-oidc 的密钥缓存与轮换由其内部策略决定。
	AuthJWKSCacheTTL time.Duration

	// PlatformAdminRoleKey 是平台管理员对应的 ZITADEL 角色名。
	// 平台管理员只能通过该角色授予，服务端不提供白名单或应急通道。
	PlatformAdminRoleKey string
	// InvitationTTL 是成员邀请的有效期。
	InvitationTTL time.Duration
}

// Load 读取环境变量并返回校验后的配置。
// 任一字段非法都会在返回的 error 中体现，错误消息包含对应的环境变量名。
func Load() (Config, error) {
	var errs []error

	cfg := Config{
		AppEnv:                        defaultAppEnv,
		HTTPAddr:                      defaultHTTPAddr,
		DatabaseDSN:                   env(EnvDatabaseDSN),
		RedisAddr:                     env(EnvRedisAddr),
		CasbinModelPath:               defaultCasbinModelPath,
		LogLevel:                      defaultLogLevel,
		ShutdownTimeout:               defaultShutdownTimeout,
		ZitadelClientID:               env(EnvZitadelClientID),
		ZitadelAPIAudience:            env(EnvZitadelAPIAudience),
		ZitadelServiceCredentialsFile: env(EnvZitadelServiceCredentialsFile),
		AuthTokenMode:                 defaultTokenMode,
		AuthJWKSCacheTTL:              defaultJWKSCacheTTL,

		PlatformAdminRoleKey: defaultPlatformAdminRoleKey,
		InvitationTTL:        defaultInvitationTTL,
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

	errs = append(errs, loadZitadelConfig(&cfg)...)
	errs = append(errs, loadTenancyConfig(&cfg)...)

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("配置校验失败：%w", errors.Join(errs...))
	}
	return cfg, nil
}

// LoadDatabaseDSN 只读取并校验迁移所需的数据库配置。
//
// 迁移命令不依赖身份平台配置，不应因为缺少 ZITADEL_* 而无法执行迁移。
func LoadDatabaseDSN() (string, error) {
	dsn := env(EnvDatabaseDSN)
	if dsn == "" {
		return "", fmt.Errorf("配置校验失败：%s 为必填项，不能为空", EnvDatabaseDSN)
	}
	return dsn, nil
}

// loadTenancyConfig 解析并校验租户与成员相关配置，返回全部错误。
func loadTenancyConfig(cfg *Config) []error {
	var errs []error

	// 显式设置为空属于配置错误：那会导致无人可创建租户。
	if raw, ok := os.LookupEnv(EnvPlatformAdminRoleKey); ok {
		cfg.PlatformAdminRoleKey = strings.TrimSpace(raw)
		if cfg.PlatformAdminRoleKey == "" {
			errs = append(errs, fmt.Errorf("%s 不能为空，平台管理员只能通过该角色授予", EnvPlatformAdminRoleKey))
		}
	}

	if v := env(EnvInvitationTTL); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s 不是合法的时间段：%w", EnvInvitationTTL, err))
		} else if d <= 0 {
			errs = append(errs, fmt.Errorf("%s 必须大于 0", EnvInvitationTTL))
		} else {
			cfg.InvitationTTL = d
		}
	}

	return errs
}

// loadZitadelConfig 解析并校验 ZITADEL 与令牌校验配置，返回全部错误。
func loadZitadelConfig(cfg *Config) []error {
	var errs []error

	issuer := env(EnvZitadelIssuer)
	if issuer == "" {
		errs = append(errs, fmt.Errorf("%s 为必填项，不能为空", EnvZitadelIssuer))
	} else {
		parsed, err := url.Parse(issuer)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("%s 不是合法的 URL：%w", EnvZitadelIssuer, err))
		case parsed.Scheme != "http" && parsed.Scheme != "https":
			errs = append(errs, fmt.Errorf("%s 必须使用 http 或 https：%q", EnvZitadelIssuer, issuer))
		case parsed.Host == "":
			errs = append(errs, fmt.Errorf("%s 缺少主机名：%q", EnvZitadelIssuer, issuer))
		case cfg.AppEnv == AppEnvProduction && parsed.Scheme != "https":
			errs = append(errs, fmt.Errorf("%s 在生产环境必须使用 https：%q", EnvZitadelIssuer, issuer))
		default:
			// Discovery 路径由 go-oidc 拼接，尾斜杠会造成双斜杠，统一去掉。
			cfg.ZitadelIssuer = strings.TrimSuffix(issuer, "/")
		}
	}

	if cfg.ZitadelAPIAudience == "" {
		errs = append(errs, fmt.Errorf("%s 为必填项，不能为空", EnvZitadelAPIAudience))
	}

	if v := env(EnvAuthTokenMode); v != "" {
		cfg.AuthTokenMode = v
	}
	switch cfg.AuthTokenMode {
	case TokenModeJWT:
		// 当前唯一支持的模式。
	case "introspect":
		errs = append(errs, fmt.Errorf("%s=introspect 尚未实现，当前仅支持 %s",
			EnvAuthTokenMode, TokenModeJWT))
	default:
		errs = append(errs, fmt.Errorf("%s 取值非法：%q，当前仅支持 %s",
			EnvAuthTokenMode, cfg.AuthTokenMode, TokenModeJWT))
	}

	if v := env(EnvAuthJWKSCacheTTL); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s 不是合法的时间段：%w", EnvAuthJWKSCacheTTL, err))
		} else if d <= 0 {
			errs = append(errs, fmt.Errorf("%s 必须大于 0", EnvAuthJWKSCacheTTL))
		} else {
			cfg.AuthJWKSCacheTTL = d
		}
	}

	return errs
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
