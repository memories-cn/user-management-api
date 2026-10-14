-- 用户与租户的成员关系，是租户权限的核心边界。
-- 采用软删除（status='removed'），因此 (tenant_id, user_id) 唯一约束必须保留被移除的行，
-- 重新邀请时走"重新激活"而不是插入。
CREATE TABLE memberships (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID NOT NULL REFERENCES tenants (id),
    user_id    UUID NOT NULL REFERENCES users (id),
    status     VARCHAR(32) NOT NULL DEFAULT 'active'
               CHECK (status IN ('active', 'pending', 'suspended', 'removed')),
    joined_at  TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_memberships_tenant_user ON memberships (tenant_id, user_id);
CREATE INDEX idx_memberships_tenant_status ON memberships (tenant_id, status);
CREATE INDEX idx_memberships_user_id ON memberships (user_id);
