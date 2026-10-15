package integration

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBackupRestoreDrill 在 CI 中跑一次真实的备份恢复演练。
//
// docs/TDD.md 16 要求"数据库迁移、备份与恢复流程经过验证"——
// 有脚本不等于可用，只有真的 dump 再 restore 一遍并比对数据才算验证。
//
// 演练步骤与 deployments/scripts/drill.sh 完全一致：
// 迁移 → 播种 → pg_dump → 建新库 → pg_restore → 逐表比对。
func TestBackupRestoreDrill(t *testing.T) {
	source := newDatabase(t)
	require.NoError(t, migrateUp(source))

	seedForDrill(t, source)

	// 容器内执行 pg_dump，与生产脚本使用同一命令形态。
	// 必须把宿主映射地址改写成容器内地址：容器里的 localhost 指向它自己。
	require.NoError(t, execInPostgres(t, "pg_dump",
		"--format=custom", "--no-owner",
		"--dbname="+internalDSN(source),
		"--file=/tmp/drill.dump"))

	restored := newDatabase(t)

	// 还原前先校验目标库迁移版本不高于备份，否则会把新结构还原到旧库上。
	require.NoError(t, execInPostgres(t, "pg_restore",
		"--clean", "--if-exists", "--no-owner",
		"--dbname="+internalDSN(restored), "/tmp/drill.dump"))

	// 逐表比对行数：这是"恢复真的可用"的最直接证据。
	for _, table := range drillTables {
		var want, got int64
		require.NoError(t, countRows(t, source, table, &want))
		require.NoError(t, countRows(t, restored, table, &got))
		assert.Equal(t, want, got, "表 %s 恢复后行数不一致", table)
	}

	// 迁移版本必须一致，否则还原后的库结构与代码不同步。
	assert.Equal(t, migrationVersion(t, source), migrationVersion(t, restored),
		"恢复后的迁移版本必须与源库一致")
}

// internalDSN 把宿主映射地址改写为容器内地址。
//
// 测试从宿主连容器（localhost:<映射端口>），而 pg_dump 是在容器里执行的，
// 容器内的 localhost:5432 才是 PostgreSQL 自己。不改写只会得到 connection refused。
func internalDSN(hostDSN string) string {
	return fmt.Sprintf("postgres://%s:%s@localhost:5432/%s?sslmode=disable",
		postgresUser, postgresPassword, databaseName(hostDSN))
}

// databaseName 从连接串中取出数据库名。
//
// 必须按最后一个 "/" 切分：DSN 的 scheme 部分（postgres://）也含斜杠，
// 从第一个斜杠切会把用户信息和端口一起当成库名。
func databaseName(dsn string) string {
	idx := strings.LastIndex(dsn, "/")
	if idx < 0 {
		return ""
	}
	name, _, _ := strings.Cut(dsn[idx+1:], "?")
	return name
}

// drillTables 是演练中比对行数的表。
var drillTables = []string{
	"users", "tenants", "memberships", "invitations",
	"roles", "permissions", "role_permissions", "membership_roles",
	"casbin_rule", "audit_logs", "policy_version",
}

// seedForDrill 在每个表中播种若干行，使行数比对有意义。
//
// 直接写 SQL 而不是走业务接口：演练关注的是"备份恢复不丢数据"，
// 不需要顺便验证一遍业务链路。
func seedForDrill(t *testing.T, dsn string) {
	t.Helper()

	db, err := openGormForDSN(t, dsn)
	require.NoError(t, err)

	stmts := []string{
		`INSERT INTO users (id, zitadel_user_id, display_name)
		 VALUES (gen_random_uuid(), 'drill-user', '演练用户')`,
		`INSERT INTO tenants (id, name, slug, created_by)
		 SELECT gen_random_uuid(), '演练租户', 'drill', u.id FROM users u
		 WHERE u.zitadel_user_id = 'drill-user'`,
		`INSERT INTO memberships (id, tenant_id, user_id)
		 SELECT gen_random_uuid(), t.id, u.id FROM tenants t, users u
		 WHERE t.slug = 'drill' AND u.zitadel_user_id = 'drill-user'`,
		`INSERT INTO permissions (id, code, resource, action)
		 VALUES (gen_random_uuid(), 'drill:read', 'drill', 'read')`,
	}
	for _, stmt := range stmts {
		require.NoError(t, db.Exec(stmt).Error, "播种失败：%s", stmt)
	}
	require.NoError(t, db.Exec(
		`INSERT INTO audit_logs (id, tenant_id, actor_user_id, action, resource_type)
		 SELECT gen_random_uuid(), t.id, u.id, 'tenant.create', 'tenant'
		 FROM tenants t, users u WHERE t.slug = 'drill' AND u.zitadel_user_id = 'drill-user'`).Error)
	require.NoError(t, db.Exec(
		`INSERT INTO policy_version (id, version) VALUES (1, 1)
		 ON CONFLICT (id) DO UPDATE SET version = policy_version.version + 1`).Error)
}

// execInPostgres 在 PostgreSQL 容器内执行命令。
//
// 退出码非零即视为失败。注意 pg_dump/pg_restore 在部分失败时仍可能返回 0，
// 因此调用方还必须做数据比对，才能确认恢复真的可用。
func execInPostgres(t *testing.T, name string, args ...string) error {
	t.Helper()
	if pgContainer == nil {
		t.Skip("PostgreSQL 容器不可用，跳过备份恢复演练；请手动执行 deployments/scripts/drill.sh")
	}

	code, reader, err := pgContainer.Exec(context.Background(), append([]string{name}, args...))
	if err != nil {
		return fmt.Errorf("容器内执行 %s 失败：%w", name, err)
	}
	if code != 0 {
		output, _ := io.ReadAll(reader)
		return fmt.Errorf("容器内执行 %s 返回退出码 %d：%s", name, code, string(output))
	}
	return nil
}

// countRows 统计指定表的行数。
func countRows(t *testing.T, dsn, table string, out *int64) error {
	t.Helper()

	db, err := openGormForDSN(t, dsn)
	if err != nil {
		return err
	}
	return db.Raw(fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(out).Error
}

// migrationVersion 返回数据库当前的迁移版本。
func migrationVersion(t *testing.T, dsn string) int {
	t.Helper()

	db, err := openGormForDSN(t, dsn)
	if err != nil {
		return -1
	}
	var version int
	if err := db.Raw("SELECT version FROM schema_migrations").Scan(&version).Error; err != nil {
		return -1
	}
	return version
}
