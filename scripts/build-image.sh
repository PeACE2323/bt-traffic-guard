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
# 注意：不能用 $(dirname "$0")，因为用户可能通过软链接调用，
# 或用 `bash /path/to/scripts/build-image.sh` 从别处执行。
# 优先用 git 定位（最可靠），失败时回退到脚本自身位置。
if ROOT="$(git rev-parse --show-toplevel 2>/dev/null)"; then
  :
else
  script_path="$0"
  # 走软链接时解析出真实路径
  if command -v readlink >/dev/null 2>&1; then
    while [ -L "$script_path" ]; do
      target="$(readlink "$script_path")"
      case "$target" in
        /*) script_path="$target" ;;
        *)  script_path="$(dirname "$script_path")/$target" ;;
      esac
    done
  fi
  ROOT="$(cd "$(dirname "$script_path")/.." && pwd)"
fi
cd "$ROOT"

if [ ! -f "$ROOT/Dockerfile.release" ]; then
  echo "错误：在 $ROOT 下找不到 Dockerfile.release" >&2
  echo "请确认你在本仓库的根目录内执行本脚本。" >&2
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
size="$(wc -c < "$BIN" | tr -d ' ')"
if [ "$size" -lt 1000000 ]; then
  echo "错误：下载的文件只有 $size 字节，明显不是有效二进制。" >&2
  echo "可能是 release 尚未发布，或架构 $ARCH 没有对应产物。" >&2
  echo "手动检查：$URL" >&2
  exit 1
fi
# 确认是 ELF（前 4 字节 0x7F 'E' 'L' 'F'）
magic="$(head -c 4 "$BIN" | od -An -tx1 | tr -d ' \n')"
if [ "$magic" != "7f454c46" ]; then
  echo "错误：下载的不是有效的 ELF 二进制（magic=$magic）。" >&2
  exit 1
fi
echo "    大小 $(echo "$size" | awk '{printf "%.2f MB", $1/1024/1024}')，ELF 校验通过"

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
