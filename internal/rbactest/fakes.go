// Package rbactest 提供授权域仓储与依赖的内存替身，专供单元测试使用。
//
// 与 internal/oidctest 一样，本包只被测试引用，不参与生产装配。
// 集中放置的原因：邀请、创建租户、成员管理、角色等用例都依赖同一组授权域接口，
// 分散到各测试包会造成大量重复替身。
package rbactest

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"github.com/example/iam-service/internal/domain/audit"
	"github.com/example/iam-service/internal/domain/permission"
	"github.com/example/iam-service/internal/domain/role"
	"github.com/example/iam-service/internal/ports"
)

// Fixture 聚合互相引用的授权域替身，避免各替身之间通过全局变量互相查找。
type Fixture struct {
	// Permissions 是系统权限目录替身。
	Permissions *PermissionRepository
	// Roles 是角色仓储替身。
	Roles *RoleRepository
	// MembershipRoles 是成员角色分配替身。
	MembershipRoles *MembershipRoleRepository
	// Policies 是策略投影替身。
	Policies *PolicyRepository
	// Audits 是审计仓储替身。
	Audits *AuditRepository
	// Tx 是事务替身。
	Tx *Transactor
	// Reloader 记录策略刷新调用。
	Reloader *Reloader
}

// NewFixture 构造一组授权域替身，permissionCodes 为预置权限目录。
func NewFixture(permissionCodes ...string) *Fixture {
	permissions := NewPermissionRepository(permissionCodes...)
	roles := &RoleRepository{items: map[string]*role.Role{}, permissions: map[string][]uuid.UUID{}, catalog: permissions}
	membershipRoles := &MembershipRoleRepository{
		items:       map[string][]*role.Assignment{},
		memberships: map[string]membershipRef{},
		roles:       roles,
	}
	// 角色占用统计需要回查成员角色分配，建立双向引用。
	roles.assignments = membershipRoles

	return &Fixture{
		Permissions:     permissions,
		Roles:           roles,
		MembershipRoles: membershipRoles,
		Policies:        NewPolicyRepository(),
		Audits:          NewAuditRepository(),
		Tx:              &Transactor{},
		Reloader:        &Reloader{},
	}
}

// PermissionRepository 是系统权限目录的内存替身。
type PermissionRepository struct {
	mu    sync.Mutex
	items []*permission.Permission
}

// NewPermissionRepository 构造权限目录替身。
func NewPermissionRepository(codes ...string) *PermissionRepository {
	items := make([]*permission.Permission, 0, len(codes))
	for _, code := range codes {
		resource, action, err := permission.ParseCode(code)
		if err != nil {
			continue
		}
		items = append(items, &permission.Permission{ID: uuid.New(), Code: code, Resource: resource, Action: action})
	}
	return &PermissionRepository{items: items}
}

// ListAll 返回全部权限。
func (r *PermissionRepository) ListAll(context.Context) ([]*permission.Permission, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*permission.Permission{}, r.items...), nil
}

// FindByCode 依据代码查询，未命中返回 (nil, nil)。
func (r *PermissionRepository) FindByCode(_ context.Context, code string) (*permission.Permission, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.items {
		if p.Code == code {
			return p, nil
		}
	}
	return nil, nil
}

// FindByCodes 批量查询，缺失的代码直接跳过。
func (r *PermissionRepository) FindByCodes(_ context.Context, codes []string) ([]*permission.Permission, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	byCode := make(map[string]*permission.Permission, len(r.items))
	for _, p := range r.items {
		byCode[p.Code] = p
	}
	result := make([]*permission.Permission, 0, len(codes))
	for _, code := range codes {
		if p, ok := byCode[code]; ok {
			result = append(result, p)
		}
	}
	return result, nil
}

// RoleRepository 是角色与角色权限的内存替身。
type RoleRepository struct {
	mu    sync.Mutex
	items map[string]*role.Role
	// permissions 记录角色关联的权限 ID。
	permissions map[string][]uuid.UUID
	// catalog 用于把权限 ID 还原为权限实体。
	catalog *PermissionRepository
	// assignments 用于统计角色被多少成员使用。
	assignments *MembershipRoleRepository
	// createErr 用于模拟创建失败，验证事务回滚。
	createErr error
}

// SetCreateError 注入创建失败。
func (r *RoleRepository) SetCreateError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.createErr = err
}

// Create 创建角色。
func (r *RoleRepository) Create(_ context.Context, rl *role.Role) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.createErr != nil {
		return r.createErr
	}
	r.items[rl.ID.String()] = rl
	return nil
}

