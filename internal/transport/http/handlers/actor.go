package handlers

import (
	"github.com/google/uuid"

	"github.com/example/iam-service/internal/domain/identity"
)

// actorID 从已认证身份中解析操作者 ID。
//
// 身份唯一来源是上下文中的 Principal，不接受任何请求头。
// 解析失败返回 uuid.Nil，由用例层当作非法创建者拒绝，避免 panic。
func actorID(principal identity.Principal) uuid.UUID {
	id, err := uuid.Parse(principal.UserID)
	if err != nil {
		return uuid.Nil
	}
	return id
}
