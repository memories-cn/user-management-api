package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setRequiredEnv 设置启动必需的环境变量，避免每个用例重复书写。
func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv(EnvDatabaseDSN, "postgres://localhost/iam")
	t.Setenv(EnvZitadelIssuer, "https://auth.example.com")
	t.Setenv(EnvZitadelAPIAudience, "iam-api-audience")
}

// TestLoad_MissingDatabaseDSN 校验缺少必填配置时必须报错，且错误信息包含字段名。
func TestLoad_MissingDatabaseDSN(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv(EnvDatabaseDSN, "")

	_, err := Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvDatabaseDSN)
}

// TestLoad_InvalidAppEnv 校验 APP_ENV 只接受 dev 与 production。
func TestLoad_InvalidAppEnv(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv(EnvAppEnv, "staging")

	_, err := Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvAppEnv)
}

// TestLoad_InvalidLogLevel 校验 LOG_LEVEL 只接受 slog 支持的四个级别。
func TestLoad_InvalidLogLevel(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv(EnvLogLevel, "trace")

	_, err := Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvLogLevel)
}

// TestLoad_InvalidShutdownTimeout 校验关闭超时必须是合法的时间段。
func TestLoad_InvalidShutdownTimeout(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv(EnvShutdownTimeout, "not-a-duration")

	_, err := Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvShutdownTimeout)
}

// TestLoad_AggregatesAllErrors 校验多个字段同时非法时一次性返回全部问题。
func TestLoad_AggregatesAllErrors(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv(EnvDatabaseDSN, "")
	t.Setenv(EnvAppEnv, "staging")
	t.Setenv(EnvLogLevel, "trace")

	_, err := Load()

	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, EnvDatabaseDSN)
	assert.Contains(t, msg, EnvAppEnv)
	assert.Contains(t, msg, EnvLogLevel)
}

// TestLoad_Defaults 校验仅提供必填配置时使用集中定义的默认值。
func TestLoad_Defaults(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv(EnvAppEnv, "")
	t.Setenv(EnvHTTPAddr, "")
	t.Setenv(EnvShutdownTimeout, "")
	t.Setenv(EnvLogLevel, "")
	t.Setenv(EnvCasbinModelPath, "")
	t.Setenv(EnvAuthTokenMode, "")
	t.Setenv(EnvAuthJWKSCacheTTL, "")

	cfg, err := Load()

	require.NoError(t, err)
	assert.Equal(t, "dev", cfg.AppEnv)
	assert.Equal(t, ":8080", cfg.HTTPAddr)
	assert.Equal(t, "./casbin/model.conf", cfg.CasbinModelPath)
	assert.Equal(t, "info", cfg.LogLevel)
	assert.Equal(t, 10*time.Second, cfg.ShutdownTimeout)
	assert.Equal(t, TokenModeJWT, cfg.AuthTokenMode)
	assert.Equal(t, 15*time.Minute, cfg.AuthJWKSCacheTTL)
}

// TestLoad_ReadsProvidedValues 校验显式提供的配置被正确解析。
func TestLoad_ReadsProvidedValues(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv(EnvAppEnv, "production")
	t.Setenv(EnvHTTPAddr, "127.0.0.1:9000")
	t.Setenv(EnvRedisAddr, "redis:6379")
	t.Setenv(EnvCasbinModelPath, "/etc/iam/model.conf")
	t.Setenv(EnvLogLevel, "warn")
	t.Setenv(EnvShutdownTimeout, "25s")
	t.Setenv(EnvZitadelClientID, "client-1")
	t.Setenv(EnvZitadelServiceCredentialsFile, "/run/secrets/zitadel.json")
	t.Setenv(EnvAuthJWKSCacheTTL, "5m")

	cfg, err := Load()

	require.NoError(t, err)
	assert.Equal(t, "production", cfg.AppEnv)
	assert.Equal(t, "127.0.0.1:9000", cfg.HTTPAddr)
	assert.Equal(t, "redis:6379", cfg.RedisAddr)
	assert.Equal(t, "/etc/iam/model.conf", cfg.CasbinModelPath)
	assert.Equal(t, "warn", cfg.LogLevel)
	assert.Equal(t, 25*time.Second, cfg.ShutdownTimeout)
	assert.Equal(t, "client-1", cfg.ZitadelClientID)
	assert.Equal(t, "/run/secrets/zitadel.json", cfg.ZitadelServiceCredentialsFile)
	assert.Equal(t, 5*time.Minute, cfg.AuthJWKSCacheTTL)
	assert.True(t, cfg.IsProduction())
}

