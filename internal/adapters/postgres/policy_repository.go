package postgres

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/example/iam-service/internal/ports"
)

// casbinRuleModel 是 casbin_rule 表的 GORM 映射。
//
// 该表由 gorm-adapter 读取（见 migrations/000001_init_casbin.up.sql），约束如下：
//   - 未使用的 v 列必须写入空字符串而非 NULL，否则唯一索引会失效；
//   - 表结构只有 id 与 ptype、v0..v5，**没有时间戳列**，因此模型不得声明 CreatedAt/UpdatedAt，
//     否则 GORM 会向不存在的列写入。
type casbinRuleModel struct {
	ID    int64  `gorm:"column:id;primaryKey;autoIncrement"`
	PType string `gorm:"column:ptype"`
	V0    string `gorm:"column:v0"`
	V1    string `gorm:"column:v1"`
	V2    string `gorm:"column:v2"`
	V3    string `gorm:"column:v3"`
	V4    string `gorm:"column:v4"`
	V5    string `gorm:"column:v5"`
}

// TableName 指定表名，必须与 gorm-adapter 的默认表名一致（单数）。
func (casbinRuleModel) TableName() string { return "casbin_rule" }

// Casbin 策略类型，与 casbin/model.conf 的 [policy_definition] 与 [role_definition] 对应。
const (
	// ptypePolicy 是权限规则。
	ptypePolicy = "p"
	// ptypeGrouping 是角色分配规则。
	ptypeGrouping = "g"
)

// PolicyRepository 基于 PostgreSQL 维护 Casbin 策略投影与全局版本号。
//
// **关键约束**：所有写入必须走 dbFrom(ctx)，即与业务写入共享同一个事务句柄。
// 一旦绕过事务直接写 casbin_rule，业务回滚后策略就会残留，形成"权限幽灵"。
type PolicyRepository struct {
	db *gorm.DB
}

// 编译期断言：确保实现 ports.PolicyRepository。
var _ ports.PolicyRepository = (*PolicyRepository)(nil)

// NewPolicyRepository 构造策略投影仓储。
func NewPolicyRepository(db *gorm.DB) *PolicyRepository {
	return &PolicyRepository{db: db}
}

// ReplaceRoleRules 用给定权限集合整体替换某角色在本租户的 p 规则。
//
// 采用"先删除后插入"而非差异比对：策略投影是业务关系的派生结果，
// 整体替换保证投影与 role_permissions 不可能出现偏差。两步都在同一事务内。
func (r *PolicyRepository) ReplaceRoleRules(ctx context.Context, tenantID, roleID string, perms []ports.PolicyPermission) error {
	if tenantID == "" || roleID == "" {
		return errors.New("替换角色策略缺少租户或角色标识")
	}

	handle := dbFrom(ctx, r.db).WithContext(ctx)
	// 删除条件必须带 tenant_id，避免误删其他租户的同名规则。
	if err := handle.Where("ptype = ? AND v0 = ? AND v1 = ?", ptypePolicy, roleID, tenantID).
		Delete(&casbinRuleModel{}).Error; err != nil {
		return fmt.Errorf("清除角色策略失败：%w", err)
	}

	rules := make([]casbinRuleModel, 0, len(perms))
	seen := make(map[string]struct{}, len(perms))
	for _, perm := range perms {
		key := perm.Resource + "/" + perm.Action
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		rules = append(rules, policyRule(roleID, tenantID, perm.Resource, perm.Action))
	}
	if len(rules) == 0 {
		return nil
	}
	if err := handle.Create(&rules).Error; err != nil {
		return fmt.Errorf("写入角色策略失败：%w", err)
	}
	return nil
}

// ReplaceMemberRules 用给定角色集合整体替换某成员在本租户的 g 规则。
func (r *PolicyRepository) ReplaceMemberRules(ctx context.Context, tenantID, userID string, roleIDs []string) error {
	if tenantID == "" || userID == "" {
		return errors.New("替换成员策略缺少租户或用户标识")
	}

	handle := dbFrom(ctx, r.db).WithContext(ctx)
	if err := handle.Where("ptype = ? AND v0 = ? AND v2 = ?", ptypeGrouping, userID, tenantID).
		Delete(&casbinRuleModel{}).Error; err != nil {
		return fmt.Errorf("清除成员策略失败：%w", err)
	}

	rules := make([]casbinRuleModel, 0, len(roleIDs))
	seen := make(map[string]struct{}, len(roleIDs))
	for _, roleID := range roleIDs {
		if _, dup := seen[roleID]; dup {
			continue
		}
		seen[roleID] = struct{}{}
		rules = append(rules, groupingRule(userID, roleID, tenantID))
	}
	if len(rules) == 0 {
		return nil
	}
	if err := handle.Create(&rules).Error; err != nil {
		return fmt.Errorf("写入成员策略失败：%w", err)
	}
	return nil
}

