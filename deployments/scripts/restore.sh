#!/usr/bin/env bash
# 从 pg_dump 自定义格式备份还原 IAM 数据库。
#
# 用法：
#   ./restore.sh <备份文件> [目标 DATABASE_DSN]
#
# 环境变量：
#   DATABASE_DSN   未提供第二个参数时作为目标连接串
#   RESTORE_YES    设为 1 时跳过交互确认（供演练脚本使用）
#
# 版本兼容性由调用方保证：本脚本只负责还原，不判断 schema 世代。
# 需要校验迁移版本时请用 drill.sh——它在独立数据库上还原后再比对。
set -euo pipefail

DUMP="${1:-}"
TARGET_DSN="${2:-${DATABASE_DSN:-}}"

if [[ -z "$DUMP" || -z "$TARGET_DSN" ]]; then
    echo "用法：$0 <备份文件> [目标 DATABASE_DSN]" >&2
    exit 1
fi
if [[ ! -f "$DUMP" ]]; then
    echo "错误：备份文件不存在：$DUMP" >&2
    exit 1
fi

# 还原前记录目标库当前迁移版本，便于事后核对是否被旧结构覆盖。
TARGET_VERSION="$(psql "$TARGET_DSN" -Atc \
    'SELECT COALESCE((SELECT version FROM schema_migrations LIMIT 1), 0)' 2>/dev/null || echo 0)"

if [[ "${RESTORE_YES:-}" != "1" ]]; then
    echo "即将把 $DUMP 还原到目标库。"
    echo "目标库当前迁移版本：${TARGET_VERSION:-未知}（还原后以备份中的版本为准）"
    echo "该操作会先 DROP 目标库中的对象，不可撤销。"
    read -r -p "确认继续？输入 yes: " answer
    if [[ "$answer" != "yes" ]]; then
        echo "已取消。"
        exit 1
    fi
fi

echo "开始还原 $DUMP → 目标库"
# --clean --if-exists：DROP 已存在的对象，对象不存在时为空操作，
# 因此既可以还原到旧库也可以还原到新建的空库。
pg_restore --clean --if-exists --no-owner --no-privileges --dbname="$TARGET_DSN" "$DUMP"

RESTORED_VERSION="$(psql "$TARGET_DSN" -Atc \
    'SELECT COALESCE((SELECT version FROM schema_migrations LIMIT 1), 0)' 2>/dev/null || echo 0)"
echo "还原完成。迁移版本：${TARGET_VERSION:-未知} → ${RESTORED_VERSION:-未知}"

if [[ "$TARGET_VERSION" != "0" && "$RESTORED_VERSION" != "0" && "$RESTORED_VERSION" -lt "$TARGET_VERSION" ]]; then
    echo "警告：还原后的迁移版本低于还原前，说明备份比目标库旧。" >&2
    echo "      此时应改用更新的备份重做，否则代码与数据库结构会不同步。" >&2
    exit 2
fi
