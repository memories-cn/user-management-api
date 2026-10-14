package integration

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	appaudit "github.com/example/iam-service/internal/application/audit"
	"github.com/example/iam-service/internal/bootstrap"
)

// TestAudit_RollsBackWithBusinessData 校验审计写入与业务写入共享同一个事务。
//
// 这是 docs/TDD.md 8.3 的核心要求：角色权限变更涉及业务关系表、
// Casbin 策略投影与审计记录，三者必须在同一事务内更新。
// 若审计独立写入，就会出现"权限已改但无留痕"或"操作失败却留下成功留痕"。
func TestAudit_RollsBackWithBusinessData(t *testing.T) {
	dsn := newDatabase(t)
	require.NoError(t, migrateUp(dsn))

	db, err := bootstrap.OpenDB(configFor(dsn))
	require.NoError(t, err)
	defer closeGorm(t, db)

	ctx := context.Background()
	audits := bootstrap.NewAuditRepository(db)
	auditSvc := appaudit.NewService(audits)
	tx := bootstrap.NewTxManager(db)

	actor := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	tenantID := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	// 审计写入失败：先手动写入一个操作者，再让第二次写入触发外键失败。
	require.NoError(t, db.WithContext(ctx).Exec(
		"INSERT INTO users (id, zitadel_user_id) VALUES (?, ?)", actor, "zitadel-audit-tx").Error)

	// 第一步：事务内成功写入一条审计。
	require.NoError(t, tx.WithinTx(ctx, func(txCtx context.Context) error {
		return auditSvc.RecordAction(txCtx, &tenantID, actor, "member.remove", "member", "user-1", "success", nil)
	}))

	var afterFirst int64
	require.NoError(t, db.WithContext(ctx).
		Raw("SELECT COUNT(*) FROM audit_logs WHERE tenant_id = ?", tenantID).Scan(&afterFirst).Error)
	require.Equal(t, int64(1), afterFirst)

	// 第二步：事务内写入后故意失败——不存在的操作者会触发外键约束错误。
	stranger := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	err = tx.WithinTx(ctx, func(txCtx context.Context) error {
		if err := auditSvc.RecordAction(txCtx, &tenantID, stranger,
			"role.assign", "member", "user-2", "success", nil); err != nil {
			return err
		}
		return errors.New("业务写入失败")
	})
	require.Error(t, err)

	var afterRollback int64
	require.NoError(t, db.WithContext(ctx).
		Raw("SELECT COUNT(*) FROM audit_logs WHERE tenant_id = ?", tenantID).Scan(&afterRollback).Error)
	assert.Equal(t, afterFirst, afterRollback, "事务回滚后不得残留审计记录")
}

// TestAudit_NestedTransactionReusesOuter 校验嵌套调用复用外层事务。
//
// GORM 的 db.Transaction 只在句柄本身已是事务时才用保存点；
// 若每次都开新事务，内层会先提交，外层回滚就带不走它——原子性形同虚设。
func TestAudit_NestedTransactionReusesOuter(t *testing.T) {
	dsn := newDatabase(t)
	require.NoError(t, migrateUp(dsn))

	db, err := bootstrap.OpenDB(configFor(dsn))
	require.NoError(t, err)
	defer closeGorm(t, db)

	ctx := context.Background()
	audits := bootstrap.NewAuditRepository(db)
	auditSvc := appaudit.NewService(audits)
	tx := bootstrap.NewTxManager(db)

	actor := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	tenantID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	require.NoError(t, db.WithContext(ctx).Exec(
		"INSERT INTO users (id, zitadel_user_id) VALUES (?, ?)", actor, "zitadel-nested-tx").Error)

	// 外层失败，内层（嵌套事务）写入的审计必须一并回滚。
	err = tx.WithinTx(ctx, func(outerCtx context.Context) error {
		inner := func(innerCtx context.Context) error {
			return auditSvc.RecordAction(innerCtx, &tenantID, actor,
				"tenant.suspend", "tenant", tenantID.String(), "success", nil)
		}
		if err := tx.WithinTx(outerCtx, inner); err != nil {
			return err
		}
		return errors.New("外层失败")
	})
	require.Error(t, err)

	var count int64
	require.NoError(t, db.WithContext(ctx).
		Raw("SELECT COUNT(*) FROM audit_logs WHERE tenant_id = ?", tenantID).Scan(&count).Error)
	assert.Zero(t, count, "内层事务必须复用外层事务，否则会独立提交")
}

// TestAudit_AdminApiRequiresPlatformRole 校验平台状态接口的完整权限链。
func TestAudit_AdminApiRequiresPlatformRole(t *testing.T) {
	stack := newTenancyStack(t)

	// 未认证。
	rec := stack.do(http.MethodPatch, "/api/v1/platform/tenants/11111111-1111-1111-1111-111111111111/status",
		"", map[string]string{"status": "suspended"})
	require.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())

	// 已认证但无平台角色。
	rec = stack.do(http.MethodPatch, "/api/v1/platform/tenants/11111111-1111-1111-1111-111111111111/status",
		stack.userToken("normal", "normal@example.com"), map[string]string{"status": "suspended"})
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
}
