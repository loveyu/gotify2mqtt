package mqtt

import (
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// BrokerOptions 是解析 DSN 后的完整 Broker 配置
type BrokerOptions struct {
	// PahoURL 是传给 paho 的 broker 地址（scheme 已转换）
	PahoURL string
	// 原始 scheme：mqtt | mqtts | ws | wss
	Scheme   string
	Username string
	Password string
	ClientID string

	QoS       byte
	Retain    bool
	QueueSize int

	ConnectTimeout    time.Duration
	KeepAlive         time.Duration
	ReconnectDelay    time.Duration
	ReconnectDelayMax time.Duration

	// TLS（mqtts / wss）
	CA       string
	Cert     string
	Key      string
	Insecure bool
}

// ParseDSN 解析 MQTT DSN 字符串为 BrokerOptions。
//
// 格式：scheme://[user:pass@]host[:port][/path][?params]
//
// 支持的 scheme：mqtt | mqtts | ws | wss
func ParseDSN(dsn string) (*BrokerOptions, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("DSN 格式错误: %w", err)
	}

	opts := &BrokerOptions{
		Scheme:            u.Scheme,
		QoS:               0,
		Retain:            true,
		QueueSize:         256,
		ConnectTimeout:    10 * time.Second,
		KeepAlive:         30 * time.Second,
		ReconnectDelay:    1 * time.Second,
		ReconnectDelayMax: 60 * time.Second,
	}

	// 认证信息
	if u.User != nil {
		opts.Username = u.User.Username()
		opts.Password, _ = u.User.Password()
	}

	// 构造 paho broker URL（paho 使用 tcp:// ssl:// ws:// wss://）
	switch u.Scheme {
	case "mqtt":
		opts.PahoURL = "tcp://" + u.Host
	case "mqtts":
		opts.PahoURL = "ssl://" + u.Host
	case "ws":
		opts.PahoURL = "ws://" + u.Host + u.Path
	case "wss":
		opts.PahoURL = "wss://" + u.Host + u.Path
	default:
		return nil, fmt.Errorf("不支持的 scheme %q，必须为 mqtt/mqtts/ws/wss", u.Scheme)
	}

	if u.Host == "" {
		return nil, fmt.Errorf("DSN 缺少 host")
	}

	q := u.Query()

	if v := q.Get("client_id"); v != "" {
		opts.ClientID = v
	}

	if v := q.Get("qos"); v != "" {
		n, err := strconv.ParseUint(v, 10, 8)
		if err != nil || n > 2 {
			return nil, fmt.Errorf("qos 必须为 0、1 或 2，当前: %s", v)
		}
		opts.QoS = byte(n)
	}

	if v := q.Get("retain"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("retain 必须为 true/false，当前: %s", v)
		}
		opts.Retain = b
	}

	if v := q.Get("queue_size"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("queue_size 必须为正整数，当前: %s", v)
		}
		opts.QueueSize = n
	}

	if v := q.Get("connect_timeout"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("connect_timeout 格式错误（示例 10s）: %w", err)
		}
		opts.ConnectTimeout = d
	}

	if v := q.Get("keep_alive"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("keep_alive 格式错误（示例 30s）: %w", err)
		}
		opts.KeepAlive = d
	}

	if v := q.Get("reconnect_delay"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("reconnect_delay 格式错误（示例 1s）: %w", err)
		}
		opts.ReconnectDelay = d
	}

	if v := q.Get("reconnect_delay_max"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("reconnect_delay_max 格式错误（示例 60s）: %w", err)
		}
		opts.ReconnectDelayMax = d
	}

	// TLS 参数（mqtts / wss 专用，其他 scheme 填了也不报错，但不生效）
	opts.CA = q.Get("ca")
	opts.Cert = q.Get("cert")
	opts.Key = q.Get("key")

	if v := q.Get("insecure"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("insecure 必须为 true/false，当前: %s", v)
		}
		opts.Insecure = b
	}

	// cert 和 key 必须同时提供
	if (opts.Cert == "") != (opts.Key == "") {
		return nil, fmt.Errorf("cert 和 key 必须同时提供")
	}

	return opts, nil
}
