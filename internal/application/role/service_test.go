package role

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/iam-service/internal/domain/role"
	"github.com/example/iam-service/internal/rbactest"
)

// newTestService 用内存替身装配角色用例。
func newTestService(codes ...string) (*Service, *rbactest.Fixture) {
	fix := rbactest.NewFixture(codes...)
	svc := NewService(fix.Roles, fix.Permissions, fix.Policies, fix.Tx, fix.Reloader, nil)
	return svc, fix
}

// seedRole 在指定租户下创建一个自定义角色。
func seedRole(t *testing.T, svc *Service, tenantID uuid.UUID, key string) *role.Role {
	t.Helper()
	rl, err := svc.CreateRole(context.Background(), tenantID.String(), key, "展示名", "", uuid.New())
	require.NoError(t, err)
	return rl
}

func TestCreateRole_CreatesCustomRole(t *testing.T) {
	svc, _ := newTestService()
	tenantID := uuid.New()

	rl, err := svc.CreateRole(context.Background(), tenantID.String(), "billing_manager", "账单管理员", "管理账单", uuid.New())

	require.NoError(t, err)
	assert.Equal(t, "billing_manager", rl.Key)
	assert.False(t, rl.IsSystem)
}

func TestCreateRole_RejectsSystemKey(t *testing.T) {
	svc, _ := newTestService()
	tenantID := uuid.New()

	_, err := svc.CreateRole(context.Background(), tenantID.String(), role.KeyOwner, "所有者", "", uuid.New())

	require.ErrorIs(t, err, ErrInvalidRoleKey, "自定义角色不得冒用系统预置标识")
}

func TestCreateRole_RejectsDuplicateKey(t *testing.T) {
	svc, _ := newTestService()
	tenantID := uuid.New()
	seedRole(t, svc, tenantID, "billing_manager")

	_, err := svc.CreateRole(context.Background(), tenantID.String(), "BILLING_MANAGER", "重复", "", uuid.New())

	require.ErrorIs(t, err, ErrRoleKeyConflict, "同租户内标识冲突必须被拒绝且大小写不敏感")
}

func TestCreateRole_RejectsInvalidKey(t *testing.T) {
	svc, _ := newTestService()
	tenantID := uuid.New()

	_, err := svc.CreateRole(context.Background(), tenantID.String(), "Bad Key", "非法", "", uuid.New())

	require.ErrorIs(t, err, role.ErrInvalidKey)
}

func TestGetRole_IsTenantScoped(t *testing.T) {
	svc, _ := newTestService()
	tenantID, otherTenant := uuid.New(), uuid.New()
	rl := seedRole(t, svc, tenantID, "billing_manager")

	_, err := svc.GetRole(context.Background(), otherTenant.String(), rl.ID.String())

	require.ErrorIs(t, err, ErrRoleNotFound, "跨租户读取角色必须表现为不存在")
}

func TestDeleteRole_RejectsSystemRole(t *testing.T) {
	svc, fix := newTestService()
	tenantID := uuid.New()
	system := rbactest.SeedSystemRole(fix.Roles, tenantID, role.KeyOwner)

	err := svc.DeleteRole(context.Background(), tenantID.String(), system.ID.String(), uuid.New())

	require.ErrorIs(t, err, role.ErrSystemRoleImmutable)
}

func TestDeleteRole_RejectsRoleInUse(t *testing.T) {
	svc, fix := newTestService()
	tenantID := uuid.New()
	rl := seedRole(t, svc, tenantID, "billing_manager")

	// 登记一个仍在使用该角色的成员。
	membershipID, userID := uuid.NewString(), uuid.NewString()
	fix.MembershipRoles.RegisterMembership(membershipID, tenantID.String(), userID, true)
	assignment, err := role.NewAssignment(tenantID, uuid.MustParse(membershipID), rl.ID, uuid.New())
	require.NoError(t, err)
	require.NoError(t, fix.MembershipRoles.Replace(context.Background(), tenantID.String(), membershipID,
		[]*role.Assignment{assignment}))

	err = svc.DeleteRole(context.Background(), tenantID.String(), rl.ID.String(), uuid.New())

	require.ErrorIs(t, err, ErrRoleInUse)
}

