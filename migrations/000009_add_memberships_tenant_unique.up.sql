-- 为 memberships 补 (id, tenant_id) 唯一约束，供 membership_roles 的复合外键引用。
-- 这不是新的业务规则：id 已是主键，追加 tenant_id 后仍唯一，但让数据库可以强制
-- "成员角色分配与成员关系属于同一个租户"（见 docs/TDD.md 8.2）。
CREATE UNIQUE INDEX IF NOT EXISTS idx_memberships_id_tenant ON memberships (id, tenant_id);
