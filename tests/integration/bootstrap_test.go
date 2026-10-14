// Package integration 包含依赖真实 PostgreSQL 的集成测试。
//
// 跳过策略：
//   - 使用 -short 时整体跳过；
//   - Docker 不可用时在 TestMain 阶段跳过，而不是让本地构建与 CI 失败。
//
// 每个用例使用容器内的独立数据库，避免相互干扰。
package integration

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/casbin/casbin/v3"
	"github.com/golang-migrate/migrate/v4"
	// 注册 PostgreSQL 迁移驱动。
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"gorm.io/gorm"

	"github.com/example/iam-service/internal/bootstrap"
	"github.com/example/iam-service/internal/config"
	"github.com/example/iam-service/migrations"
)

// 容器与连接参数。
const (
	postgresImage    = "postgres:16-alpine"
	postgresUser     = "postgres"
	postgresPassword = "postgres"
	adminDatabase    = "postgres"
	startupTimeout   = 90 * time.Second
	// casbinRuleColumns 是 casbin_rule 期望的列数量：id + ptype + v0..v5。
	casbinRuleColumns = 8
	// modelPath 指向仓库根目录的 Casbin 模型文件。
	modelPath = "../../casbin/model.conf"
)

// adminDB 指向容器中的默认数据库，用于创建与销毁每个用例的独立数据库。
var adminDB *sql.DB

// pgHost、pgPort 是 PostgreSQL 容器映射到宿主机的地址，由 TestMain 初始化。
var pgHost, pgPort string

func TestMain(m *testing.M) {
	// TestMain 运行前测试标志尚未解析，必须先解析才能读取 -short。
	flag.Parse()

	if testing.Short() {
		log.Println("跳过集成测试：-short")
		os.Exit(0)
	}

	ctx := context.Background()
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		log.Printf("跳过集成测试：Docker 不可用（%v）", err)
		os.Exit(0)
	}
	if err := provider.Health(ctx); err != nil {
		log.Printf("跳过集成测试：Docker 不健康（%v）", err)
		os.Exit(0)
	}

	ctr, err := postgres.Run(ctx, postgresImage,
		postgres.WithUsername(postgresUser),
		postgres.WithPassword(postgresPassword),
		postgres.WithDatabase(adminDatabase),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("5432/tcp").WithStartupTimeout(startupTimeout),
		),
	)
	if err != nil {
		log.Printf("启动 PostgreSQL 容器失败：%v", err)
		os.Exit(1)
	}

	host, err := ctr.Host(ctx)
	if err != nil {
		log.Printf("解析容器地址失败：%v", err)
		os.Exit(1)
	}
	mapped, err := ctr.MappedPort(ctx, "5432/tcp")
	if err != nil {
		log.Printf("解析容器端口失败：%v", err)
		os.Exit(1)
	}
	pgHost, pgPort = host, mapped.Port()

	db, err := sql.Open("postgres", adminDSN())
	if err != nil {
		log.Printf("连接 PostgreSQL 失败：%v", err)
		os.Exit(1)
	}
	adminDB = db

	code := m.Run()

	_ = db.Close()
	if err := ctr.Terminate(context.Background()); err != nil {
		log.Printf("销毁 PostgreSQL 容器失败：%v", err)
	}
	os.Exit(code)
}

// adminDSN 返回容器默认数据库的连接串。
func adminDSN() string {
	return dsnFor(adminDatabase)
}

// dsnFor 构造指定数据库的连接串。
func dsnFor(name string) string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		postgresUser, postgresPassword, pgHost, pgPort, name)
}

// newDatabase 创建一个独立数据库并返回其 DSN，用例结束时丢弃该库。
func newDatabase(t *testing.T) string {
	t.Helper()

	name := "iam_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err := adminDB.Exec(fmt.Sprintf("CREATE DATABASE %s", name))
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = adminDB.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", name))
	})
	return dsnFor(name)
}

// TestMigrationsUpIsIdempotent 校验迁移可重复执行且建表结果符合预期。
func TestMigrationsUpIsIdempotent(t *testing.T) {
	dsn := newDatabase(t)

	require.NoError(t, migrateUp(dsn))
	require.NoError(t, migrateUp(dsn), "重复执行迁移必须幂等且不报错")

	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()

	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns
		WHERE table_name = 'casbin_rule' AND table_schema = current_schema()`).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, casbinRuleColumns, count)

	// pg_indexes 同时包含主键索引且无稳定顺序，因此按索引名精确断言唯一索引存在。
	var indexCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM pg_indexes
		WHERE tablename = 'casbin_rule' AND indexname = 'idx_casbin_rule_unique'`).Scan(&indexCount)
	require.NoError(t, err)
	assert.Equal(t, 1, indexCount, "casbin_rule 必须存在唯一索引")
}

// TestVerifyMigrationsFailsWhenTableMissing 校验未执行迁移时启动校验失败。
func TestVerifyMigrationsFailsWhenTableMissing(t *testing.T) {
	dsn := newDatabase(t)

	db, err := bootstrap.OpenDB(configFor(dsn))
	require.NoError(t, err)
	defer closeGorm(t, db)

	err = bootstrap.VerifyMigrations(context.Background(), db)
	require.Error(t, err, "未执行迁移时必须报错，不能放行")
}

