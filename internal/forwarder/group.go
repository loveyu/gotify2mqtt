package forwarder

import (
"context"
"crypto/tls"
"encoding/json"
"fmt"
"io"
"log"
"math"
"net/http"
"net/url"
"strings"
"sync"
"time"

"github.com/gorilla/websocket"

"git.loveyu.info/microservice/gotify-mqtt-forwarder/internal/config"
"git.loveyu.info/microservice/gotify-mqtt-forwarder/internal/mqtt"
tmpl "git.loveyu.info/microservice/gotify-mqtt-forwarder/internal/template"
)

// gotifyMessage 对应 Gotify /stream 推送的 JSON 消息体
type gotifyMessage struct {
ID            uint                   `json:"id"`
ApplicationID uint                   `json:"appid"`
Message       string                 `json:"message"`
Title         string                 `json:"title"`
Priority      *int                   `json:"priority"`
Extras        map[string]interface{} `json:"extras,omitempty"`
Date          time.Time              `json:"date"`
}

// gotifyUser 对应 GET /current/user 返回体
type gotifyUser struct {
ID   uint   `json:"id"`
Name string `json:"name"`
}

// target 运行时绑定了 Broker 实例的 Target
type target struct {
cfg     config.Target
brokers []*mqtt.Broker
}

// Group 管理一个 Gotify 实例的 WebSocket 连接和所有转发目标
type Group struct {
cfg      config.Group
userID   uint
userName string
targets  []target
wg       sync.WaitGroup
}

// newGroup 初始化 Group，解析所有 DSN 并启动 Broker 连接
func newGroup(cfg config.Group) (*Group, error) {
g := &Group{cfg: cfg}

// 解析当前用户信息
user, err := g.fetchCurrentUser()
if err != nil {
return nil, fmt.Errorf("[%s] 获取用户信息失败: %w", cfg.Name, err)
}
g.userID = user.ID
g.userName = user.Name
log.Printf("[%s] 当前用户: id=%d name=%s", cfg.Name, user.ID, user.Name)

// 解析 DSN 并启动所有 Broker
for _, tc := range cfg.Targets {
t := target{cfg: tc}
for _, bc := range tc.Brokers {
opts, err := mqtt.ParseDSN(bc.DSN)
if err != nil {
return nil, fmt.Errorf("[%s][%s] DSN 解析失败: %w", cfg.Name, tc.Name, err)
}
// 自动生成 client_id：{group}-{target}-{host}
if opts.ClientID == "" {
u, _ := url.Parse(bc.DSN)
host := strings.ReplaceAll(u.Hostname(), ".", "-")
opts.ClientID = fmt.Sprintf("%s-%s-%s", cfg.Name, tc.Name, host)
}
b := mqtt.NewBroker(*opts, bc.Topics)
if err := b.Start(); err != nil {
log.Printf("[%s][%s] broker %s 启动失败: %v（将继续重连）",
cfg.Name, tc.Name, bc.DSN, err)
}
t.brokers = append(t.brokers, b)
}
g.targets = append(g.targets, t)
}

return g, nil
}

// Run 在 ctx 存活期间持续维护 WebSocket 连接，断线自动指数退避重连
func (g *Group) Run(ctx context.Context) {
defer g.wg.Done()

wsURL := g.buildWSURL()
backoff := time.Second
const maxBackoff = 60 * time.Second

for {
if err := g.connectAndReceive(ctx, wsURL); err != nil {
if ctx.Err() != nil {
log.Printf("[%s] WebSocket 退出", g.cfg.Name)
return
}
log.Printf("[%s] WebSocket 断开: %v，%v 后重连", g.cfg.Name, err, backoff)
select {
case <-time.After(backoff):
case <-ctx.Done():
return
}
backoff = time.Duration(math.Min(float64(backoff*2), float64(maxBackoff)))
} else {
backoff = time.Second
}
}
}

// Stop 关闭所有 Broker 连接
func (g *Group) Stop() {
for _, t := range g.targets {
for _, b := range t.brokers {
b.Stop()
}
}
}

func (g *Group) connectAndReceive(ctx context.Context, wsURL string) error {
dialer := websocket.Dialer{
HandshakeTimeout: 10 * time.Second,
TLSClientConfig:  g.buildTLSConfig(),
}
header := http.Header{"X-Gotify-Key": []string{g.cfg.Gotify.Token}}

conn, _, err := dialer.DialContext(ctx, wsURL, header)
if err != nil {
return fmt.Errorf("连接 WebSocket: %w", err)
}
defer conn.Close()
log.Printf("[%s] WebSocket 已连接: %s", g.cfg.Name, wsURL)

go func() {
<-ctx.Done()
conn.WriteMessage( //nolint:errcheck
websocket.CloseMessage,
websocket.FormatCloseMessage(websocket.CloseNormalClosure, "shutdown"))
conn.Close()
}()

for {
_, msgBytes, err := conn.ReadMessage()
if err != nil {
if ctx.Err() != nil {
return nil
}
return err
}
go g.handleMessage(msgBytes)
}
}

func (g *Group) handleMessage(raw []byte) {
var msg gotifyMessage
if err := json.Unmarshal(raw, &msg); err != nil {
log.Printf("[%s] 解析消息失败: %v", g.cfg.Name, err)
return
}

priority := 0
if msg.Priority != nil {
priority = *msg.Priority
}

data := tmpl.MessageData{
ID:       msg.ID,
AppID:    msg.ApplicationID,
UserID:   g.userID,
UserName: g.userName,
Title:    msg.Title,
Priority: priority,
Date:     msg.Date,
}

for ti := range g.targets {
t := &g.targets[ti]
if !t.cfg.Filter.Matches(data.AppID, data.UserID, data.Priority) {
continue
}
for _, b := range t.brokers {
b := b
go func() {
topics, err := tmpl.RenderTopics(b.Topics(), data)
if err != nil {
log.Printf("[%s][%s] 渲染 topic 失败: %v", g.cfg.Name, t.cfg.Name, err)
return
}
payload, err := mqtt.BuildPayload(data, msg.Message, msg.Extras)
if err != nil {
log.Printf("[%s][%s] 序列化 payload 失败: %v", g.cfg.Name, t.cfg.Name, err)
return
}
b.Publish(topics, payload)
}()
}
}
}

func (g *Group) buildWSURL() string {
base := strings.TrimRight(g.cfg.Gotify.URL, "/")
u, _ := url.Parse(base)
switch u.Scheme {
case "https":
u.Scheme = "wss"
default:
u.Scheme = "ws"
}
u.Path = strings.TrimRight(u.Path, "/") + "/stream"
return u.String()
}

func (g *Group) buildTLSConfig() *tls.Config {
return &tls.Config{
InsecureSkipVerify: g.cfg.Gotify.TLSSkipVerify, //nolint:gosec
}
}

func (g *Group) fetchCurrentUser() (*gotifyUser, error) {
apiURL := strings.TrimRight(g.cfg.Gotify.URL, "/") + "/current/user"
req, err := http.NewRequest(http.MethodGet, apiURL, nil)
if err != nil {
return nil, err
}
req.Header.Set("X-Gotify-Key", g.cfg.Gotify.Token)

httpClient := &http.Client{
Timeout: 10 * time.Second,
Transport: &http.Transport{
TLSClientConfig: g.buildTLSConfig(),
},
}
resp, err := httpClient.Do(req)
if err != nil {
return nil, err
}
defer resp.Body.Close()

if resp.StatusCode != http.StatusOK {
body, _ := io.ReadAll(resp.Body)
return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
}
var user gotifyUser
if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
return nil, err
}
return &user, nil
}
