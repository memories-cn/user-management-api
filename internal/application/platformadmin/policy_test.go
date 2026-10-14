package platformadmin

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/iam-service/internal/domain/identity"
)

func TestNewPolicy_RequiresRoleKey(t *testing.T) {
	_, err := NewPolicy("")

	require.ErrorIs(t, err, ErrInvalidRoleKey)
}

func TestIsPlatformAdmin_RoleMatch(t *testing.T) {
	policy, err := NewPolicy("platform_admin")
	require.NoError(t, err)

	assert.True(t, policy.IsPlatformAdmin(identity.Principal{
		Subject:       "zitadel-001",
		Authenticated: true,
		Roles:         []string{"platform_admin"},
	}))
}

func TestIsPlatformAdmin_RoleMismatch(t *testing.T) {
	policy, err := NewPolicy("platform_admin")
	require.NoError(t, err)

	assert.False(t, policy.IsPlatformAdmin(identity.Principal{
		Subject:       "zitadel-001",
		Authenticated: true,
		Roles:         []string{"member", "admin"},
	}), "租户级角色不得授予平台权限")
}

func TestIsPlatformAdmin_NoRoles(t *testing.T) {
	policy, err := NewPolicy("platform_admin")
	require.NoError(t, err)

	assert.False(t, policy.IsPlatformAdmin(identity.Principal{
		Subject:       "zitadel-001",
		Authenticated: true,
	}), "令牌未携带角色声明时必须拒绝")
}

func TestIsPlatformAdmin_Unauthenticated(t *testing.T) {
	policy, err := NewPolicy("platform_admin")
	require.NoError(t, err)

	assert.False(t, policy.IsPlatformAdmin(identity.Principal{
		Roles: []string{"platform_admin"},
	}), "未认证身份即使带角色也不得放行")
}

func TestIsPlatformAdmin_SimilarRoleName(t *testing.T) {
	policy, err := NewPolicy("platform_admin")
	require.NoError(t, err)

	assert.False(t, policy.IsPlatformAdmin(identity.Principal{
		Authenticated: true,
		Roles:         []string{"platform_admin_viewer", "PLATFORM_ADMIN"},
	}), "角色名必须精确匹配，大小写敏感")
}
