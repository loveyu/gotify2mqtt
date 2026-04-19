package mqtt

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	tmpl "github.com/loveyu/gotify2mqtt/internal/template"
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
	Topics  []string
	Payload []byte
	QoS     byte
	Retain  bool
}

// Broker 管理单个 MQTT Broker 的持久长连接和异步发布队列
type Broker struct {
	opts   BrokerOptions
	topics []string
	client paho.Client
	queue  chan PublishRequest
	wg     sync.WaitGroup
	stopCh chan struct{}
}

// NewBroker 创建 Broker（通过已解析的 DSN 选项），不立即连接
func NewBroker(opts BrokerOptions, topics []string) *Broker {
	return &Broker{
		opts:   opts,
		topics: topics,
		queue:  make(chan PublishRequest, opts.QueueSize),
		stopCh: make(chan struct{}),
	}
}

// Topics 返回该 Broker 配置的原始 topic 模板列表
func (b *Broker) Topics() []string { return b.topics }

// Start 建立 MQTT 连接并启动异步发布 goroutine
func (b *Broker) Start() error {
	pahoOpts, err := b.buildPahoOptions()
	if err != nil {
		return err
	}
	b.client = paho.NewClient(pahoOpts)

	if err := b.connectWithRetry(); err != nil {
		return err
	}

	b.wg.Add(1)
	go b.publishLoop()
	return nil
}

// Stop 等待队列排空后优雅断开连接
func (b *Broker) Stop() {
	close(b.stopCh)
	b.wg.Wait()
	if b.client != nil && b.client.IsConnected() {
		b.client.Disconnect(500)
	}
}

// Publish 将渲染好的 topics + payload 异步投入发布队列
func (b *Broker) Publish(topics []string, payload []byte) {
	select {
	case b.queue <- PublishRequest{
		Topics:  topics,
		Payload: payload,
		QoS:     b.opts.QoS,
		Retain:  b.opts.Retain,
	}:
	default:
		log.Printf("[mqtt][%s] 发布队列已满（容量 %d），丢弃消息",
			b.opts.ClientID, b.opts.QueueSize)
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
			log.Printf("[mqtt][%s] 连接断开，跳过 topic=%s", b.opts.ClientID, topic)
			continue
		}
		token := b.client.Publish(topic, req.QoS, req.Retain, req.Payload)
		if req.QoS > 0 {
			token.Wait()
		}
		if token.Error() != nil {
			log.Printf("[mqtt][%s] 发布失败 topic=%s: %v",
				b.opts.ClientID, topic, token.Error())
		}
	}
}

func (b *Broker) connectWithRetry() error {
	backoff := b.opts.ReconnectDelay
	for attempt := 1; ; attempt++ {
		token := b.client.Connect()
		token.Wait()
		if token.Error() == nil {
			log.Printf("[mqtt][%s] 连接成功 %s", b.opts.ClientID, b.opts.PahoURL)
			return nil
		}
		if attempt >= 5 {
			return fmt.Errorf("broker %s 连接失败（已重试 %d 次）: %w",
				b.opts.PahoURL, attempt, token.Error())
		}
		log.Printf("[mqtt][%s] 连接失败（第 %d 次）: %v，%v 后重试",
			b.opts.ClientID, attempt, token.Error(), backoff)
		time.Sleep(backoff)
		backoff = time.Duration(
			math.Min(float64(backoff*2), float64(b.opts.ReconnectDelayMax)))
	}
}

func (b *Broker) buildPahoOptions() (*paho.ClientOptions, error) {
	opts := paho.NewClientOptions()
	opts.AddBroker(b.opts.PahoURL)
	opts.SetClientID(b.opts.ClientID)

	if b.opts.Username != "" {
		opts.SetUsername(b.opts.Username)
		opts.SetPassword(b.opts.Password)
	}

	opts.SetKeepAlive(b.opts.KeepAlive)
	opts.SetConnectTimeout(b.opts.ConnectTimeout)
	opts.SetAutoReconnect(true)
	opts.SetMaxReconnectInterval(b.opts.ReconnectDelayMax)
	opts.SetConnectRetryInterval(b.opts.ReconnectDelay)
	opts.SetCleanSession(true)

	if b.opts.Scheme == "mqtts" || b.opts.Scheme == "wss" {
		tlsCfg, err := b.buildTLSConfig()
		if err != nil {
			return nil, err
		}
		opts.SetTLSConfig(tlsCfg)
	}

	opts.SetOnConnectHandler(func(_ paho.Client) {
		log.Printf("[mqtt][%s] 已连接/重连", b.opts.ClientID)
	})
	opts.SetConnectionLostHandler(func(_ paho.Client, err error) {
		log.Printf("[mqtt][%s] 连接断开: %v，paho 自动重连中...", b.opts.ClientID, err)
	})

	return opts, nil
}

func (b *Broker) buildTLSConfig() (*tls.Config, error) {
	tlsCfg := &tls.Config{
		InsecureSkipVerify: b.opts.Insecure, //nolint:gosec
	}
	if b.opts.CA != "" {
		caCert, err := os.ReadFile(b.opts.CA)
		if err != nil {
			return nil, fmt.Errorf("读取 CA 证书 %s: %w", b.opts.CA, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("解析 CA 证书失败: %s", b.opts.CA)
		}
		tlsCfg.RootCAs = pool
	}
	if b.opts.Cert != "" {
		cert, err := tls.LoadX509KeyPair(b.opts.Cert, b.opts.Key)
		if err != nil {
			return nil, fmt.Errorf("加载客户端证书: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	return tlsCfg, nil
}
