-- 系统权限目录：最小粒度的可执行操作，采用 resource:action 形式。
-- 该表是系统级资源，没有 tenant_id —— 权限集合只能由系统迁移维护，
-- 租户管理员只能在既有集合内组合自定义角色，不能新建可执行权限（见 docs/TDD.md 3.5、10.2）。
CREATE TABLE permissions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code        VARCHAR(128) NOT NULL,
    resource    VARCHAR(64) NOT NULL,
    action      VARCHAR(64) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_permissions_code ON permissions (code);
CREATE INDEX idx_permissions_resource ON permissions (resource);

-- 种子数据：权限目录由系统维护，新增权限需通过新的版本化迁移。
INSERT INTO permissions (code, resource, action, description) VALUES
    ('tenant:read',     'tenant',     'read',     '查看租户信息'),
    ('tenant:update',   'tenant',     'update',   '修改租户信息'),
    ('tenant:delete',   'tenant',     'delete',   '删除租户'),
    ('member:read',     'member',     'read',     '查看租户成员'),
    ('member:invite',   'member',     'invite',   '邀请成员加入租户'),
    ('member:update',   'member',     'update',   '修改成员状态'),
    ('member:remove',   'member',     'remove',   '移除成员'),
    ('role:read',       'role',       'read',     '查看租户角色'),
    ('role:create',     'role',       'create',   '创建自定义角色'),
    ('role:update',     'role',       'update',   '修改角色'),
    ('role:delete',     'role',       'delete',   '删除角色'),
    ('role:assign',     'role',       'assign',   '分配成员角色'),
    ('permission:read', 'permission', 'read',     '查看权限目录'),
    ('permission:assign', 'permission', 'assign', '配置角色权限'),
    ('audit:read',      'audit',      'read',     '查看租户审计日志');
