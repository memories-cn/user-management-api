-- 审计记录表：记录高风险管理操作与安全相关事件（见 docs/TDD.md 3.7）。
--
-- 设计约束：
--   - 只追加：不提供更新与删除接口，也没有 updated_at 与软删除字段，
--     任何修改都会破坏审计记录本身的可信度；
--   - tenant_id 可空：平台级操作（创建租户、停用租户）不属于任何租户内部，
--     但绝不能把空 tenant_id 当作隐式提权通道使用；
--   - 不建到 tenants 的外键：审计应保留租户被清理之后的历史；
--   - metadata 为 JSONB，默认空对象，且不得包含令牌、密码等凭据。
CREATE TABLE audit_logs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID,
    actor_user_id UUID         NOT NULL REFERENCES users(id),
    action        VARCHAR(64)  NOT NULL,
    resource_type VARCHAR(64)  NOT NULL,
    resource_id   VARCHAR(128) NOT NULL DEFAULT '',
    result        VARCHAR(32)  NOT NULL DEFAULT 'success'
                  CHECK (result IN ('success', 'denied', 'error')),
    request_id    VARCHAR(128) NOT NULL DEFAULT '',
    ip_address    VARCHAR(64)  NOT NULL DEFAULT '',
    user_agent    TEXT         NOT NULL DEFAULT '',
    metadata      JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- 主查询路径：按租户浏览最近的操作。
CREATE INDEX idx_audit_logs_tenant_created ON audit_logs (tenant_id, created_at DESC);
-- 对账路径：查某个操作者做了什么。
CREATE INDEX idx_audit_logs_tenant_actor ON audit_logs (tenant_id, actor_user_id);
-- 合规路径：查某类高风险操作的全部记录。
CREATE INDEX idx_audit_logs_tenant_action ON audit_logs (tenant_id, action);
