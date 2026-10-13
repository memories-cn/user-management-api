package user

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewUser_RequiresZitadelUserID(t *testing.T) {
	_, err := NewUser("")

	require.ErrorIs(t, err, ErrInvalidZitadelUserID)
}

func TestNewUser_TrimsZitadelUserID(t *testing.T) {
	u, err := NewUser("  zitadel-001  ")

	require.NoError(t, err)
	assert.Equal(t, "zitadel-001", u.ZitadelUserID)
}

func TestNewUser_Defaults(t *testing.T) {
	before := time.Now()

	u, err := NewUser("zitadel-001")

	require.NoError(t, err)
	assert.NotEqual(t, [16]byte{}, u.ID, "必须生成本地 UUID 主键")
	assert.Equal(t, StatusActive, u.Status)
	assert.Empty(t, u.DisplayName)
	assert.Empty(t, u.AvatarURL)
	assert.False(t, u.CreatedAt.Before(before))
	assert.True(t, u.IsActive())
}

func TestUser_Disable(t *testing.T) {
	u, err := NewUser("zitadel-001")
	require.NoError(t, err)

	u.Disable()

	assert.Equal(t, StatusDisabled, u.Status)
	assert.False(t, u.IsActive())
}

func TestUser_DisableIsIdempotent(t *testing.T) {
	u, err := NewUser("zitadel-001")
	require.NoError(t, err)

	u.Disable()
	u.Disable()

	assert.Equal(t, StatusDisabled, u.Status)
}

func TestUser_Rename(t *testing.T) {
	u, err := NewUser("zitadel-001")
	require.NoError(t, err)

	assert.True(t, u.Rename("Alice"), "首次设置展示名称应视为变更")
	assert.Equal(t, "Alice", u.DisplayName)

	assert.False(t, u.Rename("Alice"), "相同值不应触发变更")

	assert.False(t, u.Rename("  "), "空白值不得覆盖已有展示名称")
	assert.Equal(t, "Alice", u.DisplayName)

	assert.True(t, u.Rename("Bob"))
	assert.Equal(t, "Bob", u.DisplayName)
}

func TestUser_SetAvatar(t *testing.T) {
	u, err := NewUser("zitadel-001")
	require.NoError(t, err)

	assert.True(t, u.SetAvatar("https://example.com/a.png"))
	assert.Equal(t, "https://example.com/a.png", u.AvatarURL)

	assert.False(t, u.SetAvatar("https://example.com/a.png"))
	assert.False(t, u.SetAvatar(""))
	assert.Equal(t, "https://example.com/a.png", u.AvatarURL)
}
