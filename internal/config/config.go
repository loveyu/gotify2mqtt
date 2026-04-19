package config

import (
"fmt"
"net/url"
"os"

"gopkg.in/yaml.v3"
)

// Config 根配置结构
type Config struct {
PIDFile string  `yaml:"pid_file"`
Groups  []Group `yaml:"groups"`
}

// Group 对应一个 Gotify 实例及其转发目标列表
type Group struct {
Name    string   `yaml:"name"`
Gotify  Gotify   `yaml:"gotify"`
Targets []Target `yaml:"targets"`
}

// Gotify WebSocket 连接配置
type Gotify struct {
URL           string `yaml:"url"`             // http(s)://host:port
Token         string `yaml:"token"`           // 用户 Client Token
TLSSkipVerify bool   `yaml:"tls_skip_verify"` // 跳过 TLS 证书校验
}

// Target 一组过滤规则 + 目标 Broker 列表
type Target struct {
Name    string   `yaml:"name"`
Filter  Filter   `yaml:"filter"`
Brokers []Broker `yaml:"brokers"`
}

// Filter 消息过滤规则，所有条件 AND 关系；字段零值表示不过滤
type Filter struct {
// 仅转发这些 App 的消息（空表示全部）
AppIDs []uint `yaml:"app_ids"`
// 仅转发这些用户 ID 的消息（空表示全部）
UserIDs []uint `yaml:"user_ids"`
// 最低优先级（含）；0 表示不限
PriorityMin int `yaml:"priority_min"`
// 最高优先级（含）；nil 表示不限
PriorityMax *int `yaml:"priority_max"`
}

// Broker 单个 MQTT Broker 配置。
// 连接参数、认证、TLS、重连、超时均编码在 DSN 中；
// Topics 单独作为模板字符串数组。
type Broker struct {
// DSN 格式：scheme://[user:pass@]host[:port][/path][?params]
//
// 支持 scheme：mqtt | mqtts | ws | wss
//
// 查询参数：
//   client_id          MQTT 客户端 ID（留空自动生成）
//   qos                QoS 等级 0/1/2（默认 0）
//   retain             保留消息 true/false（默认 false）
//   queue_size         异步发布队列深度（默认 256）
//   connect_timeout    连接超时，如 10s（默认 10s）
//   keep_alive         心跳间隔，如 30s（默认 30s）
//   reconnect_delay    初始重连延迟，如 1s（默认 1s）
//   reconnect_delay_max 最大重连延迟，如 60s（默认 60s）
//   ca                 CA 证书文件路径（mqtts/wss）
//   cert               客户端证书路径（mTLS）
//   key                客户端私钥路径（mTLS）
//   insecure           跳过 TLS 验证 true/false（默认 false）
DSN string `yaml:"dsn"`

// Topics 是发布目标的 topic 模板列表，支持 Go template 变量：
//   {{.ID}} {{.AppID}} {{.UserID}} {{.UserName}} {{.Title}} {{.Priority}}
Topics []string `yaml:"topics"`
}

// Load 读取并校验 YAML 配置文件
func Load(path string) (*Config, error) {
data, err := os.ReadFile(path)
if err != nil {
return nil, fmt.Errorf("读取配置文件 %s: %w", path, err)
}
var cfg Config
if err := yaml.Unmarshal(data, &cfg); err != nil {
return nil, fmt.Errorf("解析 YAML: %w", err)
}
if err := cfg.validate(); err != nil {
return nil, fmt.Errorf("配置校验失败: %w", err)
}
cfg.applyDefaults()
return &cfg, nil
}

func (c *Config) applyDefaults() {
if c.PIDFile == "" {
c.PIDFile = "/tmp/gotify-mqtt-forwarder.pid"
}
}

func (c *Config) validate() error {
if len(c.Groups) == 0 {
return fmt.Errorf("至少需要配置一个 group")
}
for gi, g := range c.Groups {
if g.Name == "" {
return fmt.Errorf("group[%d].name 不能为空", gi)
}
if g.Gotify.URL == "" {
return fmt.Errorf("group[%d](%s).gotify.url 不能为空", gi, g.Name)
}
if _, err := url.Parse(g.Gotify.URL); err != nil {
return fmt.Errorf("group[%d](%s).gotify.url 格式错误: %w", gi, g.Name, err)
}
if g.Gotify.Token == "" {
return fmt.Errorf("group[%d](%s).gotify.token 不能为空", gi, g.Name)
}
if len(g.Targets) == 0 {
return fmt.Errorf("group[%d](%s) 至少需要一个 target", gi, g.Name)
}
for ti, t := range g.Targets {
if t.Name == "" {
return fmt.Errorf("group[%d](%s).targets[%d].name 不能为空", gi, g.Name, ti)
}
if len(t.Brokers) == 0 {
return fmt.Errorf("group[%d](%s).targets[%d](%s) 至少需要一个 broker",
gi, g.Name, ti, t.Name)
}
for bi, b := range t.Brokers {
loc := fmt.Sprintf("group[%d](%s).targets[%d](%s).brokers[%d]",
gi, g.Name, ti, t.Name, bi)
if err := validateBroker(loc, b); err != nil {
return err
}
}
}
}
return nil
}

func validateBroker(loc string, b Broker) error {
if b.DSN == "" {
return fmt.Errorf("%s.dsn 不能为空", loc)
}
u, err := url.Parse(b.DSN)
if err != nil {
return fmt.Errorf("%s.dsn 格式错误: %w", loc, err)
}
switch u.Scheme {
case "mqtt", "mqtts", "ws", "wss":
default:
return fmt.Errorf("%s.dsn scheme 必须为 mqtt/mqtts/ws/wss，当前: %q", loc, u.Scheme)
}
if u.Host == "" {
return fmt.Errorf("%s.dsn 缺少 host", loc)
}
if len(b.Topics) == 0 {
return fmt.Errorf("%s.topics 不能为空", loc)
}
return nil
}

// Matches 判断一条消息是否满足过滤条件
func (f *Filter) Matches(appID, userID uint, priority int) bool {
if len(f.AppIDs) > 0 {
found := false
for _, id := range f.AppIDs {
if id == appID {
found = true
break
}
}
if !found {
return false
}
}
if len(f.UserIDs) > 0 {
found := false
for _, id := range f.UserIDs {
if id == userID {
found = true
break
}
}
if !found {
return false
}
}
if priority < f.PriorityMin {
return false
}
if f.PriorityMax != nil && priority > *f.PriorityMax {
return false
}
return true
}
