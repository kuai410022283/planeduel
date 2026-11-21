# 纸上打飞机 · 容器镜像
# 多阶段：Go 编译 → 精简运行镜像（非 root 运行）

FROM golang:1.27-alpine AS build
WORKDIR /src
ARG VERSION=dev
ENV CGO_ENABLED=0 GOFLAGS=-mod=mod
COPY go.mod ./
RUN go mod download 2>/dev/null || true
COPY . .
RUN go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/planeduel ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache su-exec ca-certificates tzdata && \
    addgroup -S -g 1000 planeduel 2>/dev/null || true && \
    adduser -S -u 1000 -G planeduel planeduel 2>/dev/null || true
WORKDIR /app
COPY --from=build /out/planeduel /app/planeduel
COPY LICENSE /app/
COPY deploy/entrypoint.sh /entrypoint.sh
RUN chmod 0755 /entrypoint.sh

VOLUME ["/data"]
# 28082 = 对外游戏端口；18088 = 管理入口（容器内监听 0.0.0.0，宿主机务必映射到 127.0.0.1）
EXPOSE 28082 18088

ENTRYPOINT ["/entrypoint.sh"]
