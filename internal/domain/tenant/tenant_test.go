package tenant

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTenant_RequiresName(t *testing.T) {
	_, err := NewTenant("  ", "acme", uuid.New())

	require.ErrorIs(t, err, ErrInvalidName)
}

func TestNewTenant_RequiresCreator(t *testing.T) {
	_, err := NewTenant("Acme", "acme", uuid.Nil)

	require.Error(t, err)
}

func TestNewTenant_Defaults(t *testing.T) {
	creator := uuid.New()
	before := time.Now()

	tn, err := NewTenant("  Acme Inc  ", "Acme-1", creator)

	require.NoError(t, err)
	assert.Equal(t, "Acme Inc", tn.Name)
	assert.Equal(t, "acme-1", tn.Slug, "slug 必须归一化为小写")
	assert.Equal(t, StatusActive, tn.Status)
	assert.Equal(t, creator, tn.CreatedBy)
	assert.True(t, tn.IsActive())
	assert.False(t, tn.CreatedAt.Before(before))
}

func TestNewTenant_RejectsInvalidSlug(t *testing.T) {
	cases := map[string]string{
		"下划线":    "acme_1",
		"以连字符开头": "-acme",
		"以连字符结尾": "acme-",
		"含空格":    "acme 1",
		"空值":     "",
		"超长":     repeat('a', 65),
	}
	for name, slug := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewTenant("Acme", slug, uuid.New())
			require.ErrorIs(t, err, ErrInvalidSlug)
		})
	}
}

func TestNewTenant_NormalizesUppercaseSlug(t *testing.T) {
	// 大写会被归一化后接受，属于预期行为而非非法输入。
	tn, err := NewTenant("Acme", "ACME", uuid.New())

	require.NoError(t, err)
	assert.Equal(t, "acme", tn.Slug)
}

func TestNewTenant_RejectsReservedSlug(t *testing.T) {
	_, err := NewTenant("Acme", "admin", uuid.New())

	require.ErrorIs(t, err, ErrInvalidSlug)
}

func TestTenant_Rename(t *testing.T) {
	tn, err := NewTenant("Acme", "acme", uuid.New())
	require.NoError(t, err)

	assert.True(t, tn.Rename("Acme Inc"))
	assert.Equal(t, "Acme Inc", tn.Name)

	assert.False(t, tn.Rename("Acme Inc"), "相同值不应触发变更")
	assert.False(t, tn.Rename("   "), "空白值不得覆盖已有名称")
	assert.Equal(t, "Acme Inc", tn.Name)
}

func TestTenant_Suspend(t *testing.T) {
	tn, err := NewTenant("Acme", "acme", uuid.New())
	require.NoError(t, err)

	tn.Suspend()

	assert.Equal(t, StatusSuspended, tn.Status)
	assert.False(t, tn.IsActive())
}

// repeat 生成指定长度的重复字符串，用于构造超长 slug。
func repeat(c byte, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return string(b)
}
