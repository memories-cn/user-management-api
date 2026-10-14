-- 成员与租户角色的分配关系。一个成员可以拥有多个角色。
-- 两个复合外键是租户隔离的最后防线：
--   (membership_id, tenant_id) → memberships (id, tenant_id)
--   (role_id, tenant_id)       → roles (id, tenant_id)
-- 仅检查两个 UUID 是否存在是不够的，必须让数据库拒绝跨租户组合
-- （见 docs/TDD.md 3.6、8.2）。
CREATE TABLE membership_roles (
    membership_id UUID NOT NULL,
    role_id       UUID NOT NULL,
    tenant_id     UUID NOT NULL,
    assigned_by   UUID NOT NULL REFERENCES users (id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (membership_id, role_id),
    FOREIGN KEY (membership_id, tenant_id) REFERENCES memberships (id, tenant_id) ON DELETE CASCADE,
    FOREIGN KEY (role_id, tenant_id) REFERENCES roles (id, tenant_id) ON DELETE CASCADE
);

CREATE INDEX idx_membership_roles_tenant_id ON membership_roles (tenant_id);
CREATE INDEX idx_membership_roles_role_id ON membership_roles (role_id);
