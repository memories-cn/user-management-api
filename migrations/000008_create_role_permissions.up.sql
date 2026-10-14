-- 角色与系统权限的关联。
-- tenant_id 是冗余列：它的唯一目的是支撑复合外键 (role_id, tenant_id) → roles (id, tenant_id)，
-- 让数据库拒绝"把 A 租户的权限挂到 B 租户角色上"的写入，而不是依赖应用层自觉
-- （见 docs/TDD.md 8.2）。
CREATE TABLE role_permissions (
    role_id       UUID NOT NULL,
    permission_id UUID NOT NULL REFERENCES permissions (id),
    tenant_id     UUID NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (role_id, permission_id),
    FOREIGN KEY (role_id, tenant_id) REFERENCES roles (id, tenant_id) ON DELETE CASCADE
);

CREATE INDEX idx_role_permissions_tenant_id ON role_permissions (tenant_id);
CREATE INDEX idx_role_permissions_permission_id ON role_permissions (permission_id);
