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

cd "$(dirname "$0")/.."
ROOT="$(pwd)"

# --- 1. 识别架构 -----------------------------------------------------------
raw_arch="$(uname -m)"
case "$raw_arch" in
  x86_64|amd64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  armv7l|armv7)  ARCH="armv7" ;;
  *)
    echo "错误：无法识别的架构 '$raw_arch'" >&2
    echo "目前仅提供 amd64 与 arm64 的预编译二进制。" >&2
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

# --- 4. 校验二进制可用 -----------------------------------------------------
echo "==> 校验二进制"
"$BIN" -version

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
