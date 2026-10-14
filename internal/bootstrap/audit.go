package bootstrap

import (
	"github.com/example/iam-service/internal/adapters/postgres"
	appaudit "github.com/example/iam-service/internal/application/audit"
	"gorm.io/gorm"
)

// NewAuditRepository 构造审计仓储。
func NewAuditRepository(db *gorm.DB) *postgres.AuditRepository {
	return postgres.NewAuditRepository(db)
}

// NewAuditService 构造审计用例。
//
// 审计用例必须先于业务用例构造：成员、角色、租户等用例都要在事务内写入留痕。
func NewAuditService(db *gorm.DB) *appaudit.Service {
	return appaudit.NewService(NewAuditRepository(db))
}
