-- 业务租户表。
-- 租户只能由平台管理员创建，created_by 记录创建者（平台管理员），
-- 但平台管理员不会因此自动成为该租户成员，成员关系一律通过邀请建立。
CREATE TABLE tenants (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       VARCHAR(255) NOT NULL,
    slug       VARCHAR(64)  NOT NULL,
    status     VARCHAR(32)  NOT NULL DEFAULT 'active'
               CHECK (status IN ('active', 'suspended', 'deleted')),
    created_by UUID NOT NULL REFERENCES users (id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- slug 全局唯一，是租户的可读标识。
CREATE UNIQUE INDEX idx_tenants_slug ON tenants (slug);
CREATE INDEX idx_tenants_created_by ON tenants (created_by);
