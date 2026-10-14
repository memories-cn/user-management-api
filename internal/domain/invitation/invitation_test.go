package invitation

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testTTL = 7 * 24 * time.Hour

func TestNewInvitation_NormalizesEmail(t *testing.T) {
	inv, token, err := NewInvitation(uuid.New(), "  Alice@Example.COM ", uuid.New(), testTTL)

	require.NoError(t, err)
	assert.Equal(t, "alice@example.com", inv.Email)
	assert.NotEmpty(t, token)
	assert.NotEqual(t, token, string(inv.TokenHash), "明文令牌不得直接存入实体")
	assert.Equal(t, 43, len(token), "32 字节随机值的 base64url 长度应为 43")
}

func TestNewInvitation_RequiresValidInput(t *testing.T) {
	tenantID, userID := uuid.New(), uuid.New()

	_, _, err := NewInvitation(uuid.Nil, "a@b.com", userID, testTTL)
	require.ErrorIs(t, err, ErrInvalidTenantID)

	_, _, err = NewInvitation(tenantID, "not-an-email", userID, testTTL)
	require.ErrorIs(t, err, ErrInvalidEmail)

	_, _, err = NewInvitation(tenantID, "a@b.com", uuid.Nil, testTTL)
	require.Error(t, err)

	_, _, err = NewInvitation(tenantID, "a@b.com", userID, 0)
	require.ErrorIs(t, err, ErrInvalidTTL)
}

func TestInvitation_Matches(t *testing.T) {
	inv, token, err := NewInvitation(uuid.New(), "a@b.com", uuid.New(), testTTL)
	require.NoError(t, err)

	assert.True(t, inv.Matches(token))
	assert.False(t, inv.Matches(token+"x"))
	assert.False(t, inv.Matches(""))
}

func TestInvitation_PendingAndExpired(t *testing.T) {
	inv, _, err := NewInvitation(uuid.New(), "a@b.com", uuid.New(), testTTL)
	require.NoError(t, err)

	now := time.Now()
	assert.True(t, inv.IsPending(now))
	assert.False(t, inv.IsExpired(now))

	assert.False(t, inv.IsExpired(now.Add(testTTL-time.Minute)), "到期前一分钟仍有效")
	assert.True(t, inv.IsExpired(now.Add(testTTL+time.Minute)), "超过有效期即过期")
}

func TestInvitation_Accept(t *testing.T) {
	inv, token, err := NewInvitation(uuid.New(), "a@b.com", uuid.New(), testTTL)
	require.NoError(t, err)

	userID := uuid.New()
	now := time.Now()
	require.NoError(t, inv.Accept(userID, now))

	assert.Equal(t, StatusAccepted, inv.Status)
	require.NotNil(t, inv.AcceptedUserID)
	assert.Equal(t, userID, *inv.AcceptedUserID)
	require.NotNil(t, inv.AcceptedAt)
	assert.False(t, inv.IsPending(now))
	assert.True(t, inv.Matches(token), "接受后仍应保留令牌哈希以便审计")
}

func TestInvitation_AcceptRejectsNonPending(t *testing.T) {
	inv, _, err := NewInvitation(uuid.New(), "a@b.com", uuid.New(), testTTL)
	require.NoError(t, err)

	inv.Cancel()
	err = inv.Accept(uuid.New(), time.Now())

	require.ErrorIs(t, err, ErrNotPending)
}

func TestInvitation_AcceptRejectsExpired(t *testing.T) {
	inv, _, err := NewInvitation(uuid.New(), "a@b.com", uuid.New(), time.Hour)
	require.NoError(t, err)

	err = inv.Accept(uuid.New(), time.Now().Add(2*time.Hour))

	require.ErrorIs(t, err, ErrExpired)
}

func TestInvitation_Cancel(t *testing.T) {
	inv, _, err := NewInvitation(uuid.New(), "a@b.com", uuid.New(), testTTL)
	require.NoError(t, err)

	require.NoError(t, inv.Cancel())
	assert.Equal(t, StatusCancelled, inv.Status)

	require.ErrorIs(t, inv.Cancel(), ErrNotPending, "重复取消必须报错而不是静默成功")
}

func TestInvitation_Refresh(t *testing.T) {
	inv, _, err := NewInvitation(uuid.New(), "a@b.com", uuid.New(), testTTL)
	require.NoError(t, err)
	old := inv.ExpiresAt

	time.Sleep(time.Millisecond)
	inv.Refresh(time.Now(), testTTL)

	assert.True(t, inv.ExpiresAt.After(old), "幂等复用时必须刷新有效期")
	assert.Equal(t, StatusPending, inv.Status)
}

func TestInvitation_TokenUniqueness(t *testing.T) {
	first, _, err := NewInvitation(uuid.New(), "a@b.com", uuid.New(), testTTL)
	require.NoError(t, err)
	second, _, err := NewInvitation(uuid.New(), "a@b.com", uuid.New(), testTTL)
	require.NoError(t, err)

	assert.False(t, strings.EqualFold(string(first.TokenHash), string(second.TokenHash)),
		"两次邀请必须生成不同的令牌")
}
