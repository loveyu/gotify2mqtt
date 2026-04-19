package gotify

import (
"fmt"
"net/url"
"strconv"
"strings"
"time"
)

// Options 是解析 Gotify DSN 后的完整连接配置
type Options struct {
// BaseURL 是对应的 HTTP/HTTPS 地址（用于 REST API 调用，不含认证信息）
BaseURL string
// WSURL 是 WebSocket 连接地址（含 /stream 后缀，不含认证信息）
WSURL string

// Token 是 Gotify Client Token（DSN 查询参数 token=）
Token string
// Username / Password 用于反向代理等中间层的 Basic Auth（可选）
Username string
Password string

ConnectTimeout    time.Duration
ReconnectDelay    time.Duration
ReconnectDelayMax time.Duration

Insecure bool
}

// ParseDSN 解析 Gotify DSN 字符串为 Options。
//
// 格式：scheme://[user:pass@]host[:port][/basepath]?token=xxx[&params]
//
// 支持的 scheme：ws | wss
//
// 查询参数：
//
//token               Gotify Client Token（必填）
//insecure            跳过 TLS 证书校验 true/false（默认 false）
//connect_timeout     连接超时，如 10s（默认 10s）
//reconnect_delay     初始重连延迟，如 1s（默认 1s）
//reconnect_delay_max 最大重连延迟，如 60s（默认 60s）
func ParseDSN(dsn string) (*Options, error) {
u, err := url.Parse(dsn)
if err != nil {
return nil, fmt.Errorf("Gotify DSN 格式错误: %w", err)
}

switch u.Scheme {
case "ws", "wss":
default:
return nil, fmt.Errorf("Gotify DSN scheme 必须为 ws 或 wss，当前: %q", u.Scheme)
}

if u.Host == "" {
return nil, fmt.Errorf("Gotify DSN 缺少 host")
}

q := u.Query()

token := q.Get("token")
if token == "" {
return nil, fmt.Errorf("Gotify DSN 缺少必填参数 token（示例：?token=your-client-token）")
}

basePath := strings.TrimRight(u.Path, "/")

// WebSocket URL（不含认证信息和查询参数）
wsURL := &url.URL{
Scheme: u.Scheme,
Host:   u.Host,
Path:   basePath + "/stream",
}

// HTTP BaseURL（ws→http, wss→https，用于 REST API 调用）
httpScheme := "http"
if u.Scheme == "wss" {
httpScheme = "https"
}
baseURL := &url.URL{
Scheme: httpScheme,
Host:   u.Host,
Path:   basePath,
}

opts := &Options{
BaseURL:           baseURL.String(),
WSURL:             wsURL.String(),
Token:             token,
ConnectTimeout:    10 * time.Second,
ReconnectDelay:    1 * time.Second,
ReconnectDelayMax: 60 * time.Second,
}

// Basic Auth（可选，用于反向代理）
if u.User != nil {
opts.Username = u.User.Username()
opts.Password, _ = u.User.Password()
}

if v := q.Get("insecure"); v != "" {
b, err := strconv.ParseBool(v)
if err != nil {
return nil, fmt.Errorf("Gotify DSN insecure 必须为 true/false，当前: %s", v)
}
opts.Insecure = b
}

if v := q.Get("connect_timeout"); v != "" {
d, err := time.ParseDuration(v)
if err != nil {
return nil, fmt.Errorf("Gotify DSN connect_timeout 格式错误（示例 10s）: %w", err)
}
opts.ConnectTimeout = d
}

if v := q.Get("reconnect_delay"); v != "" {
d, err := time.ParseDuration(v)
if err != nil {
return nil, fmt.Errorf("Gotify DSN reconnect_delay 格式错误（示例 1s）: %w", err)
}
opts.ReconnectDelay = d
}

if v := q.Get("reconnect_delay_max"); v != "" {
d, err := time.ParseDuration(v)
if err != nil {
return nil, fmt.Errorf("Gotify DSN reconnect_delay_max 格式错误（示例 60s）: %w", err)
}
opts.ReconnectDelayMax = d
}

return opts, nil
}
