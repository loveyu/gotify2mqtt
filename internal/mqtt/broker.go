package mqtt

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/url"
	"os"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"git.loveyu.info/microservice/gotify-mqtt-forwarder/internal/config"
	tmpl "git.loveyu.info/microservice/gotify-mqtt-forwarder/internal/template"
)

// Payload 是发布到 MQTT 的消息体
type Payload struct {
	ID       uint                   `json:"id"`
	AppID    uint                   `json:"app_id"`
	UserID   uint                   `json:"user_id"`
	Title    string                 `json:"title"`
	Message  string                 `json:"message"`
	Priority int                    `json:"priority"`
	Date     time.Time              `json:"date"`
	Extras   map[string]interface{} `json:"extras,omitempty"`
}

// PublishRequest 是投递到 Broker 发布队列的单条任务
type PublishRequest struct {
	Topics  []string // 已渲染好的 Topic 列表
	Payload []byte
	QoS     byte
	Retain  bool
}

// Broker 管理单个 MQTT Broker 的持久长连接和异步发布队列
type Broker struct {
	cfg    config.Broker
	client paho.Client
	queue  chan PublishRequest
	wg     sync.WaitGroup
	stopCh chan struct{}
}

// NewBroker 创建 Broker，不立即连接
func NewBroker(cfg config.Broker) *Broker {
	return &Broker{
		cfg:    cfg,
		queue:  make(chan PublishRequest, cfg.QueueSize),
		stopCh: make(chan struct{}),
	}
}

// Start 建立 MQTT 连接并启动异步发布 goroutine
func (b *Broker) Start() error {
	opts, err := b.buildOptions()
	if err != nil {
		return err
	}
	b.client = paho.NewClient(opts)

	if err := b.connectWithRetry(); err != nil {
		return err
	}

	b.wg.Add(1)
	go b.publishLoop()
	return nil
}

// Stop 等待队列处理完毕后优雅断开连接
func (b *Broker) Stop() {
	close(b.stopCh)
	b.wg.Wait()
	if b.client != nil && b.client.IsConnected() {
		b.client.Disconnect(500)
	}
}

// Publish 将一条 Payload 异步投入发布队列。
// topics 为已渲染好的 Topic 列表，data 用于渲染在 Broker 层无需再渲染（此处接收已渲染结果）。
func (b *Broker) Publish(topics []string, payload []byte) {
	select {
	case b.queue <- PublishRequest{
		Topics:  topics,
		Payload: payload,
		QoS:     b.cfg.QoS,
		Retain:  b.cfg.Retain,
	}:
	default:
		log.Printf("[mqtt][%s] 发布队列已满（深度 %d），丢弃消息", b.cfg.ClientID, b.cfg.QueueSize)
	}
}

// BuildPayload 将消息数据序列化为 JSON Payload
func BuildPayload(data tmpl.MessageData, msg string, extras map[string]interface{}) ([]byte, error) {
	p := Payload{
		ID:       data.ID,
		AppID:    data.AppID,
		UserID:   data.UserID,
		Title:    data.Title,
		Message:  msg,
		Priority: data.Priority,
		Date:     data.Date,
		Extras:   extras,
	}
	return json.Marshal(p)
}

// publishLoop 持续从队列读取并发布，直到 stopCh 关闭且队列排空
func (b *Broker) publishLoop() {
	defer b.wg.Done()
	for {
		select {
		case req, ok := <-b.queue:
			if !ok {
				return
			}
			b.doPublish(req)
		case <-b.stopCh:
			// 排空剩余消息
			for {
				select {
				case req := <-b.queue:
					b.doPublish(req)
				default:
					return
				}
			}
		}
	}
}

func (b *Broker) doPublish(req PublishRequest) {
	for _, topic := range req.Topics {
		if !b.client.IsConnected() {
			log.Printf("[mqtt][%s] 连接断开，跳过 topic=%s", b.cfg.ClientID, topic)
			continue
		}
		token := b.client.Publish(topic, req.QoS, req.Retain, req.Payload)
		if req.QoS > 0 {
			token.Wait()
		}
		if token.Error() != nil {
			log.Printf("[mqtt][%s] 发布失败 topic=%s: %v", b.cfg.ClientID, topic, token.Error())
		}
	}
}

func (b *Broker) connectWithRetry() error {
	backoff := time.Second
	const maxBackoff = 60 * time.Second
	for attempt := 1; ; attempt++ {
		token := b.client.Connect()
		token.Wait()
		if token.Error() == nil {
			log.Printf("[mqtt][%s] 连接成功 %s", b.cfg.ClientID, b.cfg.URL)
			return nil
		}
		if attempt >= 5 {
			return fmt.Errorf("broker %s 连接失败（已重试 %d 次）: %w", b.cfg.URL, attempt, token.Error())
		}
		log.Printf("[mqtt][%s] 连接失败（第 %d 次）: %v，%v 后重试", b.cfg.ClientID, attempt, token.Error(), backoff)
		time.Sleep(backoff)
		backoff = time.Duration(math.Min(float64(backoff*2), float64(maxBackoff)))
	}
}

func (b *Broker) buildOptions() (*paho.ClientOptions, error) {
	u, err := url.Parse(b.cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("解析 broker URL %s: %w", b.cfg.URL, err)
	}

	opts := paho.NewClientOptions()

	var brokerURL string
	switch u.Scheme {
	case "mqtt":
		brokerURL = fmt.Sprintf("tcp://%s", u.Host)
	case "mqtts":
		brokerURL = fmt.Sprintf("ssl://%s", u.Host)
		tlsCfg, err := b.buildTLSConfig()
		if err != nil {
			return nil, err
		}
		opts.SetTLSConfig(tlsCfg)
	default:
		return nil, fmt.Errorf("不支持的 broker scheme: %s", u.Scheme)
	}

	opts.AddBroker(brokerURL)
	opts.SetClientID(b.cfg.ClientID)

	if b.cfg.Username != "" {
		opts.SetUsername(b.cfg.Username)
		opts.SetPassword(b.cfg.Password)
	}

	opts.SetKeepAlive(30 * time.Second)
	opts.SetConnectTimeout(10 * time.Second)
	opts.SetAutoReconnect(true)
	opts.SetMaxReconnectInterval(60 * time.Second)
	opts.SetConnectRetryInterval(time.Second)
	opts.SetCleanSession(true)

	opts.SetOnConnectHandler(func(_ paho.Client) {
		log.Printf("[mqtt][%s] 已重新连接", b.cfg.ClientID)
	})
	opts.SetConnectionLostHandler(func(_ paho.Client, err error) {
		log.Printf("[mqtt][%s] 连接断开: %v，paho 自动重连中...", b.cfg.ClientID, err)
	})

	return opts, nil
}

func (b *Broker) buildTLSConfig() (*tls.Config, error) {
	tlsCfg := &tls.Config{
		InsecureSkipVerify: b.cfg.TLS.Insecure, //nolint:gosec // 用户明确配置
	}
	if b.cfg.TLS.CA != "" {
		caCert, err := os.ReadFile(b.cfg.TLS.CA)
		if err != nil {
			return nil, fmt.Errorf("读取 CA 证书 %s: %w", b.cfg.TLS.CA, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("解析 CA 证书失败: %s", b.cfg.TLS.CA)
		}
		tlsCfg.RootCAs = pool
	}
	if b.cfg.TLS.Cert != "" {
		cert, err := tls.LoadX509KeyPair(b.cfg.TLS.Cert, b.cfg.TLS.Key)
		if err != nil {
			return nil, fmt.Errorf("加载客户端证书: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	return tlsCfg, nil
}
