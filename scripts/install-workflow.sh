#!/usr/bin/env bash
# ---------------------------------------------------------------------------
# 一键启用 GitHub Actions 自动构建
#
# 背景：GitHub 对 .github/workflows/ 下的文件有【独立的权限门禁】。
#   如果用于自动化推送的令牌只有 Contents 权限而没有 workflow 权限，
#   这个文件会被拒绝，报错形如：
#
#     refusing to allow a Personal Access Token to create or update workflow
#     `.github/workflows/build.yml` without `workflow` scope
#
#   该限制同时在 REST API 与 git 推送两条路径上生效，无法绕过。
#
# 本脚本用【你自己的 git 凭据】推送，不受上述限制。
#
# 用法（在仓库根目录执行）：
#   bash scripts/install-workflow.sh
# ---------------------------------------------------------------------------
set -euo pipefail

REPO_URL="https://github.com/PeACE2323/bt-traffic-guard.git"
WF_PATH=".github/workflows/build.yml"

# 定位仓库根目录。
#
# 不能用 $(dirname "$0")：它给出的是【相对于调用时工作目录】的路径，
# 用户从别处用相对路径调用时会算错；软链接调用也会算错。
#
# 因此按以下顺序尝试：
#   1. 用 git 定位 —— 最可靠，但要求是从本项目的 git 仓库里执行
#   2. 回退：解析软链接得到脚本真实路径，再取其上一级
locate_root() {
  local candidate=""
  if candidate="$(git rev-parse --show-toplevel 2>/dev/null)" && [ -n "$candidate" ]; then
    # 校验确实是本项目：模板副本随仓库分发，可作为标志物
    if [ -f "$candidate/scripts/build.yml.example" ] || [ -f "$candidate/Dockerfile.release" ]; then
      printf '%s' "$candidate"
      return 0
    fi
  fi

  local sp="$0"
  if command -v readlink >/dev/null 2>&1; then
    local guard=0
    while [ -L "$sp" ] && [ "$guard" -lt 40 ]; do
      local target
      target="$(readlink "$sp")"
      case "$target" in
        /*) sp="$target" ;;
        *)  sp="$(dirname "$sp")/$target" ;;
      esac
      guard=$((guard + 1))
    done
  fi
  (cd "$(dirname "$sp")/.." && pwd)
}

ROOT="$(locate_root)"
cd "$ROOT"

if [ ! -f "$WF_PATH" ]; then
  # 工作树里没有 workflow 文件时，从随仓库分发的模板副本恢复。
  # 这样即使它因分支操作被意外删除，也能自动复原。
  if [ -f "$ROOT/scripts/build.yml.example" ]; then
    echo "==> 工作树中缺少 $WF_PATH，正在从 scripts/build.yml.example 恢复"
    mkdir -p "$(dirname "$WF_PATH")"
    cp "$ROOT/scripts/build.yml.example" "$WF_PATH"
    echo "    已恢复"
  else
    echo "错误：在 $ROOT 下找不到 $WF_PATH" >&2
    echo "也找不到用于恢复的 scripts/build.yml.example。" >&2
    echo "请确认你在本仓库的根目录内执行本脚本。" >&2
    exit 1
  fi
fi

echo "==> 仓库根目录: $(pwd)"
echo
echo "==> 确保 origin 指向正确的仓库"
if ! git remote get-url origin >/dev/null 2>&1; then
  git remote add origin "$REPO_URL"
  echo "    已添加 origin"
else
  cur=$(git remote get-url origin)
  echo "    当前 origin: $cur"
  if [ "$cur" != "$REPO_URL" ]; then
    echo "    与预期不符，改为: $REPO_URL"
    git remote set-url origin "$REPO_URL"
  fi
fi

echo
echo "==> 暂存 workflow 文件"
git add "$WF_PATH"
git status --short "$WF_PATH" || true

echo
echo "==> 提交"
if git diff --cached --quiet; then
  echo "    该文件已在版本库中且无改动，跳过提交。"
else
  git commit -m "ci: 添加 GitHub Actions 自动构建并发布镜像到 GHCR

推送 main 或打 v* 标签时自动运行测试、构建 amd64/arm64 双架构镜像，
并推送到 ghcr.io/peace2323/bt-traffic-guard。
测试不通过则不会构建镜像。"
fi

echo
echo "==> 推送（使用你本机已保存的 git 凭据）"
git push origin HEAD

cat <<'EOF'

-----------------------------------------------------------------------
完成。

查看构建进度：
  https://github.com/PeACE2323/bt-traffic-guard/actions

构建成功后的镜像地址：
  ghcr.io/peace2323/bt-traffic-guard:latest

【重要】首次发布后，请把 Package 可见性改为 Public，
否则 docker pull 会报 denied：
  https://github.com/users/PeACE2323/packages/container/bt-traffic-guard/settings

如果推送时仍报 "without `workflow` scope"：
  说明你本机的 git 凭据是【细粒度 PAT】且缺少 workflow 权限。
  解决办法（任选其一）：
   1) 给该令牌加上 Workflows: Read and write
      https://github.com/settings/personal-access-tokens
   2) 改用 SSH 推送：
      git remote set-url origin git@github.com:PeACE2323/bt-traffic-guard.git
      git push origin HEAD
   3) 直接在网页上创建该文件（复制本仓库 .github/workflows/build.yml 的内容）：
      https://github.com/PeACE2323/bt-traffic-guard/new/main/.github/workflows
-----------------------------------------------------------------------
EOF