// TestLoad_TrimsValues 校验环境变量首尾空白不会污染配置值。
func TestLoad_TrimsValues(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv(EnvDatabaseDSN, "  postgres://localhost/iam  ")
	t.Setenv(EnvAppEnv, " production ")

	cfg, err := Load()

	require.NoError(t, err)
	assert.Equal(t, "postgres://localhost/iam", cfg.DatabaseDSN)
	assert.Equal(t, "production", cfg.AppEnv)
}

// TestLoad_MissingZitadelIssuer 校验缺少 Issuer 时报错。
func TestLoad_MissingZitadelIssuer(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv(EnvZitadelIssuer, "")

	_, err := Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvZitadelIssuer)
}

// TestLoad_MissingAudience 校验缺少受众时报错，受众不允许猜测。
func TestLoad_MissingAudience(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv(EnvZitadelAPIAudience, "")

	_, err := Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvZitadelAPIAudience)
}

// TestLoad_InvalidIssuerScheme 校验 Issuer 必须使用 http 或 https。
func TestLoad_InvalidIssuerScheme(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv(EnvZitadelIssuer, "auth.example.com")

	_, err := Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvZitadelIssuer)
}

// TestLoad_ProductionRequiresHTTPS 校验生产环境的 Issuer 必须是 https。
func TestLoad_ProductionRequiresHTTPS(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv(EnvAppEnv, AppEnvProduction)
	t.Setenv(EnvZitadelIssuer, "http://auth.internal")

	_, err := Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvZitadelIssuer)
}

// TestLoad_IssuerTrailingSlashTrimmed 校验 Issuer 尾斜杠被去掉，避免 Discovery 路径出现双斜杠。
func TestLoad_IssuerTrailingSlashTrimmed(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv(EnvZitadelIssuer, "https://auth.example.com/")

	cfg, err := Load()

	require.NoError(t, err)
	assert.Equal(t, "https://auth.example.com", cfg.ZitadelIssuer)
}

// TestLoad_IntrospectTokenModeNotSupported 校验未实现的令牌模式必须显式报错而不是静默降级。
func TestLoad_IntrospectTokenModeNotSupported(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv(EnvAuthTokenMode, "introspect")

	_, err := Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvAuthTokenMode)
	assert.Contains(t, err.Error(), "尚未实现")
}

// TestLoad_InvalidTokenMode 校验未知令牌模式被拒绝。
func TestLoad_InvalidTokenMode(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv(EnvAuthTokenMode, "magic")

	_, err := Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvAuthTokenMode)
}

// TestLoadDatabaseDSN 校验迁移命令只需数据库配置。
func TestLoadDatabaseDSN(t *testing.T) {
	t.Setenv(EnvDatabaseDSN, "postgres://localhost/iam")
	t.Setenv(EnvZitadelIssuer, "")

	dsn, err := LoadDatabaseDSN()

	require.NoError(t, err, "迁移不应依赖 ZITADEL 配置")
	assert.Equal(t, "postgres://localhost/iam", dsn)
}

// TestLoadDatabaseDSN_Missing 校验缺少数据库配置时报错。
func TestLoadDatabaseDSN_Missing(t *testing.T) {
	t.Setenv(EnvDatabaseDSN, "")

	_, err := LoadDatabaseDSN()

	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvDatabaseDSN)
}

// TestConfig_LogLevelSlog 校验日志级别映射。
func TestConfig_LogLevelSlog(t *testing.T) {
	levels := map[string]int{
		"debug": -4,
		"info":  0,
		"warn":  4,
		"error": 8,
	}
	for level, want := range levels {
		cfg := Config{LogLevel: level}
		assert.Equal(t, want, int(cfg.LogLevelSlog()), "日志级别 %s 映射错误", level)
	}
}
