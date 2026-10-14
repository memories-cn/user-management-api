package handlers

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	appaudit "github.com/example/iam-service/internal/application/audit"
	appinvitation "github.com/example/iam-service/internal/application/invitation"
	appmembership "github.com/example/iam-service/internal/application/membership"
	approle "github.com/example/iam-service/internal/application/role"
	apptenant "github.com/example/iam-service/internal/application/tenant"
	invitationdomain "github.com/example/iam-service/internal/domain/invitation"
	permissiondomain "github.com/example/iam-service/internal/domain/permission"
	roledomain "github.com/example/iam-service/internal/domain/role"
	tenantdomain "github.com/example/iam-service/internal/domain/tenant"
	"github.com/example/iam-service/internal/ports"
	"github.com/example/iam-service/internal/transport/http/responses"
)

// 通用错误码引用 responses 包中的唯一来源，保证与中间件层口径一致。
const (
	// CodeInvalidArgument 表示请求参数不合法。
	CodeInvalidArgument = responses.CodeInvalidArgument
	// CodeForbidden 表示身份有效但权限不足。
	CodeForbidden = responses.CodeForbidden
	// CodeInternal 表示未预期的服务端错误。
	CodeInternal = responses.CodeInternal
	// CodeNotFound 复用统一的未找到错误码。
	CodeNotFound = responses.CodeNotFound
)

// 业务错误码（本包独有）。
const (
	// CodeTenantNotFound 表示平台路径下未找到租户。
	CodeTenantNotFound = "TENANT_NOT_FOUND"
	// CodeAuditActionInvalid 表示审计查询条件中的操作类型非法。
	CodeAuditActionInvalid = "AUDIT_ACTION_INVALID"
	// CodeAuditFilterInvalid 表示审计查询的过滤条件非法。
	CodeAuditFilterInvalid = "AUDIT_FILTER_INVALID"
	// CodeTenantSlugConflict 表示租户标识冲突。
	CodeTenantSlugConflict = "TENANT_SLUG_CONFLICT"
	// CodeMemberAlreadyExists 表示目标已是租户成员。
	CodeMemberAlreadyExists = "MEMBER_ALREADY_EXISTS"
	// CodeCannotRemoveLastMember 表示不能移除最后一个有效成员。
	CodeCannotRemoveLastMember = "CANNOT_REMOVE_LAST_MEMBER"
	// CodeInvalidStatus 表示目标状态不被允许。
	CodeInvalidStatus = "INVALID_STATUS"
	// CodeEmailNotVerified 表示邮箱未通过身份平台验证。
	CodeEmailNotVerified = "EMAIL_NOT_VERIFIED"
	// CodeInvitationEmailMismatch 表示身份邮箱与邀请邮箱不一致。
	CodeInvitationEmailMismatch = "INVITATION_EMAIL_MISMATCH"
	// CodeInvalidInvitationToken 表示邀请令牌无效。
	CodeInvalidInvitationToken = "INVALID_INVITATION_TOKEN"
	// CodeInvitationExpired 表示邀请已过期。
	CodeInvitationExpired = "INVITATION_EXPIRED"
	// CodeInvitationNotPending 表示邀请不处于待接受状态。
	CodeInvitationNotPending = "INVITATION_NOT_PENDING"

	// CodeRoleNotFound 表示角色不存在或不属于当前租户。
	CodeRoleNotFound = "ROLE_NOT_FOUND"
	// CodeRoleKeyConflict 表示租户内已存在同名角色。
	CodeRoleKeyConflict = "ROLE_KEY_CONFLICT"
	// CodeRoleInUse 表示角色仍被成员使用。
	CodeRoleInUse = "ROLE_IN_USE"
	// CodeSystemRoleImmutable 表示系统预置角色不允许该变更。
	CodeSystemRoleImmutable = "SYSTEM_ROLE_IMMUTABLE"
	// CodeInvalidRoleKey 表示角色标识非法。
	CodeInvalidRoleKey = "INVALID_ROLE_KEY"
	// CodePermissionNotFound 表示权限不在系统权限目录中。
	CodePermissionNotFound = "PERMISSION_NOT_FOUND"
	// CodeCannotRemoveLastOwner 表示不能移除租户内最后一个有效所有者。
	CodeCannotRemoveLastOwner = "CANNOT_REMOVE_LAST_OWNER"
	// CodeMembershipInactive 表示成员关系不可用，不能分配角色。
	CodeMembershipInactive = "MEMBERSHIP_INACTIVE"
)