func TestDeleteRole_RemovesPolicyAndBumpsVersion(t *testing.T) {
	svc, fix := newTestService("member:read")
	tenantID := uuid.New()
	rl := seedRole(t, svc, tenantID, "billing_manager")
	_, err := svc.SetRolePermissions(context.Background(), tenantID.String(), rl.ID.String(), []string{"member:read"}, uuid.New())
	require.NoError(t, err)

	require.NoError(t, svc.DeleteRole(context.Background(), tenantID.String(), rl.ID.String(), uuid.New()))

	assert.Empty(t, fix.Policies.RoleRules(rl.ID.String()), "删除角色必须清除其策略投影")
	assert.Equal(t, 2, fix.Reloader.Count(), "权限设置与删除各刷新一次")
}

func TestSetRolePermissions_WritesProjection(t *testing.T) {
	svc, fix := newTestService("member:read", "member:invite")
	tenantID := uuid.New()
	rl := seedRole(t, svc, tenantID, "billing_manager")

	perms, err := svc.SetRolePermissions(context.Background(), tenantID.String(), rl.ID.String(),
		[]string{"member:read", "member:invite"}, uuid.New())

	require.NoError(t, err)
	require.Len(t, perms, 2)
	rules := fix.Policies.RoleRules(rl.ID.String())
	require.Len(t, rules, 2)
	assert.Equal(t, "member", rules[0].Resource)
	assert.Equal(t, "read", rules[0].Action)
	assert.Equal(t, 1, fix.Reloader.Count(), "提交后必须刷新本地内存策略")
}

func TestSetRolePermissions_RejectsUnknownPermission(t *testing.T) {
	svc, fix := newTestService("member:read")
	tenantID := uuid.New()
	rl := seedRole(t, svc, tenantID, "billing_manager")

	_, err := svc.SetRolePermissions(context.Background(), tenantID.String(), rl.ID.String(),
		[]string{"member:read", "member:self_destruct"}, uuid.New())

	require.ErrorIs(t, err, ErrPermissionNotFound, "目录外权限必须整次拒绝")
	assert.Empty(t, fix.Policies.RoleRules(rl.ID.String()), "不得部分应用")
}

func TestSetRolePermissions_RejectsMalformedCode(t *testing.T) {
	svc, fix := newTestService("member:read")
	tenantID := uuid.New()
	rl := seedRole(t, svc, tenantID, "billing_manager")

	_, err := svc.SetRolePermissions(context.Background(), tenantID.String(), rl.ID.String(), []string{"garbage"}, uuid.New())

	require.Error(t, err)
	assert.Empty(t, fix.Policies.RoleRules(rl.ID.String()), "非法代码不得写入任何策略")
}

func TestSetRolePermissions_RollsBackWhenBumpFails(t *testing.T) {
	svc, fix := newTestService("member:read")
	tenantID := uuid.New()
	rl := seedRole(t, svc, tenantID, "billing_manager")
	fix.Policies.SetBumpError(errors.New("版本号递增失败"))

	_, err := svc.SetRolePermissions(context.Background(), tenantID.String(), rl.ID.String(), []string{"member:read"}, uuid.New())

	require.Error(t, err, "提交阶段失败必须向上冒泡，不能假装成功")
	assert.Equal(t, 0, fix.Reloader.Count(), "回滚后不得刷新策略")
}

// fullCatalog 与迁移种子（000006）保持一致，供预置角色测试使用。
var fullCatalog = []string{
	"tenant:read", "tenant:update", "tenant:delete",
	"member:read", "member:invite", "member:update", "member:remove",
	"role:read", "role:create", "role:update", "role:delete", "role:assign",
	"permission:read", "permission:assign", "audit:read",
}

func TestEnsureSystemRoles_IsIdempotent(t *testing.T) {
	svc, fix := newTestService(fullCatalog...)
	tenantID := uuid.New()

	require.NoError(t, svc.EnsureSystemRoles(context.Background(), tenantID))
	require.NoError(t, svc.EnsureSystemRoles(context.Background(), tenantID))

	result, err := svc.ListRoles(context.Background(), tenantID.String(), 0, 0)
	require.NoError(t, err)
	assert.Len(t, result.Roles, 4, "重复预置不得产生重复角色")

	// 预置角色应带默认权限投影。
	for _, rl := range result.Roles {
		assert.NotEmpty(t, fix.Policies.RoleRules(rl.ID.String()), "角色 %s 必须有权限投影", rl.Key)
	}
}

func TestEnsureSystemRoles_FailsWhenPresetMissing(t *testing.T) {
	// 权限目录为空，预置权限必然缺失于目录。
	svc, _ := newTestService()
	tenantID := uuid.New()

	err := svc.EnsureSystemRoles(context.Background(), tenantID)

	require.ErrorIs(t, err, ErrPresetPermissionMissing)
}