// FindByID 依据租户与角色标识查询，跨租户返回 (nil, nil)。
func (r *RoleRepository) FindByID(_ context.Context, tenantID, roleID string) (*role.Role, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rl, ok := r.items[roleID]
	if !ok || rl.TenantID.String() != tenantID {
		return nil, nil
	}
	return rl, nil
}

// FindByKey 依据租户与角色 key 查询。
func (r *RoleRepository) FindByKey(_ context.Context, tenantID, key string) (*role.Role, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rl := range r.items {
		if rl.TenantID.String() == tenantID && rl.Key == role.NormalizeKey(key) {
			return rl, nil
		}
	}
	return nil, nil
}

// ListByTenant 返回租户角色。
func (r *RoleRepository) ListByTenant(_ context.Context, tenantID string, _ int, _ int) ([]*role.Role, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]*role.Role, 0)
	for _, rl := range r.items {
		if rl.TenantID.String() == tenantID {
			result = append(result, rl)
		}
	}
	return result, nil
}

// Update 更新角色。
func (r *RoleRepository) Update(_ context.Context, rl *role.Role) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[rl.ID.String()] = rl
	return nil
}

// Delete 删除角色。
func (r *RoleRepository) Delete(_ context.Context, tenantID, roleID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rl, ok := r.items[roleID]
	if !ok || rl.TenantID.String() != tenantID {
		return nil
	}
	delete(r.items, roleID)
	delete(r.permissions, roleID)
	return nil
}

// ReplacePermissions 整体替换角色权限。
func (r *RoleRepository) ReplacePermissions(_ context.Context, _, roleID string, permissionIDs []uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.permissions[roleID] = append([]uuid.UUID{}, permissionIDs...)
	return nil
}

// ListPermissionIDs 返回角色权限 ID。
func (r *RoleRepository) ListPermissionIDs(_ context.Context, _, roleID string) ([]uuid.UUID, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]uuid.UUID{}, r.permissions[roleID]...), nil
}

// ListPermissions 返回角色权限实体。
func (r *RoleRepository) ListPermissions(ctx context.Context, tenantID, roleID string) ([]*permission.Permission, error) {
	ids, err := r.ListPermissionIDs(ctx, tenantID, roleID)
	if err != nil {
		return nil, err
	}

	all, err := r.catalog.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]*permission.Permission, len(all))
	for _, p := range all {
		byID[p.ID] = p
	}

	result := make([]*permission.Permission, 0, len(ids))
	for _, id := range ids {
		if p, ok := byID[id]; ok {
			result = append(result, p)
		}
	}
	return result, nil
}

// CountByTenant 统计租户角色数。
func (r *RoleRepository) CountByTenant(_ context.Context, tenantID string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var count int64
	for _, rl := range r.items {
		if rl.TenantID.String() == tenantID {
			count++
		}
	}
	return count, nil
}

// CountAssignments 统计仍在使用该角色的成员数。
func (r *RoleRepository) CountAssignments(ctx context.Context, tenantID, roleID string) (int64, error) {
	if r.assignments == nil {
		return 0, nil
	}
	return r.assignments.CountByRole(ctx, tenantID, roleID)
}

// MembershipRoleRepository 是成员角色分配的内存替身。
type MembershipRoleRepository struct {
	mu sync.Mutex
	// items 以 membershipID 为键保存分配。
	items map[string][]*role.Assignment
	// memberships 记录 membershipID →（租户、用户、是否 active），用于 owner 统计。
	memberships map[string]membershipRef
	// roles 用于把角色 ID 还原为角色实体。
	roles *RoleRepository
}

// membershipRef 是统计有效所有者所需的最小成员信息。
type membershipRef struct {
	tenantID string
	userID   string
	active   bool
}

// RegisterMembership 登记成员关系，使 owner 统计与撤权断言有意义。
func (r *MembershipRoleRepository) RegisterMembership(membershipID, tenantID, userID string, active bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.memberships[membershipID] = membershipRef{tenantID: tenantID, userID: userID, active: active}
}

// Replace 整体替换成员角色分配。
func (r *MembershipRoleRepository) Replace(_ context.Context, _, membershipID string, assignments []*role.Assignment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[membershipID] = append([]*role.Assignment{}, assignments...)
	return nil
}

// ListRoles 返回成员被授予的角色。
func (r *MembershipRoleRepository) ListRoles(_ context.Context, _, membershipID string) ([]*role.Role, error) {
	r.mu.Lock()
	assignments := append([]*role.Assignment{}, r.items[membershipID]...)
	r.mu.Unlock()

	result := make([]*role.Role, 0, len(assignments))
	for _, a := range assignments {
		rl, err := r.roles.FindByID(context.Background(), a.TenantID.String(), a.RoleID.String())
		if err != nil {
			return nil, err
		}
		if rl != nil {
			result = append(result, rl)
		}
	}
	return result, nil
}

