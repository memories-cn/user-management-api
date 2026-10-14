-- 租户内的角色定义。角色归属于租户，key 在租户内唯一。
-- is_system 标记系统预置角色（owner/admin/member/viewer）：不可删除、不可改 key，
-- 只允许调整 name/description 与权限集合（见 docs/TDD.md 3.4、10.2）。
--
-- UNIQUE (id, tenant_id) 不是业务约束，而是为 role_permissions 的复合外键提供引用目标，
-- 从而在数据库层保证"被分配的角色与关联表属于同一个租户"。
CREATE TABLE roles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenants (id),
    key         VARCHAR(64) NOT NULL,
    name        VARCHAR(255) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    is_system   BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_roles_tenant_key ON roles (tenant_id, key);
CREATE UNIQUE INDEX idx_roles_id_tenant ON roles (id, tenant_id);
CREATE INDEX idx_roles_tenant_id ON roles (tenant_id);
