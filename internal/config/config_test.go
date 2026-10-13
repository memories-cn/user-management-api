package config

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoad_MissingDatabaseDSN 校验缺少必填配置时必须报错，且错误信息包含字段名。
func TestLoad_MissingDatabaseDSN(t *testing.T) {
	t.Setenv("DATABASE_DSN", "")

	_, err := Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "DATABASE_DSN")
}

// TestLoad_InvalidAppEnv 校验 APP_ENV 只接受 dev 与 production。
func TestLoad_InvalidAppEnv(t *testing.T) {
	t.Setenv("DATABASE_DSN", "postgres://localhost/iam")
	t.Setenv("APP_ENV", "staging")

	_, err := Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "APP_ENV")
}

// TestLoad_InvalidLogLevel 校验 LOG_LEVEL 只接受 slog 支持的四个级别。
func TestLoad_InvalidLogLevel(t *testing.T) {
	t.Setenv("DATABASE_DSN", "postgres://localhost/iam")
	t.Setenv("LOG_LEVEL", "trace")

	_, err := Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "LOG_LEVEL")
}

// TestLoad_InvalidShutdownTimeout 校验关闭超时必须是合法的时间段。
func TestLoad_InvalidShutdownTimeout(t *testing.T) {
	t.Setenv("DATABASE_DSN", "postgres://localhost/iam")
	t.Setenv("APP_SHUTDOWN_TIMEOUT", "not-a-duration")

	_, err := Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "APP_SHUTDOWN_TIMEOUT")
}

// TestLoad_AggregatesAllErrors 校验多个字段同时非法时一次性返回全部问题，便于一次修完。
func TestLoad_AggregatesAllErrors(t *testing.T) {
	t.Setenv("DATABASE_DSN", "")
	t.Setenv("APP_ENV", "staging")
	t.Setenv("LOG_LEVEL", "trace")

	_, err := Load()

	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "DATABASE_DSN")
	assert.Contains(t, msg, "APP_ENV")
	assert.Contains(t, msg, "LOG_LEVEL")
}

// TestLoad_Defaults 校验仅提供必填配置时使用集中定义的默认值。
func TestLoad_Defaults(t *testing.T) {
	t.Setenv("DATABASE_DSN", "postgres://localhost/iam")
	t.Setenv("APP_ENV", "")
	t.Setenv("APP_HTTP_ADDR", "")
	t.Setenv("APP_SHUTDOWN_TIMEOUT", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("CASBIN_MODEL_PATH", "")

	cfg, err := Load()

	require.NoError(t, err)
	assert.Equal(t, "dev", cfg.AppEnv)
	assert.Equal(t, ":8080", cfg.HTTPAddr)
	assert.Equal(t, "./casbin/model.conf", cfg.CasbinModelPath)
	assert.Equal(t, "info", cfg.LogLevel)
	assert.Equal(t, 10*time.Second, cfg.ShutdownTimeout)
}

// TestLoad_ReadsProvidedValues 校验显式提供的配置被正确解析。
func TestLoad_ReadsProvidedValues(t *testing.T) {
	t.Setenv("DATABASE_DSN", "postgres://localhost/iam")
	t.Setenv("APP_ENV", "production")
	t.Setenv("APP_HTTP_ADDR", "127.0.0.1:9000")
	t.Setenv("REDIS_ADDR", "redis:6379")
	t.Setenv("CASBIN_MODEL_PATH", "/etc/iam/model.conf")
	t.Setenv("LOG_LEVEL", "warn")
	t.Setenv("APP_SHUTDOWN_TIMEOUT", "25s")

	cfg, err := Load()

	require.NoError(t, err)
	assert.Equal(t, "production", cfg.AppEnv)
	assert.Equal(t, "127.0.0.1:9000", cfg.HTTPAddr)
	assert.Equal(t, "redis:6379", cfg.RedisAddr)
	assert.Equal(t, "/etc/iam/model.conf", cfg.CasbinModelPath)
	assert.Equal(t, "warn", cfg.LogLevel)
	assert.Equal(t, 25*time.Second, cfg.ShutdownTimeout)
}

// TestLoad_TrimsValues 校验环境变量首尾空白不会污染配置值。
func TestLoad_TrimsValues(t *testing.T) {
	t.Setenv("DATABASE_DSN", "  postgres://localhost/iam  ")
	t.Setenv("APP_ENV", " production ")

	cfg, err := Load()

	require.NoError(t, err)
	assert.Equal(t, "postgres://localhost/iam", cfg.DatabaseDSN)
	assert.Equal(t, "production", cfg.AppEnv)
	assert.False(t, strings.ContainsAny(cfg.DatabaseDSN[:2], " "))
}
