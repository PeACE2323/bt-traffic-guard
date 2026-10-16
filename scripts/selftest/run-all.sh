#!/usr/bin/env bash
# ---------------------------------------------------------------------------
# 本地自检：一条命令跑完所有非 Go 的自动化校验
#
# 包含：
#   validate-yaml.cjs        配置文件结构与关键字段断言
#   verify-script-logic.cjs  build-image.sh 的架构映射与产物校验逻辑
#   test-frontend.cjs        实际执行前端 JS，检查语法/运行时/纯函数
#   test-roundtrip.cjs       打真实 HTTP 接口验证表单掩码语义（需服务在跑）
#
# 用法：
#   bash scripts/selftest/run-all.sh
#
# Go 侧的测试请另行执行：
#   go test ./...
# ---------------------------------------------------------------------------
set -euo pipefail

# 定位仓库根目录（优先用 git，回退到脚本位置）
if ROOT="$(git rev-parse --show-toplevel 2>/dev/null)"; then
  :
else
  ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
fi
cd "$ROOT"

if ! command -v node >/dev/null 2>&1; then
  echo "错误：需要 node 来运行这些校验脚本。" >&2
  exit 1
fi

SELF="$ROOT/scripts/selftest"
fail=0

run() {
  local name="$1"; shift
  echo
  echo "=============================================================="
  echo "  $name"
  echo "=============================================================="
  if "$@"; then
    echo "  -> 通过"
  else
    echo "  -> 失败"
    fail=$((fail + 1))
  fi
}

run "配置文件校验"        node "$SELF/validate-yaml.cjs" "$ROOT"
run "构建脚本逻辑校验"    node "$SELF/verify-script-logic.cjs" "$ROOT/.tools"
run "前端脚本校验"        node "$SELF/test-frontend.cjs" "$ROOT"

# ELF 校验需要已构建的产物（由 build-image.sh 下载，或本地交叉编译得到）
if [ -f "$ROOT/bt-traffic-guard" ]; then
  case "$(uname -m)" in
    x86_64|amd64)  _a=amd64 ;;
    aarch64|arm64) _a=arm64 ;;
    armv7l|armv7)  _a=armv7 ;;
    *)             _a=amd64 ;;
  esac
  run "产物 ELF 校验"      node "$SELF/verify-elf.cjs" "$ROOT/bt-traffic-guard:$_a"
else
  echo
  echo "=============================================================="
  echo "  产物 ELF 校验 —— 已跳过"
  echo "=============================================================="
  echo "  未找到 $ROOT/bt-traffic-guard"
  echo "  可先执行 bash scripts/build-image.sh 下载产物，或手动指定："
  echo "    node scripts/selftest/verify-elf.cjs <文件路径>:<amd64|arm64|armv7>"
fi

# 往返测试需要一个运行中的实例。若未提供地址则跳过。
if [ -n "${BTG_TEST_URL:-}" ]; then
  run "表单往返测试"      node "$SELF/test-roundtrip.cjs" "$BTG_TEST_URL"
else
  echo
  echo "=============================================================="
  echo "  表单往返测试 —— 已跳过"
  echo "=============================================================="
  echo "  如需运行，请先启动一个实例，然后："
  echo "    BTG_TEST_URL=http://127.0.0.1:8080 bash scripts/selftest/run-all.sh"
fi

echo
if [ "$fail" -eq 0 ]; then
  echo "*** 全部自检通过 ***"
else
  echo "*** 有 $fail 组校验失败 ***"
  exit 1
fi
