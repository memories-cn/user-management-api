-- 全局策略版本号：每次 Casbin 策略投影发生变更时，在**同一个业务事务内**递增。
-- 其他实例通过比对版本号判断本地 enforcer 内存策略是否已过期，从而在不引入 Redis 的前提下
-- 实现跨实例的最终一致失效（Redis pub/sub watcher 留待 Phase 6）。
--
-- 单行表：CHECK (id = 1) 保证不会插入第二行造成版本号分裂。
CREATE TABLE policy_version (
    id         SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    version    BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO policy_version (id, version) VALUES (1, 0);