// DeleteRoleRules 删除某角色的 p 规则以及所有引用它的 g 规则。
//
// 两步都必须执行：只删 p 规则会在 casbin_rule 里留下悬空的 g 规则，
// 只删 g 规则会让已删除角色的权限仍可被匹配。
func (r *PolicyRepository) DeleteRoleRules(ctx context.Context, tenantID, roleID string) error {
	if tenantID == "" || roleID == "" {
		return errors.New("删除角色策略缺少租户或角色标识")
	}

	handle := dbFrom(ctx, r.db).WithContext(ctx)
	if err := handle.Where("ptype = ? AND v0 = ? AND v1 = ?", ptypePolicy, roleID, tenantID).
		Delete(&casbinRuleModel{}).Error; err != nil {
		return fmt.Errorf("删除角色权限规则失败：%w", err)
	}
	if err := handle.Where("ptype = ? AND v1 = ? AND v2 = ?", ptypeGrouping, roleID, tenantID).
		Delete(&casbinRuleModel{}).Error; err != nil {
		return fmt.Errorf("删除角色分配规则失败：%w", err)
	}
	return nil
}

// DeleteMemberRules 删除某成员在本租户的全部 g 规则，即立即撤权。
func (r *PolicyRepository) DeleteMemberRules(ctx context.Context, tenantID, userID string) error {
	if tenantID == "" || userID == "" {
		return errors.New("删除成员策略缺少租户或用户标识")
	}
	if err := dbFrom(ctx, r.db).WithContext(ctx).
		Where("ptype = ? AND v0 = ? AND v2 = ?", ptypeGrouping, userID, tenantID).
		Delete(&casbinRuleModel{}).Error; err != nil {
		return fmt.Errorf("删除成员策略失败：%w", err)
	}
	return nil
}

// DeleteTenantRules 删除租户的全部 p/g 规则。
func (r *PolicyRepository) DeleteTenantRules(ctx context.Context, tenantID string) error {
	if tenantID == "" {
		return errors.New("删除租户策略缺少租户标识")
	}
	if err := dbFrom(ctx, r.db).WithContext(ctx).
		Where("ptype = ? AND v1 = ?", ptypePolicy, tenantID).
		Delete(&casbinRuleModel{}).Error; err != nil {
		return fmt.Errorf("删除租户权限规则失败：%w", err)
	}
	if err := dbFrom(ctx, r.db).WithContext(ctx).
		Where("ptype = ? AND v2 = ?", ptypeGrouping, tenantID).
		Delete(&casbinRuleModel{}).Error; err != nil {
		return fmt.Errorf("删除租户分配规则失败：%w", err)
	}
	return nil
}

// BumpVersion 在同一事务内递增全局策略版本号并返回新版本。
//
// 使用 UPDATE ... RETURNING 而非"读-改-写"，避免并发写入互相覆盖导致版本号停滞，
// 进而让其他实例误以为策略未变更。
func (r *PolicyRepository) BumpVersion(ctx context.Context) (int64, error) {
	var version int64
	err := dbFrom(ctx, r.db).WithContext(ctx).
		Raw("UPDATE policy_version SET version = version + 1, updated_at = now() WHERE id = 1 RETURNING version").
		Scan(&version).Error
	if err != nil {
		return 0, fmt.Errorf("递增策略版本号失败：%w", err)
	}
	return version, nil
}

// CurrentVersion 读取全局策略版本号。
func (r *PolicyRepository) CurrentVersion(ctx context.Context) (int64, error) {
	var version int64
	err := dbFrom(ctx, r.db).WithContext(ctx).
		Raw("SELECT version FROM policy_version WHERE id = 1").
		Scan(&version).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("读取策略版本号失败：%w", err)
	}
	return version, nil
}

// policyRule 构造一条 p 规则：p, roleID, tenantID, resource, action。
//
// 列映射是全仓唯一的定义点，必须与 casbin/model.conf 的 p = sub, dom, obj, act 一致：
// gorm-adapter 会把 p 之后的四个值依次写入 v0..v3。
func policyRule(roleID, tenantID, resource, action string) casbinRuleModel {
	return casbinRuleModel{
		PType: ptypePolicy,
		V0:    roleID,
		V1:    tenantID,
		V2:    resource,
		V3:    action,
	}
}

// groupingRule 构造一条 g 规则：g, userID, roleID, tenantID。
//
// 与 casbin/model.conf 的 g = _, _, _ 对应，dom（租户）作为第三位。
func groupingRule(userID, roleID, tenantID string) casbinRuleModel {
	return casbinRuleModel{
		PType: ptypeGrouping,
		V0:    userID,
		V1:    roleID,
		V2:    tenantID,
	}
}
