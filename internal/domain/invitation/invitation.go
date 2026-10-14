// Package invitation 定义成员邀请实体与其生命周期。
//
// 核心原则：**先认证、后绑定**。邀请只绑定已通过 ZITADEL 认证且邮箱已验证的身份，
// 邮箱不作为身份主键，仅用于与被验证令牌中的 email 比对。
//
// 分层约束：本包属于 Domain，不得依赖 Gin、GORM、Redis 或 Casbin。
package invitation

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Status 表示邀请状态。
type Status string

const (
	// StatusPending 表示待接受。
	StatusPending Status = "pending"
	// StatusAccepted 表示已被接受。
	StatusAccepted Status = "accepted"
	// StatusCancelled 表示已被撤销。
	StatusCancelled Status = "cancelled"
	// StatusExpired 表示已过期（由接受时判定写入）。
	StatusExpired Status = "expired"
)

var (
	// ErrInvalidTenantID 表示租户标识缺失。
	ErrInvalidTenantID = errors.New("tenant_id 不能为空")
	// ErrInvalidEmail 表示邮箱格式非法。
	ErrInvalidEmail = errors.New("邮箱格式非法")
	// ErrInvalidInviter 表示邀请人缺失。
	ErrInvalidInviter = errors.New("invited_by 不能为空")
	// ErrInvalidTTL 表示有效期非法。
	ErrInvalidTTL = errors.New("有效期必须大于 0")
	// ErrNotPending 表示邀请不处于待接受状态。
	ErrNotPending = errors.New("邀请不处于待接受状态")
	// ErrExpired 表示邀请已过期。
	ErrExpired = errors.New("邀请已过期")
)

// tokenBytes 是随机令牌的字节长度。
const tokenBytes = 32

// Invitation 是成员邀请实体。
type Invitation struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	// Email 归一化后（小写、去空白）的受邀邮箱。
	Email string
	// TokenHash 只保存明文令牌的 SHA-256，明文不在数据库中留存。
	TokenHash []byte
	// Status 表示邀请状态。
	Status Status
	// InvitedBy 是发起邀请的用户。
	InvitedBy uuid.UUID
	// AcceptedUserID 是接受邀请的用户。
	AcceptedUserID *uuid.UUID
	// ExpiresAt 是有效期截止时间。
	ExpiresAt time.Time
	// AcceptedAt 是接受时间。
	AcceptedAt *time.Time
	// CreatedAt、UpdatedAt 由领域层初始化。
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NormalizeEmail 归一化邮箱：去空白并转小写。
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// NewInvitation 创建邀请，返回实体与**明文令牌**。
//
// 明文令牌只在创建时返回一次，用于邮件投递；实体内只保留哈希。
func NewInvitation(tenantID uuid.UUID, email string, invitedBy uuid.UUID, ttl time.Duration) (*Invitation, string, error) {
	if tenantID == uuid.Nil {
		return nil, "", ErrInvalidTenantID
	}
	email = NormalizeEmail(email)
	if !strings.Contains(email, "@") || strings.HasPrefix(email, "@") || strings.HasSuffix(email, "@") {
		return nil, "", ErrInvalidEmail
	}
	if invitedBy == uuid.Nil {
		return nil, "", ErrInvalidInviter
	}
	if ttl <= 0 {
		return nil, "", ErrInvalidTTL
	}

	token, err := generateToken()
	if err != nil {
		return nil, "", err
	}

	now := time.Now()
	return &Invitation{
		ID:        uuid.New(),
		TenantID:  tenantID,
		Email:     email,
		TokenHash: hashToken(token),
		Status:    StatusPending,
		InvitedBy: invitedBy,
		ExpiresAt: now.Add(ttl),
		CreatedAt: now,
		UpdatedAt: now,
	}, token, nil
}

// IsPending 报告邀请是否处于待接受状态。
func (i *Invitation) IsPending(now time.Time) bool {
	return i.Status == StatusPending
}

// IsExpired 报告邀请是否已超过有效期。
func (i *Invitation) IsExpired(now time.Time) bool {
	return now.After(i.ExpiresAt)
}

// Matches 校验明文令牌是否与本邀请匹配，使用常量时间比较避免时序侧信道。
func (i *Invitation) Matches(token string) bool {
	if token == "" || len(i.TokenHash) == 0 {
		return false
	}
	return subtle.ConstantTimeCompare(hashToken(token), i.TokenHash) == 1
}

// Accept 接受邀请并绑定用户。
func (i *Invitation) Accept(userID uuid.UUID, now time.Time) error {
	if !i.IsPending(now) {
		return ErrNotPending
	}
	if i.IsExpired(now) {
		i.Status = StatusExpired
		i.UpdatedAt = now
		return ErrExpired
	}

	i.Status = StatusAccepted
	i.AcceptedUserID = &userID
	i.AcceptedAt = &now
	i.UpdatedAt = now
	return nil
}

// Cancel 撤销邀请，重复取消返回 ErrNotPending 而不是静默成功。
func (i *Invitation) Cancel() error {
	if i.Status != StatusPending {
		return ErrNotPending
	}
	i.Status = StatusCancelled
	i.UpdatedAt = time.Now()
	return nil
}

// Refresh 在幂等复用邀请时刷新有效期。
func (i *Invitation) Refresh(now time.Time, ttl time.Duration) {
	i.ExpiresAt = now.Add(ttl)
	i.UpdatedAt = now
}

// generateToken 生成 URL 安全的随机令牌。
func generateToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// hashToken 计算令牌的 SHA-256。
func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