// DeleteByMembership 清除成员角色分配。
func (r *MembershipRoleRepository) DeleteByMembership(_ context.Context, _, membershipID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.items, membershipID)
	return nil
}

// CountActiveOwners 统计租户内 active 成员中被授予 owner 的人数。
//
// 三个条件缺一不可：成员 active、角色属于本租户、角色 key 为 owner。
func (r *MembershipRoleRepository) CountActiveOwners(ctx context.Context, tenantID string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var count int64
	for membershipID, assignments := range r.items {
		ref, ok := r.memberships[membershipID]
		if !ok || !ref.active || ref.tenantID != tenantID {
			continue
		}
		for _, a := range assignments {
			rl, err := r.roles.FindByID(ctx, tenantID, a.RoleID.String())
			if err != nil {
				return 0, err
			}
			if rl != nil && rl.Key == role.KeyOwner {
				count++
			}
		}
	}
	return count, nil
}

// HasActiveRole 报告 active 成员是否被授予指定角色。
func (r *MembershipRoleRepository) HasActiveRole(ctx context.Context, tenantID, userID, roleKey string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for membershipID, assignments := range r.items {
		ref, ok := r.memberships[membershipID]
		if !ok || !ref.active || ref.tenantID != tenantID || ref.userID != userID {
			continue
		}
		for _, a := range assignments {
			rl, err := r.roles.FindByID(ctx, tenantID, a.RoleID.String())
			if err != nil {
				return false, err
			}
			if rl != nil && rl.Key == role.NormalizeKey(roleKey) {
				return true, nil
			}
		}
	}
	return false, nil
}

// CountByRole 统计某角色被分配给多少成员。
func (r *MembershipRoleRepository) CountByRole(_ context.Context, tenantID, roleID string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var count int64
	for membershipID, assignments := range r.items {
		ref, ok := r.memberships[membershipID]
		if !ok || ref.tenantID != tenantID {
			continue
		}
		for _, a := range assignments {
			if a.RoleID.String() == roleID {
				count++
			}
		}
	}
	return count, nil
}

// PolicyRepository 是策略投影与版本号的内存替身。
type PolicyRepository struct {
	mu sync.Mutex
	// roleRules 记录 roleID → 权限二元组。
	roleRules map[string][]ports.PolicyPermission
	// memberRules 记录 tenantID|userID → 角色 ID，保证跨租户断言准确。
	memberRules map[string][]string
	version     int64
	// bumpErr 用于模拟提交阶段失败，验证事务原子性。
	bumpErr error
}

// NewPolicyRepository 构造策略投影替身。
func NewPolicyRepository() *PolicyRepository {
	return &PolicyRepository{
		roleRules:   map[string][]ports.PolicyPermission{},
		memberRules: map[string][]string{},
	}
}

// SetBumpError 注入版本号递增失败。
func (r *PolicyRepository) SetBumpError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bumpErr = err
}

// ReplaceRoleRules 整体替换角色 p 规则。
func (r *PolicyRepository) ReplaceRoleRules(_ context.Context, _, roleID string, perms []ports.PolicyPermission) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.roleRules[roleID] = append([]ports.PolicyPermission{}, perms...)
	return nil
}

// ReplaceMemberRules 整体替换成员 g 规则。
func (r *PolicyRepository) ReplaceMemberRules(_ context.Context, tenantID, userID string, roleIDs []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.memberRules[tenantID+"|"+userID] = append([]string{}, roleIDs...)
	return nil
}

// DeleteRoleRules 删除角色 p 规则与引用它的 g 规则。
func (r *PolicyRepository) DeleteRoleRules(_ context.Context, _, roleID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.roleRules, roleID)

	for key, roleIDs := range r.memberRules {
		kept := make([]string, 0, len(roleIDs))
		for _, id := range roleIDs {
			if id != roleID {
				kept = append(kept, id)
			}
		}
		r.memberRules[key] = kept
	}
	return nil
}

// DeleteMemberRules 删除成员 g 规则，即撤权。
func (r *PolicyRepository) DeleteMemberRules(_ context.Context, tenantID, userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.memberRules, tenantID+"|"+userID)
	return nil
}

// DeleteTenantRules 删除租户全部规则。
func (r *PolicyRepository) DeleteTenantRules(_ context.Context, _ string) error { return nil }

// BumpVersion 递增版本号。
func (r *PolicyRepository) BumpVersion(_ context.Context) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bumpErr != nil {
		return 0, r.bumpErr
	}
	r.version++
	return r.version, nil
}

