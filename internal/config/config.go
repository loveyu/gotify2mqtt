package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"

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
	URL           string `yaml:"url"`            // http(s)://host:port
	Token         string `yaml:"token"`          // 用户 Client Token
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
	// 仅转发这些用户 ID 的消息（空表示全部；UserID 为连接时解析的当前用户）
	UserIDs []uint `yaml:"user_ids"`
	// 最低优先级（含）；0 表示不限
	PriorityMin int `yaml:"priority_min"`
	// 最高优先级（含）；nil 表示不限
	PriorityMax *int `yaml:"priority_max"`
}

// Broker 单个 MQTT Broker 配置
type Broker struct {
	// MQTT 连接 URL，支持 mqtt:// 和 mqtts:// scheme
	URL      string `yaml:"url"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	// 留空则自动生成：{group}-{target}-{broker-host}
	ClientID string   `yaml:"client_id"`
	QoS      byte     `yaml:"qos"`
	Retain   bool     `yaml:"retain"`
	Topics   []string `yaml:"topics"`
	// 发布队列深度，默认 256
	QueueSize int       `yaml:"queue_size"`
	TLS       TLSConfig `yaml:"tls"`
}

// TLSConfig MQTTS 证书配置
type TLSConfig struct {
	CA       string `yaml:"ca"`       // CA 证书文件路径
	Cert     string `yaml:"cert"`     // 客户端证书（mTLS）
	Key      string `yaml:"key"`      // 客户端私钥（mTLS）
	Insecure bool   `yaml:"insecure"` // 跳过服务端证书校验（仅测试用）
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
	for gi := range c.Groups {
		g := &c.Groups[gi]
		for ti := range g.Targets {
			t := &g.Targets[ti]
			for bi := range t.Brokers {
				b := &t.Brokers[bi]
				if b.QueueSize <= 0 {
					b.QueueSize = 256
				}
				if b.ClientID == "" {
					u, _ := url.Parse(b.URL)
					host := ""
					if u != nil {
						host = strings.ReplaceAll(u.Hostname(), ".", "-")
					}
					b.ClientID = fmt.Sprintf("%s-%s-%s", g.Name, t.Name, host)
				}
			}
		}
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
				return fmt.Errorf("group[%d](%s).targets[%d](%s) 至少需要一个 broker", gi, g.Name, ti, t.Name)
			}
			for bi, b := range t.Brokers {
				loc := fmt.Sprintf("group[%d](%s).targets[%d](%s).brokers[%d]", gi, g.Name, ti, t.Name, bi)
				if b.URL == "" {
					return fmt.Errorf("%s.url 不能为空", loc)
				}
				u, err := url.Parse(b.URL)
				if err != nil {
					return fmt.Errorf("%s.url 格式错误: %w", loc, err)
				}
				if u.Scheme != "mqtt" && u.Scheme != "mqtts" {
					return fmt.Errorf("%s.url scheme 必须为 mqtt 或 mqtts，当前: %s", loc, u.Scheme)
				}
				if len(b.Topics) == 0 {
					return fmt.Errorf("%s.topics 不能为空", loc)
				}
				if b.QoS > 2 {
					return fmt.Errorf("%s.qos 必须为 0、1 或 2", loc)
				}
				if b.TLS.Cert != "" && b.TLS.Key == "" {
					return fmt.Errorf("%s: cert 和 key 必须同时提供", loc)
				}
				if b.TLS.Key != "" && b.TLS.Cert == "" {
					return fmt.Errorf("%s: cert 和 key 必须同时提供", loc)
				}
			}
		}
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
