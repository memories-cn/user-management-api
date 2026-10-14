package role

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRole(t *testing.T) {
	tenantID := uuid.New()
	r, err := NewRole(tenantID, "billing_manager", "账单管理员", "管理账单")
	require.NoError(t, err)
	assert.Equal(t, tenantID, r.TenantID)
	assert.Equal(t, "billing_manager", r.Key)
	assert.False(t, r.IsSystem)
}

func TestNewRoleMarksSystemKeys(t *testing.T) {
	tenantID := uuid.New()
	for _, key := range []string{KeyOwner, KeyAdmin, KeyMember, KeyViewer} {
		r, err := NewRole(tenantID, key, "展示名", "")
		require.NoError(t, err)
		assert.True(t, r.IsSystem, "预置角色 %s 必须标记为系统角色", key)
	}
}

func TestNewRoleRejectsInvalidInput(t *testing.T) {
	tenantID := uuid.New()
	tests := []struct {
		name     string
		tenantID uuid.UUID
		key      string
		roleName string
		wantErr  error
	}{
		{"租户为空", uuid.Nil, "custom", "自定义", ErrInvalidTenantID},
		{"标识为空", tenantID, "", "自定义", ErrInvalidKey},
		{"标识含连字符", tenantID, "cus-tom", "自定义", ErrInvalidKey},
		{"标识以数字开头", tenantID, "1custom", "自定义", ErrInvalidKey},
		{"标识含冒号", tenantID, "cus:tom", "自定义", ErrInvalidKey},
		{"名称为空", tenantID, "custom", "   ", ErrInvalidName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRole(tt.tenantID, tt.key, tt.roleName, "")
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestNewSystemRoleRejectsCustomKey(t *testing.T) {
	_, err := NewSystemRole(uuid.New(), "custom", "自定义", "")
	require.ErrorIs(t, err, ErrInvalidKey)
}

func TestRenameAndSetDescriptionReturnChangeFlag(t *testing.T) {
	tenantID := uuid.New()
	r, err := NewRole(tenantID, "custom", "原名", "原说明")
	require.NoError(t, err)

	assert.False(t, r.Rename("   "), "空白名称不得覆盖已有值")
	assert.False(t, r.Rename("原名"), "同名不视为变更")
	assert.True(t, r.Rename("新名"))
	assert.Equal(t, "新名", r.Name)

	assert.False(t, r.SetDescription("原说明"))
	assert.True(t, r.SetDescription("新说明"))
	assert.Equal(t, "新说明", r.Description)
}

func TestEnsureDeletable(t *testing.T) {
	tenantID := uuid.New()

	system, err := NewRole(tenantID, KeyOwner, "所有者", "")
	require.NoError(t, err)
	require.ErrorIs(t, system.EnsureDeletable(), ErrSystemRoleImmutable)

	custom, err := NewRole(tenantID, "custom", "自定义", "")
	require.NoError(t, err)
	require.NoError(t, custom.EnsureDeletable())
}

func TestNormalizeKeyAndIsSystemKey(t *testing.T) {
	assert.Equal(t, "owner", NormalizeKey("  OWNER "))
	assert.True(t, IsSystemKey("Owner"))
	assert.False(t, IsSystemKey("billing_manager"))
}

func TestNewAssignment(t *testing.T) {
	tenantID, membershipID, roleID, actorID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	a, err := NewAssignment(tenantID, membershipID, roleID, actorID)
	require.NoError(t, err)
	assert.Equal(t, tenantID, a.TenantID)
	assert.Equal(t, membershipID, a.MembershipID)
	assert.Equal(t, roleID, a.RoleID)
	assert.Equal(t, actorID, a.AssignedBy)
}

func TestNewAssignmentRejectsMissingIdentifiers(t *testing.T) {
	tenantID, membershipID, roleID, actorID := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	_, err := NewAssignment(uuid.Nil, membershipID, roleID, actorID)
	require.ErrorIs(t, err, ErrInvalidMembership)

	_, err = NewAssignment(tenantID, uuid.Nil, roleID, actorID)
	require.ErrorIs(t, err, ErrInvalidMembership)

	_, err = NewAssignment(tenantID, membershipID, uuid.Nil, actorID)
	require.ErrorIs(t, err, ErrInvalidRole)

	_, err = NewAssignment(tenantID, membershipID, roleID, uuid.Nil)
	require.ErrorIs(t, err, ErrInvalidAssigner)
}
