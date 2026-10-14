-- 成员邀请表。
-- 遵循"先认证、后绑定"：邀请只绑定已通过 ZITADEL 认证且邮箱已验证的身份，
-- 邮箱不作为身份主键，仅用于与被验证令牌中的 email 比对。
CREATE TABLE invitations (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID NOT NULL REFERENCES tenants (id),
    email             VARCHAR(320) NOT NULL,
    -- 只保存随机令牌的 SHA-256，明文不在数据库中留存。
    token_hash        BYTEA NOT NULL,
    status            VARCHAR(32) NOT NULL DEFAULT 'pending'
                      CHECK (status IN ('pending', 'accepted', 'cancelled', 'expired')),
    invited_by_user_id UUID NOT NULL REFERENCES users (id),
    accepted_user_id  UUID REFERENCES users (id),
    expires_at        TIMESTAMPTZ NOT NULL,
    accepted_at       TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 幂等的数据库兜底：同一租户下同一邮箱只能存在一条 pending 邀请。
CREATE UNIQUE INDEX idx_invitations_pending_unique
    ON invitations (tenant_id, lower(email)) WHERE status = 'pending';
CREATE UNIQUE INDEX idx_invitations_token_hash ON invitations (token_hash);
CREATE INDEX idx_invitations_tenant_status ON invitations (tenant_id, status);
CREATE INDEX idx_invitations_email ON invitations (lower(email));
