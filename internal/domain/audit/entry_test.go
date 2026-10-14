package audit

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEntry_ValidatesInvariants(t *testing.T) {
	tenantID, actor := uuid.New(), uuid.New()

	entry, err := NewEntry(&tenantID, actor, ActionMemberRemove, "member", "user-1", ResultSuccess)
	require.NoError(t, err)
	assert.Equal(t, ActionMemberRemove, entry.Action)
	assert.Equal(t, ResultSuccess, entry.Result)
	assert.Equal(t, "user-1", entry.ResourceID)
	assert.NotNil(t, entry.TenantID)
}

func TestNewEntry_RejectsMissingActor(t *testing.T) {
	tenantID := uuid.New()

	_, err := NewEntry(&tenantID, uuid.Nil, ActionMemberRemove, "member", "user-1", ResultSuccess)

	require.ErrorIs(t, err, ErrInvalidActor, "无法归因的操作没有审计价值")
}

func TestNewEntry_AllowsPlatformLevelOperation(t *testing.T) {
	// 平台级操作不属于任何租户，tenantID 为 nil 是合法输入。
	entry, err := NewEntry(nil, uuid.New(), ActionTenantSuspend, "tenant", "t-1", ResultSuccess)

	require.NoError(t, err)
	assert.Nil(t, entry.TenantID)
}

func TestNewEntry_RejectsUnknownAction(t *testing.T) {
	tenantID := uuid.New()

	_, err := NewEntry(&tenantID, uuid.New(), Action("tenant.self_destruct"), "tenant", "t-1", ResultSuccess)

	require.ErrorIs(t, err, ErrInvalidAction, "未登记的操作类型不得进入审计表")
}

func TestNewEntry_RejectsEmptyResourceType(t *testing.T) {
	tenantID := uuid.New()

	_, err := NewEntry(&tenantID, uuid.New(), ActionMemberRemove, "  ", "user-1", ResultSuccess)

	require.ErrorIs(t, err, ErrInvalidResourceType)
}

func TestNewEntry_RejectsUnknownResult(t *testing.T) {
	tenantID := uuid.New()

	_, err := NewEntry(&tenantID, uuid.New(), ActionMemberRemove, "member", "user-1", Result("maybe"))

	require.ErrorIs(t, err, ErrInvalidResult)
}

func TestWithMetadata_RejectsCredentialLikeKeys(t *testing.T) {
	// docs/TDD.md 3.7：metadata 不得包含令牌、密码等敏感认证材料。
	cases := []string{
		"access_token",
		"refresh_token",
		"password",
		"Authorization",
		"client_secret",
		"cookie",
		"private_key",
	}
	for _, key := range cases {
		t.Run(key, func(t *testing.T) {
			entry := mustEntry(t)
			err := entry.WithMetadata(map[string]any{key: "sensitive-value"})

			require.ErrorIs(t, err, ErrSensitiveMetadata)
		})
	}
}

func TestWithMetadata_RejectsNonScalarValues(t *testing.T) {
	entry := mustEntry(t)

	err := entry.WithMetadata(map[string]any{"details": map[string]any{"nested": 1}})

	require.ErrorIs(t, err, ErrUnsupportedMetadataValue,
		"结构体序列化时容易带出内部字段，只接受标量")
}

func TestWithMetadata_AcceptsScalars(t *testing.T) {
	entry := mustEntry(t)

	err := entry.WithMetadata(map[string]any{
		"role_key":         "owner",
		"permission_count": 3,
		"from_status":      "active",
		"reused":           false,
	})

	require.NoError(t, err)
	assert.Equal(t, "owner", entry.Metadata["role_key"])
	assert.Equal(t, 3, entry.Metadata["permission_count"])
}

func TestWithRequestContext_TruncatesUserAgent(t *testing.T) {
	entry := mustEntry(t)
	long := make([]byte, maxUserAgentLength+100)
	for i := range long {
		long[i] = 'a'
	}

	entry.WithRequestContext(RequestContext{
		RequestID: "req-1",
		IPAddress: "203.0.113.7",
		UserAgent: string(long),
	})

	assert.Equal(t, "req-1", entry.RequestID)
	assert.Equal(t, "203.0.113.7", entry.IPAddress)
	assert.Len(t, entry.UserAgent, maxUserAgentLength, "超长请求头必须截断，避免污染审计表")
}

func TestAction_IsValid(t *testing.T) {
	valid := []Action{
		ActionMemberInvite, ActionMemberRemove, ActionMemberStatusChange,
		ActionRoleAssign, ActionRoleCreate, ActionRoleUpdate, ActionRoleDelete,
		ActionRolePermissionUpdate,
		ActionTenantCreate, ActionTenantSuspend, ActionTenantActivate,
	}
	for _, a := range valid {
		assert.True(t, a.IsValid(), "%s 应当是合法操作类型", a)
	}
	assert.False(t, Action("").IsValid())
	assert.False(t, Action("member.explode").IsValid())
}

func TestResult_IsValid(t *testing.T) {
	assert.True(t, ResultSuccess.IsValid())
	assert.True(t, ResultDenied.IsValid())
	assert.True(t, ResultError.IsValid())
	assert.False(t, Result("").IsValid())
}

// mustEntry 构造一条合法的审计记录。
func mustEntry(t *testing.T) *Entry {
	t.Helper()

	tenantID := uuid.New()
	entry, err := NewEntry(&tenantID, uuid.New(), ActionMemberRemove, "member", "user-1", ResultSuccess)
	require.NoError(t, err)
	return entry
}
