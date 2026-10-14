package handlers

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	appinvitation "github.com/example/iam-service/internal/application/invitation"
	appmembership "github.com/example/iam-service/internal/application/membership"
	apptenant "github.com/example/iam-service/internal/application/tenant"
	invitationdomain "github.com/example/iam-service/internal/domain/invitation"
	tenantdomain "github.com/example/iam-service/internal/domain/tenant"
	"github.com/example/iam-service/internal/transport/http/responses"
)

// 业务错误码。
const (
	// CodeInvalidArgument 表示请求参数不合法。
	CodeInvalidArgument = "INVALID_ARGUMENT"
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
	// CodeInternal 表示未预期的服务端错误。
	CodeInternal = "INTERNAL"
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

	default:
		slog.ErrorContext(c.Request.Context(), "unhandled error", "error", err)
		responses.Fail(c, http.StatusInternalServerError, CodeInternal, "internal server error")
	}
	c.Abort()
}

// CodeNotFound 复用中间件层的未找到错误码。
const CodeNotFound = "NOT_FOUND"
