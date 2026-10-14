package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/iam-service/internal/bootstrap"
	"github.com/example/iam-service/internal/ports"
)

// TestRBAC_PolicyProjectionRollsBack 校验策略投影与业务写入共享同一个事务。
//
// 这是 docs/TDD.md 8.3 的核心要求：不能因为两个组件都用 GORM 就默认具备原子性。
// 残留的投影就是"权限幽灵"——业务已回滚但授权仍放行。
func TestRBAC_PolicyProjectionRollsBack(t *testing.T) {
	dsn := newDatabase(t)
	require.NoError(t, migrateUp(dsn))

	db, err := bootstrap.OpenDB(configFor(dsn))
	require.NoError(t, err)
	defer closeGorm(t, db)

	ctx := context.Background()
	policies := bootstrap.NewPolicyRepository(db)
	tx := bootstrap.NewTxManager(db)

	tenantID, roleID := uuid.NewString(), uuid.NewString()
	perms := []ports.PolicyPermission{{Resource: "member", Action: "invite"}}

	err = tx.WithinTx(ctx, func(txCtx context.Context) error {
		if err := policies.ReplaceRoleRules(txCtx, tenantID, roleID, perms); err != nil {
			return err
		}
		if _, err := policies.BumpVersion(txCtx); err != nil {
			return err
		}
		return errors.New("业务写入失败")
	})
	require.Error(t, err)

	var rules int64
	require.NoError(t, db.WithContext(ctx).
		Raw("SELECT COUNT(*) FROM casbin_rule WHERE v0 = ?", roleID).Scan(&rules).Error)
	assert.Zero(t, rules, "事务回滚后不得残留策略投影")

	version, err := policies.CurrentVersion(ctx)
	require.NoError(t, err)
	assert.Zero(t, version, "版本号递增必须随事务一起回滚")
}

// TestRBAC_PolicyProjectionMatchesCasbinModel 锁定 casbin_rule 的列映射约定。
//
// 投影是写入方（本项目）与读取方（gorm-adapter）之间的隐式契约，
// 一旦列顺序或 ptype 语义变化，授权结果会静默错误。
// 该用例用真实 enforcer 验证"写入后能被正确加载并判定"，把契约固定下来。
func TestRBAC_PolicyProjectionMatchesCasbinModel(t *testing.T) {
	dsn := newDatabase(t)
	require.NoError(t, migrateUp(dsn))

	db, err := bootstrap.OpenDB(configFor(dsn))
	require.NoError(t, err)
	defer closeGorm(t, db)

	ctx := context.Background()
	policies := bootstrap.NewPolicyRepository(db)
	tx := bootstrap.NewTxManager(db)

	tenantID, roleID, userID := uuid.NewString(), uuid.NewString(), uuid.NewString()

	require.NoError(t, tx.WithinTx(ctx, func(txCtx context.Context) error {
		if err := policies.ReplaceRoleRules(txCtx, tenantID, roleID,
			[]ports.PolicyPermission{{Resource: "member", Action: "invite"}}); err != nil {
			return err
		}
		if err := policies.ReplaceMemberRules(txCtx, tenantID, userID, []string{roleID}); err != nil {
			return err
		}
		_, err := policies.BumpVersion(txCtx)
		return err
	}))

	// 用全新 enforcer 从数据库加载，验证投影可被 gorm-adapter 正确解析。
	enforcer, err := bootstrap.NewEnforcer(db, tenancyModelPath)
	require.NoError(t, err)

	assertEnforce(t, enforcer, userID, tenantID, "member", "invite", true, "同租户同权限必须放行")
	assertEnforce(t, enforcer, userID, tenantID, "member", "remove", false, "未授予的权限必须拒绝")
	assertEnforce(t, enforcer, userID, "22222222-2222-2222-2222-222222222222", "member", "invite", false,
		"跨租户不得命中策略")
	assertEnforce(t, enforcer, uuid.NewString(), tenantID, "member", "invite", false,
		"未分配角色的用户必须拒绝")
}

// TestRBAC_DeleteMemberRulesRevokesImmediately 校验撤权会清除全部 g 规则。
func TestRBAC_DeleteMemberRulesRevokesImmediately(t *testing.T) {
	dsn := newDatabase(t)
	require.NoError(t, migrateUp(dsn))

	db, err := bootstrap.OpenDB(configFor(dsn))
	require.NoError(t, err)
	defer closeGorm(t, db)

	ctx := context.Background()
	policies := bootstrap.NewPolicyRepository(db)

	tenantID, roleID, userID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	require.NoError(t, policies.ReplaceMemberRules(ctx, tenantID, userID, []string{roleID, uuid.NewString()}))

	var before int64
	require.NoError(t, db.WithContext(ctx).
		Raw("SELECT COUNT(*) FROM casbin_rule WHERE ptype = 'g' AND v0 = ? AND v2 = ?", userID, tenantID).
		Scan(&before).Error)
	require.Equal(t, int64(2), before)

	require.NoError(t, policies.DeleteMemberRules(ctx, tenantID, userID))

	var after int64
	require.NoError(t, db.WithContext(ctx).
		Raw("SELECT COUNT(*) FROM casbin_rule WHERE ptype = 'g' AND v0 = ? AND v2 = ?", userID, tenantID).
		Scan(&after).Error)
	assert.Zero(t, after, "撤权必须清除该成员在本租户的全部 g 规则")
}

// TestRBAC_DeleteRoleRulesRemovesPoliciesAndAssignments 校验删除角色时两类规则都被清除。
func TestRBAC_DeleteRoleRulesRemovesPoliciesAndAssignments(t *testing.T) {
	dsn := newDatabase(t)
	require.NoError(t, migrateUp(dsn))

	db, err := bootstrap.OpenDB(configFor(dsn))
	require.NoError(t, err)
	defer closeGorm(t, db)

	ctx := context.Background()
	policies := bootstrap.NewPolicyRepository(db)

	tenantID, roleID, userID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	require.NoError(t, policies.ReplaceRoleRules(ctx, tenantID, roleID,
		[]ports.PolicyPermission{{Resource: "role", Action: "assign"}}))
	require.NoError(t, policies.ReplaceMemberRules(ctx, tenantID, userID, []string{roleID}))

	require.NoError(t, policies.DeleteRoleRules(ctx, tenantID, roleID))

	var remaining int64
	require.NoError(t, db.WithContext(ctx).
		Raw("SELECT COUNT(*) FROM casbin_rule WHERE v1 = ? OR v2 = ?", roleID, tenantID).
		Scan(&remaining).Error)
	assert.Zero(t, remaining, "删除角色不得留下悬空的 p 或 g 规则")
}