// TestVerifyMigrationsSucceedsAfterUp 校验迁移执行后启动校验通过。
func TestVerifyMigrationsSucceedsAfterUp(t *testing.T) {
	dsn := newDatabase(t)
	require.NoError(t, migrateUp(dsn))

	db, err := bootstrap.OpenDB(configFor(dsn))
	require.NoError(t, err)
	defer closeGorm(t, db)

	require.NoError(t, bootstrap.VerifyMigrations(context.Background(), db))
}

// TestVerifyMigrationsFailsAfterDown 校验回滚后启动校验失败。
func TestVerifyMigrationsFailsAfterDown(t *testing.T) {
	dsn := newDatabase(t)
	require.NoError(t, migrateUp(dsn))
	require.NoError(t, migrateDown(dsn))

	db, err := bootstrap.OpenDB(configFor(dsn))
	require.NoError(t, err)
	defer closeGorm(t, db)

	require.Error(t, bootstrap.VerifyMigrations(context.Background(), db))
}

// TestEnforcerDefaultDeny 校验策略为空时授权判定为拒绝，且不返回错误。
func TestEnforcerDefaultDeny(t *testing.T) {
	dsn := newDatabase(t)
	require.NoError(t, migrateUp(dsn))

	db, err := bootstrap.OpenDB(configFor(dsn))
	require.NoError(t, err)
	defer closeGorm(t, db)

	enforcer, err := bootstrap.NewEnforcer(db, modelPath)
	require.NoError(t, err)

	allowed, err := enforcer.Enforce("user-001", "tenant-001", "member", "invite")
	require.NoError(t, err)
	assert.False(t, allowed, "未定义权限时必须默认拒绝")
}

// TestEnforcerHonoursTenantDomain 校验策略的租户域必须匹配，跨租户不得命中。
func TestEnforcerHonoursTenantDomain(t *testing.T) {
	dsn := newDatabase(t)
	require.NoError(t, migrateUp(dsn))

	db, err := bootstrap.OpenDB(configFor(dsn))
	require.NoError(t, err)
	defer closeGorm(t, db)

	enforcer, err := bootstrap.NewEnforcer(db, modelPath)
	require.NoError(t, err)

	_, err = enforcer.AddPolicy("owner", "tenant-001", "member", "invite")
	require.NoError(t, err)
	_, err = enforcer.AddGroupingPolicy("user-001", "owner", "tenant-001")
	require.NoError(t, err)
	require.NoError(t, enforcer.SavePolicy())

	// 重新加载以确认策略确实持久化到数据库。
	reloaded, err := bootstrap.NewEnforcer(db, modelPath)
	require.NoError(t, err)
	assertEnforce(t, reloaded, "user-001", "tenant-001", "member", "invite", true)
	assertEnforce(t, reloaded, "user-001", "tenant-002", "member", "invite", false, "跨租户不得命中策略")
}

// TestAppCheckFailsWhenDatabaseUnreachable 校验依赖不可用时候绪检查失败（fail closed）。
func TestAppCheckFailsWhenDatabaseUnreachable(t *testing.T) {
	dsn := newDatabase(t)
	require.NoError(t, migrateUp(dsn))

	db, err := bootstrap.OpenDB(configFor(dsn))
	require.NoError(t, err)

	enforcer, err := bootstrap.NewEnforcer(db, modelPath)
	require.NoError(t, err)

	// 授权器依赖策略版本号，就绪检查会读取其刷新状态，必须一并装配。
	authorizer, err := bootstrap.NewAuthorizer(enforcer, bootstrap.NewPolicyRepository(db), time.Second)
	require.NoError(t, err)

	app := &bootstrap.App{DB: db, Enforcer: enforcer, Authorizer: authorizer}
	require.NoError(t, app.Check(context.Background()))

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	require.Error(t, app.Check(context.Background()), "数据库不可用时就绪检查必须失败而不是放行")
}

// assertEnforce 断言授权判定结果。
func assertEnforce(t *testing.T, e *casbin.SyncedEnforcer, sub, dom, obj, act string, want bool, msgAndArgs ...any) {
	t.Helper()

	allowed, err := e.Enforce(sub, dom, obj, act)
	require.NoError(t, err)
	assert.Equal(t, want, allowed, msgAndArgs...)
}

// configFor 构造指向指定 DSN 的最小配置。
func configFor(dsn string) config.Config {
	return config.Config{DatabaseDSN: dsn}
}

// closeGorm 关闭数据库连接。
func closeGorm(t *testing.T, db *gorm.DB) {
	t.Helper()

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
}

// migrateUp 对指定数据库应用全部迁移。
func migrateUp(dsn string) error {
	m, err := newMigrate(dsn)
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

// migrateDown 回滚指定数据库的全部迁移。
func migrateDown(dsn string) error {
	m, err := newMigrate(dsn)
	if err != nil {
		return err
	}
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

// newMigrate 基于内嵌迁移文件构造迁移实例。
func newMigrate(dsn string) (*migrate.Migrate, error) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return nil, err
	}
	return migrate.NewWithSourceInstance("iofs", src, dsn)
}