// CurrentVersion 读取版本号。
func (r *PolicyRepository) CurrentVersion(_ context.Context) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.version, nil
}

// MemberRules 返回某用户在指定租户下的 g 规则，供断言使用。
func (r *PolicyRepository) MemberRules(tenantID, userID string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.memberRules[tenantID+"|"+userID]...)
}

// RoleRules 返回某角色的 p 规则，供断言使用。
func (r *PolicyRepository) RoleRules(roleID string) []ports.PolicyPermission {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ports.PolicyPermission{}, r.roleRules[roleID]...)
}

// AuditRepository 是审计仓储的内存替身。
//
// 断言审计内容时直接读取 Items / Entries；
// 需要制造失败时用 SetCreateError 验证"审计写失败则操作回滚"。
type AuditRepository struct {
	mu sync.Mutex
	// Entries 是已写入的审计记录，按写入顺序排列。
	Entries []*audit.Entry
	// createErr 非空时模拟写入失败。
	createErr error
}

// NewAuditRepository 构造审计仓储替身。
func NewAuditRepository() *AuditRepository {
	return &AuditRepository{}
}

// SetCreateError 注入审计写入失败。
func (r *AuditRepository) SetCreateError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.createErr = err
}

// Create 追加一条审计记录。
func (r *AuditRepository) Create(_ context.Context, e *audit.Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.createErr != nil {
		return r.createErr
	}
	r.Entries = append(r.Entries, e)
	return nil
}

// ListByTenant 返回指定租户的审计记录，忽略过滤条件。
func (r *AuditRepository) ListByTenant(_ context.Context, tenantID string, _ ports.AuditFilter) ([]*audit.Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	result := make([]*audit.Entry, 0, len(r.Entries))
	for _, e := range r.Entries {
		if e.TenantID != nil && e.TenantID.String() == tenantID {
			result = append(result, e)
		}
	}
	return result, nil
}

// CountByTenant 返回指定租户的审计记录数。
func (r *AuditRepository) CountByTenant(_ context.Context, tenantID string, _ ports.AuditFilter) (int64, error) {
	entries, _ := r.ListByTenant(context.Background(), tenantID, ports.AuditFilter{})
	return int64(len(entries)), nil
}

// Count 返回全部审计记录数。
func (r *AuditRepository) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.Entries)
}

// Actions 返回已记录的操作类型，便于断言留痕覆盖面。
func (r *AuditRepository) Actions() []audit.Action {
	r.mu.Lock()
	defer r.mu.Unlock()

	actions := make([]audit.Action, 0, len(r.Entries))
	for _, e := range r.Entries {
		actions = append(actions, e.Action)
	}
	return actions
}

// HasAction 报告是否记录过指定类型的操作。
func (r *AuditRepository) HasAction(action audit.Action) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.Entries {
		if e.Action == action {
			return true
		}
	}
	return false
}

// Transactor 直接执行回调的事务替身。
type Transactor struct {
	// CommitErr 非空时模拟提交失败。
	CommitErr error
}

// WithinTx 执行回调，回调成功后按 CommitErr 决定是否失败。
func (t *Transactor) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if err := fn(ctx); err != nil {
		return err
	}
	return t.CommitErr
}

// Reloader 记录策略刷新调用次数。
type Reloader struct {
	mu    sync.Mutex
	calls int
	err   error
}

// RefreshIfStale 记录一次刷新调用。
func (r *Reloader) RefreshIfStale(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return r.err
}

// SetError 注入刷新失败。
func (r *Reloader) SetError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

// Count 返回刷新调用次数。
func (r *Reloader) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// SeedSystemRole 创建单个系统角色，便于构造租户初始状态。
func SeedSystemRole(roles *RoleRepository, tenantID uuid.UUID, key string) *role.Role {
	rl, err := role.NewSystemRole(tenantID, key, key, "")
	if err != nil {
		panic("构造系统角色失败：" + err.Error())
	}
	if err := roles.Create(context.Background(), rl); err != nil {
		panic("创建系统角色失败：" + err.Error())
	}
	return rl
}

// SeedSystemRoles 为租户预置四个系统角色。
//
// 生产环境由"创建租户"用例完成预置；单测直接调用本方法构造等价初始状态，
// 否则接受邀请时找不到 owner/member 角色会失败。
func (f *Fixture) SeedSystemRoles(tenantID uuid.UUID) {
	for _, key := range []string{role.KeyOwner, role.KeyAdmin, role.KeyMember, role.KeyViewer} {
		SeedSystemRole(f.Roles, tenantID, key)
	}
}