// writeError 把用例层错误统一映射为 HTTP 状态码与错误码。
//
// 未知错误一律记 ERROR 日志并返回 500，绝不把内部细节泄漏给客户端。
func writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, tenantdomain.ErrInvalidName),
		errors.Is(err, tenantdomain.ErrInvalidSlug),
		errors.Is(err, tenantdomain.ErrInvalidCreator):
		responses.Fail(c, http.StatusBadRequest, CodeInvalidArgument, err.Error())

	case errors.Is(err, apptenant.ErrTenantSlugConflict):
		responses.Fail(c, http.StatusConflict, CodeTenantSlugConflict, "tenant slug already exists")

	case errors.Is(err, appmembership.ErrInvalidStatus):
		responses.Fail(c, http.StatusBadRequest, CodeInvalidStatus, err.Error())
	case errors.Is(err, appmembership.ErrMembershipNotFound):
		responses.Fail(c, http.StatusNotFound, CodeNotFound, "resource not found")
	case errors.Is(err, appmembership.ErrCannotRemoveLastMember):
		responses.Fail(c, http.StatusConflict, CodeCannotRemoveLastMember, "cannot remove the last active member")

	case errors.Is(err, appinvitation.ErrMemberAlreadyExists):
		responses.Fail(c, http.StatusConflict, CodeMemberAlreadyExists, "user is already a member of this tenant")
	case errors.Is(err, appinvitation.ErrEmailNotVerified):
		responses.Fail(c, http.StatusForbidden, CodeEmailNotVerified, "email is not verified")
	case errors.Is(err, appinvitation.ErrInvitationEmailMismatch):
		responses.Fail(c, http.StatusForbidden, CodeInvitationEmailMismatch, "email does not match the invitation")
	case errors.Is(err, appinvitation.ErrInvalidInvitationToken):
		responses.Fail(c, http.StatusForbidden, CodeInvalidInvitationToken, "invalid invitation token")
	case errors.Is(err, appinvitation.ErrInvitationNotFound):
		responses.Fail(c, http.StatusNotFound, CodeNotFound, "resource not found")
	case errors.Is(err, invitationdomain.ErrExpired):
		responses.Fail(c, http.StatusConflict, CodeInvitationExpired, "invitation is expired")
	case errors.Is(err, invitationdomain.ErrNotPending):
		responses.Fail(c, http.StatusConflict, CodeInvitationNotPending, "invitation is not pending")

	// 角色与权限
	case errors.Is(err, approle.ErrRoleNotFound):
		responses.Fail(c, http.StatusNotFound, CodeRoleNotFound, "role not found")
	case errors.Is(err, approle.ErrRoleKeyConflict):
		responses.Fail(c, http.StatusConflict, CodeRoleKeyConflict, "role key already exists in this tenant")
	case errors.Is(err, approle.ErrRoleInUse):
		responses.Fail(c, http.StatusConflict, CodeRoleInUse, "role is still assigned to members")
	case errors.Is(err, approle.ErrInvalidRoleKey):
		responses.Fail(c, http.StatusBadRequest, CodeInvalidRoleKey, "invalid role key")
	case errors.Is(err, approle.ErrPermissionNotFound):
		responses.Fail(c, http.StatusBadRequest, CodePermissionNotFound, "permission is not in the system catalog")
	case errors.Is(err, roledomain.ErrSystemRoleImmutable):
		responses.Fail(c, http.StatusConflict, CodeSystemRoleImmutable, "system preset role cannot be changed")
	case errors.Is(err, roledomain.ErrInvalidKey),
		errors.Is(err, roledomain.ErrInvalidName),
		errors.Is(err, roledomain.ErrInvalidTenantID):
		responses.Fail(c, http.StatusBadRequest, CodeInvalidArgument, err.Error())
	case errors.Is(err, permissiondomain.ErrInvalidCode),
		errors.Is(err, permissiondomain.ErrInvalidResource),
		errors.Is(err, permissiondomain.ErrInvalidAction):
		responses.Fail(c, http.StatusBadRequest, CodePermissionNotFound, err.Error())

	// 成员与角色分配
	case errors.Is(err, approle.ErrMembershipNotFound):
		responses.Fail(c, http.StatusNotFound, CodeNotFound, "resource not found")
	case errors.Is(err, approle.ErrMembershipInactive):
		responses.Fail(c, http.StatusForbidden, CodeMembershipInactive, "membership is not active")
	case errors.Is(err, approle.ErrCannotRemoveLastOwner),
		errors.Is(err, appmembership.ErrCannotRemoveLastOwner):
		responses.Fail(c, http.StatusConflict, CodeCannotRemoveLastOwner, "cannot remove the last active owner")
	case errors.Is(err, ports.ErrInvalidToken):
		responses.Fail(c, http.StatusUnauthorized, responses.CodeUnauthenticated, "missing or invalid credentials")

	// 平台租户状态管理
	case errors.Is(err, apptenant.ErrTenantNotFound):
		responses.Fail(c, http.StatusNotFound, CodeTenantNotFound, "tenant not found")
	case errors.Is(err, apptenant.ErrInvalidStatus):
		responses.Fail(c, http.StatusBadRequest, CodeInvalidStatus, err.Error())

	// 审计查询
	case errors.Is(err, appaudit.ErrInvalidAction):
		responses.Fail(c, http.StatusBadRequest, CodeAuditActionInvalid, err.Error())
	case errors.Is(err, appaudit.ErrInvalidTimeRange):
		responses.Fail(c, http.StatusBadRequest, CodeAuditFilterInvalid, err.Error())

	default:
		slog.ErrorContext(c.Request.Context(), "unhandled error", "error", err)
		responses.Fail(c, http.StatusInternalServerError, CodeInternal, "internal server error")
	}
	c.Abort()
}
