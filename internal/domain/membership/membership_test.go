package membership

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMembership_RequiresTenantAndUser(t *testing.T) {
	_, err := NewMembership(uuid.Nil, uuid.New())
	require.ErrorIs(t, err, ErrInvalidTenantID)

	_, err = NewMembership(uuid.New(), uuid.Nil)
	require.ErrorIs(t, err, ErrInvalidUserID)
}

func TestNewMembership_Defaults(t *testing.T) {
	tenantID, userID := uuid.New(), uuid.New()
	before := time.Now()

	m, err := NewMembership(tenantID, userID)

	require.NoError(t, err)
	assert.Equal(t, tenantID, m.TenantID)
	assert.Equal(t, userID, m.UserID)
	assert.Equal(t, StatusActive, m.Status)
	assert.True(t, m.IsActive())
	require.NotNil(t, m.JoinedAt)
	assert.False(t, m.JoinedAt.Before(before))
}

func TestMembership_SuspendAndActivate(t *testing.T) {
	m, err := NewMembership(uuid.New(), uuid.New())
	require.NoError(t, err)

	m.Suspend()
	assert.Equal(t, StatusSuspended, m.Status)
	assert.False(t, m.IsActive())

	m.Activate()
	assert.Equal(t, StatusActive, m.Status)
	assert.True(t, m.IsActive())
}

func TestMembership_RemoveIsIdempotent(t *testing.T) {
	m, err := NewMembership(uuid.New(), uuid.New())
	require.NoError(t, err)

	m.Remove()
	m.Remove()

	assert.Equal(t, StatusRemoved, m.Status)
	assert.False(t, m.IsActive())
}

func TestMembership_Rejoin(t *testing.T) {
	m, err := NewMembership(uuid.New(), uuid.New())
	require.NoError(t, err)
	firstJoined := m.JoinedAt

	m.Remove()
	assert.Nil(t, m.JoinedAt, "移除后不再保留加入时间")

	time.Sleep(time.Millisecond)
	m.Rejoin(time.Now())

	assert.Equal(t, StatusActive, m.Status)
	require.NotNil(t, m.JoinedAt)
	assert.NotEqual(t, firstJoined, m.JoinedAt, "重新加入应刷新 joined_at")
}
