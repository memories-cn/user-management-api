// Package permission 定义系统权限目录实体与不变量。
//
// 权限是**系统级**资源：集合由系统迁移维护，租户管理员只能在既有集合内组合自定义角色，
// 不能新建可执行权限（见 docs/TDD.md 3.5、10.2）。因此本实体不含 TenantID。
//
// 分层约束：本包属于 Domain，不得依赖 Gin、GORM、Redis 或 Casbin。
package permission

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrInvalidResource 表示资源标识非法。
	ErrInvalidResource = errors.New("权限资源标识非法")
	// ErrInvalidAction 表示操作标识非法。
	ErrInvalidAction = errors.New("权限操作标识非法")
	// ErrInvalidCode 表示权限代码格式非法。
	ErrInvalidCode = errors.New("权限代码格式非法")
)

// 标识格式约束：只允许小写字母、数字与下划线，且以小写字母开头。
const (
	maxResourceLen = 64
	maxActionLen   = 64
	maxCodeLen     = 128
)

// identPattern 限定资源与操作标识的字符集。
var identPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Permission 是系统权限目录中的一项，采用 resource:action 形式。
type Permission struct {
	// ID 是本地业务主键。
	ID uuid.UUID
	// Code 是稳定的对外标识，形如 member:invite。
	Code string
	// Resource 是受控资源，形如 member。
	Resource string
	// Action 是资源上的操作，形如 invite。
	Action string
	// Description 是展示用的中文说明。
	Description string
	// CreatedAt 是权限进入目录的时间。
	CreatedAt time.Time
}

// CodeSeparator 分隔资源与操作，构成权限代码。
const CodeSeparator = ":"

// BuildCode 由资源与操作构造权限代码。
func BuildCode(resource, action string) string {
	return resource + CodeSeparator + action
}

// ParseCode 解析权限代码，非法格式返回 ErrInvalidCode。
func ParseCode(code string) (resource, action string, err error) {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "" || len(code) > maxCodeLen {
		return "", "", ErrInvalidCode
	}
	resource, action, found := strings.Cut(code, CodeSeparator)
	if !found {
		return "", "", ErrInvalidCode
	}
	if !identPattern.MatchString(resource) || !identPattern.MatchString(action) {
		return "", "", ErrInvalidCode
	}
	return resource, action, nil
}

// NewPermission 构造权限目录项并校验资源与操作标识。
func NewPermission(resource, action, description string) (*Permission, error) {
	resource = strings.ToLower(strings.TrimSpace(resource))
	action = strings.ToLower(strings.TrimSpace(action))
	if resource == "" || len(resource) > maxResourceLen || !identPattern.MatchString(resource) {
		return nil, ErrInvalidResource
	}
	if action == "" || len(action) > maxActionLen || !identPattern.MatchString(action) {
		return nil, ErrInvalidAction
	}
	return &Permission{
		ID:          uuid.New(),
		Code:        BuildCode(resource, action),
		Resource:    resource,
		Action:      action,
		Description: strings.TrimSpace(description),
		CreatedAt:   time.Now(),
	}, nil
}

// String 返回权限代码。
func (p *Permission) String() string {
	return p.Code
}

// MatchesCode 报告权限是否匹配给定代码，比较前归一化大小写与空白。
func (p *Permission) MatchesCode(code string) bool {
	return strings.EqualFold(strings.TrimSpace(code), p.Code)
}

// ValidateCodes 校验一组权限代码是否全部合法，返回首个非法代码。
func ValidateCodes(codes []string) error {
	for _, code := range codes {
		if _, _, err := ParseCode(code); err != nil {
			return fmt.Errorf("权限代码 %q 非法：%w", code, ErrInvalidCode)
		}
	}
	return nil
}
