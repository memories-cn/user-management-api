package audit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	auditdomain "github.com/example/iam-service/internal/domain/audit"
	"github.com/example/iam-service/internal/ports"
	"github.com/example/iam-service/internal/rbactest"
)

func TestRecord_WritesEntryWithRequestContext(t *testing.T) {
	fix := rbactest.NewFixture()
	svc := NewService(fix.Audits)
	tenantID, actor := uuid.New(), uuid.New()

	ctx := auditdomain.WithRequestContext(context.Background(),
		auditdomain.RequestContext{RequestID: "req-42", IPAddress: "203.0.113.7", UserAgent: "curl/8"})

	require.NoError(t, svc.RecordAction(ctx, &tenantID, actor,
		auditdomain.ActionMemberRemove, "member", "user-1", auditdomain.ResultSuccess,
		map[string]any{"to_status": "removed"}))

	require.Equal(t, 1, fix.Audits.Count())
	entry := fix.Audits.Entries[0]
	assert.Equal(t, auditdomain.ActionMemberRemove, entry.Action)
	assert.Equal(t, "req-42", entry.RequestID, "审计必须能关联到具体请求")
	assert.Equal(t, "203.0.113.7", entry.IPAddress)
	assert.Equal(t, "removed", entry.Metadata["to_status"])
}

func TestRecord_FailsWhenRepositoryFails(t *testing.T) {
	fix := rbactest.NewFixture()
	fix.Audits.SetCreateError(errors.New("审计表不可写"))
	svc := NewService(fix.Audits)

	err := svc.RecordAction(context.Background(), nil, uuid.New(),
		auditdomain.ActionTenantSuspend, "tenant", "t-1", auditdomain.ResultSuccess, nil)

	// 审计写失败必须让调用方感知：整个高风险操作应当回滚。
	require.Error(t, err)
}

func TestRecord_RejectsSensitiveMetadata(t *testing.T) {
	fix := rbactest.NewFixture()
	svc := NewService(fix.Audits)
	tenantID := uuid.New()

	err := svc.RecordAction(context.Background(), &tenantID, uuid.New(),
		auditdomain.ActionMemberRemove, "member", "user-1", auditdomain.ResultSuccess,
		map[string]any{"access_token": "leaked"})

	require.ErrorIs(t, err, auditdomain.ErrSensitiveMetadata)
	assert.Zero(t, fix.Audits.Count(), "被拒绝的元数据不得落库")
}

func TestRecord_FailsWhenServiceNotWired(t *testing.T) {
	// 审计未装配时必须显式失败，而不是静默丢留痕。
	svc := &Service{}

	err := svc.RecordAction(context.Background(), nil, uuid.New(),
		auditdomain.ActionTenantSuspend, "tenant", "t-1", auditdomain.ResultSuccess, nil)

	require.Error(t, err)
}

func TestList_RejectsUnknownAction(t *testing.T) {
	fix := rbactest.NewFixture()
	svc := NewService(fix.Audits)

	_, err := svc.List(context.Background(), uuid.NewString(), ports.AuditFilter{Action: "member.explode"})

	require.ErrorIs(t, err, ErrInvalidAction,
		"静默忽略非法过滤条件会让管理员误以为没有这类操作")
}

func TestList_RejectsInvertedTimeRange(t *testing.T) {
	fix := rbactest.NewFixture()
	svc := NewService(fix.Audits)

	since := time.Now()
	until := since.Add(-time.Hour)
	_, err := svc.List(context.Background(), uuid.NewString(),
		ports.AuditFilter{Since: &since, Until: &until})

	require.ErrorIs(t, err, ErrInvalidTimeRange)
}

func TestList_NormalizesPaging(t *testing.T) {
	fix := rbactest.NewFixture()
	svc := NewService(fix.Audits)

	result, err := svc.List(context.Background(), uuid.NewString(),
		ports.AuditFilter{Limit: 5000, Offset: -10})

	require.NoError(t, err)
	assert.Equal(t, MaxLimit, result.Limit)
	assert.Equal(t, 0, result.Offset)
}

func TestList_DefaultsPaging(t *testing.T) {
	fix := rbactest.NewFixture()
	svc := NewService(fix.Audits)

	result, err := svc.List(context.Background(), uuid.NewString(), ports.AuditFilter{})

	require.NoError(t, err)
	assert.Equal(t, DefaultLimit, result.Limit)
}

func TestParseTime(t *testing.T) {
	parsed, err := ParseTime("2026-01-02T03:04:05Z")
	require.NoError(t, err)
	require.NotNil(t, parsed)
	assert.Equal(t, 2026, parsed.Year())

	empty, err := ParseTime("  ")
	require.NoError(t, err)
	assert.Nil(t, empty, "空值表示不限，不是错误")

	_, err = ParseTime("not-a-time")
	require.ErrorIs(t, err, ErrInvalidTimeRange)
}
