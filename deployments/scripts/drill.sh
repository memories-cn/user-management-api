#!/usr/bin/env bash
# 备份恢复演练：备份 → 还原到独立库 → 校验迁移版本与关键表行数。
#
# 用法：
#   ./drill.sh [备份文件]     # 省略时先做一次新备份
#
# 环境变量：
#   DATABASE_DSN      必填，被演练的源数据库
#   DRILL_TARGET_DSN  可选，还原目标；默认在同集群建 iam_drill_<时间戳>
#   ADMIN_DSN         可选，用于创建/销毁演练库的管理连接串，默认与 DATABASE_DSN 同库
#
# 退出码：0 = 演练通过；非 0 = 失败，输出中会指出差异所在。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./backup.sh
source "$SCRIPT_DIR/backup.sh"

if [[ -z "${DATABASE_DSN:-}" ]]; then
    echo "错误：未设置 DATABASE_DSN" >&2
    exit 1
fi

BACKUP_FILE="${1:-}"
if [[ -z "$BACKUP_FILE" ]]; then
    BACKUP_FILE="$(ls -1t ./backups/iam-*.dump 2>/dev/null | head -1 || true)"
    if [[ -z "$BACKUP_FILE" ]]; then
        OUT_DIR="${1:-./backups}"
        mkdir -p "$OUT_DIR"
        STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
        BACKUP_FILE="$OUT_DIR/iam-$STAMP.dump"
        echo "开始备份 → $BACKUP_FILE"
        pg_dump --format=custom --no-owner --no-privileges \
            --dbname="$DATABASE_DSN" --file="$BACKUP_FILE"
    fi
fi
echo "演练使用备份：$BACKUP_FILE"

# 演练库与源库隔离：绝不能把 --clean 的还原打到源库上。
DRILL_DB="iam_drill_$(date -u +%Y%m%dT%H%M%SZ)"
ADMIN_DSN="${ADMIN_DSN:-$DATABASE_DSN}"
TARGET_DSN="${DRILL_TARGET_DSN:-}"

if [[ -z "$TARGET_DSN" ]]; then
    # 从管理连接串推导出演练库的连接串：同主机同端口，只换库名。
    ADMIN_BASE="${ADMIN_DSN%%\?*}"
    ADMIN_BASE="${ADMIN_BASE%/}"
    SCHEME_AND_CREDS="${ADMIN_BASE%/*}"
    TARGET_DSN="$SCHEME_AND_CREDS/$DRILL_DB?$(printf '%s' "${ADMIN_DSN#*\?}" | grep -o 'sslmode=[^&]*' || echo 'sslmode=disable')"
fi

cleanup() {
    local db_name
    db_name="$(basename "${TARGET_DSN%%\?*}")"
    # 只清理本脚本创建的演练库，绝不触碰源库。
    if [[ "$db_name" == iam_drill_* ]]; then
        echo "清理演练库 $db_name"
        psql "$ADMIN_DSN" -Atc "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '$db_name'" >/dev/null 2>&1 || true
        psql "$ADMIN_DSN" -Atc "DROP DATABASE IF EXISTS $db_name" >/dev/null 2>&1 || true
    fi
}
trap cleanup EXIT

echo "创建演练库 $DRILL_DB"
psql "$ADMIN_DSN" -Atc "CREATE DATABASE $DRILL_DB" >/dev/null

echo "还原到演练库"
RESTORE_YES=1 "$SCRIPT_DIR/restore.sh" "$BACKUP_FILE" "$TARGET_DSN"

# 校验迁移版本一致。
SRC_VERSION="$(psql "$DATABASE_DSN" -Atc 'SELECT version FROM schema_migrations LIMIT 1')"
DST_VERSION="$(psql "$TARGET_DSN" -Atc 'SELECT version FROM schema_migrations LIMIT 1')"
if [[ "$SRC_VERSION" != "$DST_VERSION" ]]; then
    echo "失败：迁移版本不一致（源 $SRC_VERSION vs 演练 $DST_VERSION）" >&2
    exit 1
fi

# 校验关键表行数一致。
TABLES="users tenants memberships invitations roles permissions role_permissions membership_roles casbin_rule audit_logs policy_version"
FAILED=0
for table in $TABLES; do
    SRC_COUNT="$(psql "$DATABASE_DSN" -Atc "SELECT COUNT(*) FROM $table")"
    DST_COUNT="$(psql "$TARGET_DSN" -Atc "SELECT COUNT(*) FROM $table")"
    if [[ "$SRC_COUNT" != "$DST_COUNT" ]]; then
        echo "失败：表 $table 行数不一致（源 $SRC_COUNT vs 演练 $DST_COUNT）" >&2
        FAILED=1
    else
        printf '  %-20s %s 行（一致）\n' "$table" "$SRC_COUNT"
    fi
done

if [[ "$FAILED" -ne 0 ]]; then
    echo "演练失败。" >&2
    exit 1
fi

echo "演练通过：备份可还原，迁移版本与关键表行数均一致。"
