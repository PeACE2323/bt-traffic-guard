#!/usr/bin/env bash
# ---------------------------------------------------------------------------
# 一键构建镜像（无需 Go 环境）
#
# 做三件事：
#   1. 识别本机架构（amd64 / arm64）
#   2. 从 GitHub Release 下载对应的预编译二进制
#   3. 用 Dockerfile.release 打成镜像
#
# 用法：
#   bash scripts/build-image.sh              # 默认 tag: bt-traffic-guard:latest
#   bash scripts/build-image.sh my/tag:1.0   # 自定义 tag
#
# 前提：本机已装 docker。不需要装 Go。
# ---------------------------------------------------------------------------
set -euo pipefail

REPO="PeACE2323/bt-traffic-guard"
IMAGE_TAG="${1:-bt-traffic-guard:latest}"

# 定位仓库根目录。
#
# 不能用 $(dirname "$0")：它给出的是【相对于调用时工作目录】的路径，
# 用户从别处用相对路径调用时会算错；软链接调用也会算错。
#
# 因此按以下顺序尝试：
#   1. 用 git 定位 —— 最可靠，但要求是从 git 仓库里执行
#   2. 回退：解析软链接得到脚本真实路径，再取其上一级
#
# 注意 git 分支要校验结果里确实有 Dockerfile.release，
# 否则用户可能在【另一个】恰好包含本脚本副本的仓库里执行，
# 那样会定位到错误的根目录。
locate_root() {
  local candidate=""
  if candidate="$(git rev-parse --show-toplevel 2>/dev/null)" && [ -n "$candidate" ]; then
    if [ -f "$candidate/Dockerfile.release" ]; then
      printf '%s' "$candidate"
      return 0
    fi
  fi

  # 回退：先解析软链接链，拿到脚本的真实路径
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
  # 取脚本所在目录的上一级（scripts/ -> 仓库根）
  (cd "$(dirname "$sp")/.." && pwd)
}

ROOT="$(locate_root)"
cd "$ROOT"

if [ ! -f "$ROOT/Dockerfile.release" ]; then
  echo "错误：在 $ROOT 下找不到 Dockerfile.release" >&2
  echo "请确认你在本仓库的根目录内执行本脚本。" >&2
  echo "（若你是解压 tar 包得到的目录，直接 cd 进去再执行即可。）" >&2
  exit 1
fi

# --- 1. 识别架构 -----------------------------------------------------------
raw_arch="$(uname -m)"
case "$raw_arch" in
  x86_64|amd64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  armv7l|armv7)  ARCH="armv7" ;;
  *)
    echo "错误：无法识别的架构 '$raw_arch'" >&2
    echo "目前仅提供 amd64 / arm64 / armv7 的预编译二进制。" >&2
    echo "你可以改用源码构建：docker build -t $IMAGE_TAG ." >&2
    exit 1
    ;;
esac

echo "==> 检测到架构: $raw_arch  ->  $ARCH"

# --- 2. 检查 docker --------------------------------------------------------
if ! command -v docker >/dev/null 2>&1; then
  echo "错误：未找到 docker。请先安装 Docker。" >&2
  exit 1
fi

# --- 3. 下载二进制 ---------------------------------------------------------
BIN="$ROOT/bt-traffic-guard"
if [ -f "$BIN" ] && [ "${FORCE_DOWNLOAD:-0}" != "1" ]; then
  echo "==> 已存在 $BIN，跳过下载（如需重新下载：FORCE_DOWNLOAD=1 bash $0）"
else
  URL="https://github.com/$REPO/releases/latest/download/bt-traffic-guard-linux-$ARCH"
  echo "==> 下载预编译二进制"
  echo "    $URL"
  if command -v curl >/dev/null 2>&1; then
    curl -fL --progress-bar -o "$BIN" "$URL"
  elif command -v wget >/dev/null 2>&1; then
    wget -O "$BIN" "$URL"
  else
    echo "错误：需要 curl 或 wget 来下载二进制。" >&2
    exit 1
  fi
  chmod +x "$BIN"
fi

# --- 4. 校验产物基本合法 ---------------------------------------------------
# 只做静态校验，不实际执行二进制：
# 用户可能是在 macOS 上构建给 NAS 用的镜像，此时 Linux 二进制无法在本机运行。
echo "==> 校验产物"
#
# 注意：这里刻意不用 `head -c 4` 与 `od -An -tx1`。
# 前者是 GNU 扩展（POSIX 只保证 head -n），后者在部分精简系统上
# 的选项写法不一致。NAS 环境五花八门（群晖 DSM、QNAP、Unraid、树莓派），
# 用 dd 读前 4 字节再按字符串比较是最稳的写法。
if command -v wc >/dev/null 2>&1; then
  size="$(wc -c < "$BIN" | tr -d ' ')"
else
  size=0
fi
if [ "${size:-0}" -lt 1000000 ]; then
  echo "错误：下载的文件只有 ${size:-未知} 字节，明显不是有效二进制。" >&2
  echo "可能是 release 尚未发布，或架构 $ARCH 没有对应产物。" >&2
  echo "手动检查：$URL" >&2
  exit 1
fi

# 确认是 ELF：前 4 字节应为 0x7F 'E' 'L' 'F'
elf_magic() {
  # dd 用 bs=1 count=4 读取文件头；2>/dev/null 屏蔽统计信息
  dd if="$1" bs=1 count=4 2>/dev/null | od -An -c 2>/dev/null | tr -d ' \n'
}
magic="$(elf_magic "$BIN")"
case "$magic" in
  177ELF*)
    : # 匹配（od -c 把 0x7F 显示为 177）
    ;;
  *)
    # 备用判定：直接看文件是否以 ELF 开头
    if [ "$(dd if="$BIN" bs=1 count=4 2>/dev/null)" = "$(printf '\177ELF')" ]; then
      :
    else
      echo "错误：下载的不是有效的 ELF 二进制（文件头 = $magic）。" >&2
      echo "可能下载到了错误页面而非二进制。请手动检查：" >&2
      echo "  $URL" >&2
      exit 1
    fi
    ;;
esac

if command -v awk >/dev/null 2>&1; then
  echo "    大小 $(awk "BEGIN{printf \"%.2f MB\", $size/1024/1024}")，ELF 校验通过"
else
  echo "    大小 $size 字节，ELF 校验通过"
fi

# --- 5. 构建镜像 -----------------------------------------------------------
echo "==> 构建镜像: $IMAGE_TAG"
docker build -f Dockerfile.release -t "$IMAGE_TAG" .

cat <<EOF

-----------------------------------------------------------------------
构建完成。

启动：

  docker run -d \\
    --name bt-traffic-guard \\
    --restart unless-stopped \\
    -p 8080:8080 \\
    -e TZ=Asia/Shanghai \\
    -v /mnt/user/appdata/bt-traffic-guard/config:/config \\
    -v /mnt/user/appdata/bt-traffic-guard/data:/data \\
    $IMAGE_TAG

然后打开 http://<NAS 的 IP>:8080 ，在「配置」标签页里填写
qBittorrent / Transmission 的连接信息即可，不需要预先设置环境变量。
-----------------------------------------------------------------------
EOF
