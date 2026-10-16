# ---------------------------------------------------------------------------
# 构建阶段：静态编译出单个二进制，不带任何运行时依赖
# ---------------------------------------------------------------------------
FROM golang:1.24-alpine AS builder

# 国内构建环境可通过 --build-arg GOPROXY=... 指定镜像源
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY} \
    CGO_ENABLED=0 \
    GOOS=linux

WORKDIR /src

# 先只拷贝依赖清单，利用 Docker 层缓存加速重复构建
COPY go.mod go.sum* ./
RUN go mod download

# 再拷贝源码并编译
COPY cmd/ ./cmd/
COPY internal/ ./internal/

ARG VERSION=dev
RUN go build \
        -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/bt-traffic-guard \
        ./cmd/bt-traffic-guard \
 && /out/bt-traffic-guard -version

# ---------------------------------------------------------------------------
# 运行阶段：极简镜像
# ---------------------------------------------------------------------------
FROM alpine:3.20

# ca-certificates 用于 HTTPS 推送通知（ntfy / webhook）
# tzdata 用于正确的本地时区与自然日周期边界计算
RUN apk add --no-cache ca-certificates tzdata wget \
 && addgroup -g 1000 -S btg \
 && adduser -u 1000 -S btg -G btg \
 && mkdir -p /config /data \
 && chown -R btg:btg /config /data

COPY --from=builder /out/bt-traffic-guard /usr/local/bin/bt-traffic-guard

# 默认时区，可通过 TZ 环境变量覆盖
ENV TZ=Asia/Shanghai \
    BTG_CONFIG=/config/config.yaml \
    BTG_DATA_DIR=/data \
    BTG_LISTEN=:8080

# 配置目录与数据目录，建议都做持久化挂载
VOLUME ["/config", "/data"]

EXPOSE 8080

# healthcheck 使用镜像内自带的 wget，避免额外依赖
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/api/healthz || exit 1

USER btg

ENTRYPOINT ["/usr/local/bin/bt-traffic-guard"]
CMD ["-config", "/config/config.yaml"]
