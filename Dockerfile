# ── Build stage ──────────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

WORKDIR /src

# 依赖层单独缓存，源码变更时不重新下载
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /gotify2mqtt \
    ./

# ── Final stage ───────────────────────────────────────────────────────────────
# scratch 镜像：无 shell、无包管理器，攻击面最小
FROM scratch

# TLS 根证书（WSS / MQTTS 连接必需）
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

COPY --from=builder /gotify2mqtt /gotify2mqtt

ENTRYPOINT ["/gotify2mqtt"]
CMD ["-config", "/etc/gotify2mqtt/config.yaml"]
