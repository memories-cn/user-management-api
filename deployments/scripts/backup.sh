#!/usr/bin/env bash
# 对 IAM 数据库执行一次逻辑备份（pg_dump --format=custom）。
#
# 用法：
#   ./backup.sh [输出目录]
#
# 环境变量：
#   DATABASE_DSN   必填，PostgreSQL 连接串（需有备份权限的账号）
#   BACKUP_KEEP    可选，保留最近几份备份，默认 7
#
# 输出：<输出目录>/iam-<UTC 时间戳>.dump，并更新 latest 符号链接。
set -euo pipefail

OUT_DIR="${1:-./backups}"
KEEP="${BACKUP_KEEP:-7}"

if [[ -z "${DATABASE_DSN:-}" ]]; then
    echo "错误：未设置 DATABASE_DSN" >&2
    exit 1
fi

mkdir -p "$OUT_DIR"

STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
TARGET="$OUT_DIR/iam-$STAMP.dump"

echo "开始备份 → $TARGET"
# --no-owner / --no-privileges：还原时按目标库的既有角色授权，
# 避免把源库的角色对象带到一个角色定义不同的环境。
pg_dump --format=custom --no-owner --no-privileges --dbname="$DATABASE_DSN" --file="$TARGET"

ln -sfn "iam-$STAMP.dump" "$OUT_DIR/latest"

# 只清理本脚本产生的备份文件，避免误删同目录下的其他产物。
ls -1t "$OUT_DIR"/iam-*.dump 2>/dev/null | tail -n +"$((KEEP + 1))" | xargs -r rm -f --

echo "备份完成：$TARGET（保留最近 $KEEP 份）"
