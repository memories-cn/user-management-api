package permission

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPermission(t *testing.T) {
	p, err := NewPermission("member", "invite", "邀请成员加入租户")
	require.NoError(t, err)
	assert.Equal(t, "member:invite", p.Code)
	assert.Equal(t, "member", p.Resource)
	assert.Equal(t, "invite", p.Action)
	assert.Equal(t, "邀请成员加入租户", p.Description)
	assert.NotEqual(t, "", p.ID.String())
}

func TestNewPermissionRejectsInvalidIdentifiers(t *testing.T) {
	tests := []struct {
		name     string
		resource string
		action   string
		wantErr  error
	}{
		{"资源为空", "", "read", ErrInvalidResource},
		{"资源含冒号", "mem:ber", "read", ErrInvalidResource},
		{"资源以数字开头", "1member", "read", ErrInvalidResource},
		{"操作为空", "member", "", ErrInvalidAction},
		{"操作含连字符", "member", "in-vite", ErrInvalidAction},
		{"操作含冒号", "member", "in:vite", ErrInvalidAction},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewPermission(tt.resource, tt.action, "")
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestNewPermissionNormalizesCase(t *testing.T) {
	p, err := NewPermission("  Member ", " Invite ", "")
	require.NoError(t, err)
	assert.Equal(t, "member:invite", p.Code)
}

func TestParseCode(t *testing.T) {
	resource, action, err := ParseCode("role:assign")
	require.NoError(t, err)
	assert.Equal(t, "role", resource)
	assert.Equal(t, "assign", action)
}

func TestParseCodeRejectsMalformed(t *testing.T) {
	// 注意：ParseCode 先归一化大小写，因此 "Member:read" 等价于 "member:read"。
	tests := []string{"", "member", "member:", ":invite", "member:in:vite", "member:1nvite"}
	for _, code := range tests {
		t.Run("code="+code, func(t *testing.T) {
			_, _, err := ParseCode(code)
			require.ErrorIs(t, err, ErrInvalidCode)
		})
	}
}

func TestValidateCodes(t *testing.T) {
	require.NoError(t, ValidateCodes([]string{"member:read", "role:assign"}))
	require.ErrorIs(t, ValidateCodes([]string{"member:read", "bad"}), ErrInvalidCode)
	require.NoError(t, ValidateCodes(nil))
}

func TestMatchesCode(t *testing.T) {
	p, err := NewPermission("member", "invite", "")
	require.NoError(t, err)
	assert.True(t, p.MatchesCode("member:invite"))
	assert.True(t, p.MatchesCode("  MEMBER:INVITE "))
	assert.False(t, p.MatchesCode("member:remove"))
}
