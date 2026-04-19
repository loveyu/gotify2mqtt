package gotify

import (
"fmt"
"net/url"
"strconv"
"time"
)

// Options 是解析 Gotify DSN 后的完整连接配置
type Options struct {
// BaseURL 是 HTTP/HTTPS 基础地址（无 /stream 后缀）
BaseURL string
// WSURL 是对应的 WebSocket 地址（/stream 后缀）
WSURL string
// Token 即 Gotify Client Token（DSN 中的 userinfo username）
Token string

ConnectTimeout    time.Duration
ReconnectDelay    time.Duration
ReconnectDelayMax time.Duration

Insecure bool
}

// ParseDSN 解析 Gotify DSN 字符串为 Options。
//
// 格式：scheme://token@host[:port][/basepath][?params]
//
// 支持的 scheme：http | https
//
// 查询参数：
//
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
case "http", "https":
default:
return nil, fmt.Errorf("Gotify DSN scheme 必须为 http 或 https，当前: %q", u.Scheme)
}

if u.Host == "" {
return nil, fmt.Errorf("Gotify DSN 缺少 host")
}

token := ""
if u.User != nil {
token = u.User.Username()
}
if token == "" {
return nil, fmt.Errorf("Gotify DSN 缺少 token（格式：scheme://token@host）")
}

// 构建无认证信息的 baseURL
clean := *u
clean.User = nil
clean.RawQuery = ""
clean.Fragment = ""
baseURL := clean.String()

// 构建 WebSocket URL
wsURL := clean
switch u.Scheme {
case "https":
wsURL.Scheme = "wss"
default:
wsURL.Scheme = "ws"
}
wsURL.Path = trimRight(wsURL.Path, "/") + "/stream"

opts := &Options{
BaseURL:           baseURL,
WSURL:             wsURL.String(),
Token:             token,
ConnectTimeout:    10 * time.Second,
ReconnectDelay:    1 * time.Second,
ReconnectDelayMax: 60 * time.Second,
}

q := u.Query()

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

func trimRight(s, cut string) string {
for len(s) > 0 && len(cut) > 0 && s[len(s)-1] == cut[0] {
s = s[:len(s)-1]
}
return s
}
