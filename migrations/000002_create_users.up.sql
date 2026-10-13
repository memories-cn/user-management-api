-- 业务用户表：保存 ZITADEL 身份与本地业务资料的映射。
-- 身份属性（邮箱、手机号、验证状态）以 ZITADEL 为权威来源，本表只存展示副本。
CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    zitadel_user_id VARCHAR(255) NOT NULL,
    display_name    VARCHAR(255) NOT NULL DEFAULT '',
    avatar_url      TEXT         NOT NULL DEFAULT '',
    status          VARCHAR(32)  NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active', 'disabled')),
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- 外部身份唯一，是用户同步的幂等依据。
CREATE UNIQUE INDEX idx_users_zitadel_user_id ON users (zitadel_user_id);
