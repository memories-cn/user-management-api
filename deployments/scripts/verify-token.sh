#!/usr/bin/env bash
# 上线前人工门禁：用真实 ZITADEL 访问令牌核对认证链路。
#
# 为什么是人工门禁：CI 中没有可用的 ZITADEL 实例，
# 而 docs/TDD.md 16 要求"ZITADEL Token 验证通过真实 Token 集成测试"。
# 桩只能验证"我们的验签逻辑"，验证不了"我们的 Issuer/Audience/角色声明
# 与真实 ZITADEL 配置一致"——后者只能由真实令牌确认。
#
# 用法：
#   IAM_BASE_URL=http://localhost:8080 \
#   ZITADEL_TOKEN=<真实访问令牌> ./verify-token.sh
#
# 可选：
#   PLATFORM_ADMIN_TOKEN=<真实平台管理员令牌>   额外核对平台路径
set -euo pipefail

BASE="${IAM_BASE_URL:-http://localhost:8080}"
TOKEN="${ZITADEL_TOKEN:-}"
PLATFORM_TOKEN="${PLATFORM_ADMIN_TOKEN:-}"

if [[ -z "$TOKEN" ]]; then
    echo "错误：未设置 ZITADEL_TOKEN" >&2
    exit 1
fi

FAILED=0

check() {
    local desc="$1" expected="$2" method="$3" path="$4" body="${5:-}"
    local args=(-s -o /tmp/verify-token-body -w '%{http_code}' -X "$method")
    args+=("$BASE$path" -H "Authorization: Bearer $TOKEN")
    if [[ -n "$body" ]]; then
        args+=(-H 'Content-Type: application/json' -d "$body")
    fi
    local code
    code="$(curl "${args[@]}")"
    if [[ "$code" == "$expected" ]]; then
        printf '  [OK]   %-46s → %s\n' "$desc" "$code"
    else
        printf '  [FAIL] %-46s → %s（期望 %s）\n' "$desc" "$code" "$expected"
        echo "        响应体：$(cat /tmp/verify-token-body)"
        FAILED=1
    fi
}

check_platform() {
    local desc="$1" expected="$2" method="$3" path="$4" body="${5:-}"
    local args=(-s -o /tmp/verify-token-body -w '%{http_code}' -X "$method")
    args+=("$BASE$path" -H "Authorization: Bearer $PLATFORM_TOKEN")
    if [[ -n "$body" ]]; then
        args+=(-H 'Content-Type: application/json' -d "$body")
    fi
    local code
    code="$(curl "${args[@]}")"
    if [[ "$code" == "$expected" ]]; then
        printf '  [OK]   %-46s → %s\n' "$desc" "$code"
    else
        printf '  [FAIL] %-46s → %s（期望 %s）\n' "$desc" "$code" "$expected"
        echo "        响应体：$(cat /tmp/verify-token-body)"
        FAILED=1
    fi
}

echo "== 认证链路核对（目标 $BASE）=="

echo "-- 无凭证必须被拒绝 --"
code="$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/v1/me")"
if [[ "$code" == "401" ]]; then
    printf '  [OK]   %-46s → %s\n' "GET /api/v1/me（无令牌）" "$code"
else
    printf '  [FAIL] %-46s → %s（期望 401）\n' "GET /api/v1/me（无令牌）" "$code"
    FAILED=1
fi

echo "-- 有效令牌应能通过认证 --"
check "GET /api/v1/me" 200 GET /api/v1/me
check "GET /api/v1/tenants" 200 GET /api/v1/tenants

echo "-- 伪造/篡改的令牌必须被拒绝 --"
check "GET /api/v1/me（令牌追加字符）" 401 GET /api/v1/me 2>/dev/null || true
TAMPERED="${TOKEN}x"
code="$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/v1/me" -H "Authorization: Bearer $TAMPERED")"
if [[ "$code" == "401" ]]; then
    printf '  [OK]   %-46s → %s\n' "GET /api/v1/me（篡改令牌）" "$code"
else
    printf '  [FAIL] %-46s → %s（期望 401）\n' "GET /api/v1/me（篡改令牌）" "$code"
    FAILED=1
fi

echo
echo "== 手动核对项（脚本无法替代）=="
cat <<'EOF'
  [ ] 解码访问令牌，确认 iss 与 ZITADEL_ISSUER 完全一致
  [ ] 确认 aud 包含 ZITADEL_API_AUDIENCE
  [ ] 确认 alg 为 RS256 且在允许列表内
  [ ] 若需要平台管理员：确认存在 urn:zitadel:iam:user:roles 且含配置的角色 key
  [ ] 确认令牌 exp 符合预期（过期令牌应返回 401）
EOF

if [[ -n "$PLATFORM_TOKEN" ]]; then
    echo
    echo "== 平台路径核对 =="
    code="$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/v1/platform/tenants" -X POST \
        -H "Authorization: Bearer $PLATFORM_TOKEN" -H 'Content-Type: application/json' \
        -d '{"name":"核对用","slug":"token-verify","owner_email":"verify@example.com"}')"
    if [[ "$code" == "200" ]]; then
        printf '  [OK]   %-46s → %s\n' "POST /platform/tenants（平台管理员）" "$code"
    else
        printf '  [FAIL] %-46s → %s（期望 200）\n' "POST /platform/tenants（平台管理员）" "$code"
        FAILED=1
    fi
fi

echo
if [[ "$FAILED" -ne 0 ]]; then
    echo "核对未通过，请勿上线。"
    exit 1
fi
echo "自动核对通过。请再完成上方的手动核对项。"
