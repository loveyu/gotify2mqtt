# Gotify MQTT Plugin 需求文档（v2）

## 1. 项目概述

### 1.1 项目名称
gotify-mqtt-plugin

### 1.2 项目目标
开发一个 Gotify 插件，通过 Webhook 方式接收消息，先将消息推送至 Gotify 获得消息 ID，再将完整消息转发到用户配置的 MQTT/MQTTS Broker。每个 Gotify 用户独立配置自己的 MQTT 服务器列表。

### 1.3 技术栈
- 语言：Go
- 插件框架：gotify/plugin-api v1
- MQTT 客户端库：eclipse/paho.mqtt.golang
- 构建方式：Go Plugin（`-buildmode=plugin`）

---

## 2. Gotify 插件接口选型

Gotify 插件系统基于 Go Plugin 机制，**每个用户创建独立的插件实例**。本项目启用以下接口：

| 接口 | 方法 | 用途 | 是否启用 |
|------|------|------|----------|
| `Plugin`（必须） | `Enable() error` / `Disable() error` | 生命周期：建立/断开 MQTT 长连接 | ✅ |
| `Messenger` | `SetMessageHandler(h MessageHandler)` | 向 Gotify 推送消息并获取 MessageID | ✅ |
| `Webhooker` | `RegisterWebhook(basePath, mux)` | 注册接收外部消息的 HTTP 端点 | ✅ |
| `Configurer` | `DefaultConfig()` / `ValidateAndSetConfig()` | 每用户独立的 MQTT 配置 UI | ✅ |
| `Storager` | `SetStorageHandler()` | 持久化用户配置 | ✅ |
| `Displayer` | `GetDisplay()` | 展示 Webhook URL 及使用说明 | ✅ |

> **关键设计原则**：使用 `Configurer` + `Storager` 替代全局环境变量，支持 Gotify 多租户场景——每个用户实例拥有完全隔离的 MQTT 配置。

---

## 3. 系统架构

### 3.1 总体架构图

```
┌─────────────────────────────────────────────────────────────────────┐
│                          Gotify Server                              │
│                                                                     │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │              gotify-mqtt-plugin 实例（每用户一个）           │   │
│  │                                                              │   │
│  │  ┌────────────┐   ┌────────────┐   ┌──────────────────────┐ │   │
│  │  │ Configurer │   │  Storager  │   │      Displayer       │ │   │
│  │  │  (配置UI)  │──▶│  (持久化)  │   │  (展示 Webhook URL)  │ │   │
│  │  └────────────┘   └─────┬──────┘   └──────────────────────┘ │   │
│  │                         │ 读取配置                           │   │
│  │                    ┌────▼──────┐                            │   │
│  │                    │  Plugin   │                            │   │
│  │                    │ (核心逻辑) │                            │   │
│  │                    └────┬──────┘                            │   │
│  │          ┌──────────────┼──────────────┐                   │   │
│  │          ▼              ▼              ▼                   │   │
│  │  ┌──────────────┐ ┌──────────┐ ┌────────────┐             │   │
│  │  │  Webhooker   │ │Messenger │ │MQTT Manager│             │   │
│  │  │ /message 端点 │ │(→Gotify) │ │  (长连接)  │             │   │
│  │  └──────┬───────┘ └────┬─────┘ └─────┬──────┘             │   │
│  │         │              │             │                     │   │
│  └─────────┼──────────────┼─────────────┼─────────────────────┘   │
│            │              │             │                          │
└────────────┼──────────────┼─────────────┼──────────────────────────┘
             │              │             │
             │              ▼             │
    外部服务  │      ┌───────────────┐    │
    HTTP POST │      │ Gotify 消息DB │    │
    ──────────┘      └───────────────┘    │
                                          ▼
                              ┌──────────────────────┐
                              │   MQTT/MQTTS Brokers  │
                              │  ┌────────┐ ┌──────┐  │
                              │  │Broker-1│ │Broker│  │
                              │  └────────┘ └──────┘  │
                              └──────────────────────┘
```

### 3.2 消息流时序图

```
外部服务          插件 Webhook          Gotify Messenger       MQTT Manager
   │                   │                      │                     │
   │  POST /message    │                      │                     │
   │──────────────────▶│                      │                     │
   │                   │                      │                     │
   │                   │ 1. 解析请求体         │                     │
   │                   │    (title/msg/prio)  │                     │
   │                   │                      │                     │
   │                   │ 2. SendMessage()     │                     │
   │                   │─────────────────────▶│                     │
   │                   │                      │ 写入 Gotify DB      │
   │                   │    MessageID 返回     │                     │
   │                   │◀─────────────────────│                     │
   │                   │                      │                     │
   │                   │ 3. 构建完整 Payload   │                     │
   │                   │    (含 MessageID)    │                     │
   │                   │                      │                     │
   │                   │ 4. 并发发布到各 Broker │                    │
   │                   │────────────────────────────────────────────▶│
   │                   │                      │            ┌─────────┤
   │                   │                      │            │ Broker-1│
   │                   │                      │            │ Topic-A │
   │                   │                      │            │ Topic-B │
   │                   │                      │            ├─────────┤
   │                   │                      │            │ Broker-2│
   │                   │                      │            │ Topic-C │
   │                   │                      │            └─────────┘
   │  HTTP 200         │                      │                     │
   │◀──────────────────│                      │                     │
   │                   │                      │                     │
```

> **关键顺序保证**：先 `SendMessage` 获取 MessageID，再异步并发发布 MQTT，最后同步返回 HTTP 200。

### 3.3 MQTT 连接生命周期

```
插件 Enable()                    插件 Disable()
     │                                │
     ▼                                ▼
┌─────────────────────────────────────────────┐
│  MQTT Manager                               │
│                                             │
│  读取 Storager 中用户配置                    │
│       │                                     │
│       ├──▶ Broker-1 建立长连接              │
│       │      └── 自动重连（指数退避）         │
│       ├──▶ Broker-2 建立长连接              │
│       │      └── 自动重连（指数退避）         │
│       └──▶ Broker-N ...                    │
│                                             │
│  消息到达时：复用已有连接 Publish()           │
│                                             │
│  Disable() → 优雅断开所有连接 Disconnect()  │
└─────────────────────────────────────────────┘
```

---

## 4. 插件配置设计（Configurer + Storager）

### 4.1 配置结构

用户通过 Gotify UI 在插件配置页中填写，配置结构如下：

```go
type Config struct {
    // MQTT 服务器列表，每行一个 DSN
    Servers []ServerConfig `yaml:"servers"`
}

type ServerConfig struct {
    // DSN 格式：scheme://[user:pass@]host[:port]?params
    DSN string `yaml:"dsn"`
}
```

Gotify UI 中每个 ServerConfig 对应一行可增删的输入框。

### 4.2 DSN 格式定义

```
scheme://[username[:password]@]host[:port][?parameters]
```

| 字段 | 说明 | 示例 |
|------|------|------|
| `scheme` | `mqtt`（TCP）或 `mqtts`（TLS） | `mqtt` |
| `username` | MQTT 认证用户名（可选） | `admin` |
| `password` | MQTT 认证密码（可选） | `secret` |
| `host` | Broker 地址 | `broker.example.com` |
| `port` | 端口（默认 mqtt:1883 / mqtts:8883） | `1883` |

### 4.3 DSN 查询参数

| 参数 | 必填 | 默认值 | 说明 |
|------|------|--------|------|
| `topics` | ✅ | — | Topic 列表（逗号分隔），支持模板变量 |
| `client_id` | ❌ | `gotify-{userID}-{brokerHost}` | MQTT Client ID，需全局唯一 |
| `qos` | ❌ | `0` | QoS 等级（0 / 1 / 2） |
| `retain` | ❌ | `false` | 是否设置 Retain 标志 |
| `ca` | ❌ | — | MQTTS CA 证书文件路径 |
| `cert` | ❌ | — | mTLS 客户端证书路径 |
| `key` | ❌ | — | mTLS 客户端私钥路径 |
| `insecure` | ❌ | `false` | 跳过 TLS 证书校验（仅测试用） |

### 4.4 DSN 示例

```
# 最简配置
mqtt://broker.example.com?topics=gotify/messages

# 带认证 + 多 Topic
mqtt://admin:pass@broker.example.com:1883?topics=alerts/critical,alerts/info&qos=1

# MQTTS + 模板变量
mqtts://user:pass@broker.example.com:8883?topics=gotify/{{.UserID}}/{{.Title}}&ca=/etc/ssl/ca.crt&qos=1&retain=true

# mTLS 双向认证
mqtts://broker.example.com?topics=gotify/alerts&ca=/ca.crt&cert=/client.crt&key=/client.key

# 多服务器（每行一条 DSN，在 UI 中分别添加）
mqtt://broker1:1883?topics=topic1
mqtts://broker2:8883?topics=topic2&ca=/certs/ca.crt
```

### 4.5 配置校验（ValidateAndSetConfig）

| 校验项 | 规则 |
|--------|------|
| DSN 格式 | scheme 必须为 `mqtt` 或 `mqtts` |
| topics | 不能为空 |
| qos | 必须为 0、1 或 2 |
| mqtts + ca | 若 scheme 为 mqtts 且 insecure=false，ca 文件必须存在 |
| cert/key | 必须同时提供或同时不提供 |

---

## 5. 模板变量系统

### 5.1 可用模板变量

MQTT Topic 支持 Go template 语法，**所有变量均在消息处理时确定**：

| 变量 | 类型 | 来源 | 示例输出 |
|------|------|------|----------|
| `{{.MessageID}}` | uint | Gotify `SendMessage` 返回值 | `12345` |
| `{{.UserID}}` | uint | `plugin.UserContext.ID` | `1` |
| `{{.UserName}}` | string | `plugin.UserContext.Name` | `admin` |
| `{{.Title}}` | string | Webhook 请求体（安全处理后） | `Server_Alert` |
| `{{.Priority}}` | int | Webhook 请求体 | `5` |

> ⚠️ **移除 `AppID` / `AppName`**：插件 Webhook 端点由外部服务直接调用，Gotify 框架不会向请求体注入应用标识，无法可靠获取，故从模板变量中删除，避免误用。

### 5.2 Topic 安全处理规则

| 字符 | 处理方式 |
|------|----------|
| 空格 | 替换为 `_` |
| `+` / `#` | 移除（MQTT 通配符，禁止出现在发布 Topic 中） |
| `/` | 保留（Topic 层级分隔符） |
| 其余 ASCII 控制字符 | 移除 |

### 5.3 模板执行时序

```
收到 Webhook 请求
       │
       ▼
SendMessage(title, message, priority)
       │
       └──▶ 返回 MessageID
                  │
                  ▼
       构建 TemplateData{
           MessageID: <返回值>,
           UserID:    ctx.ID,
           UserName:  ctx.Name,
           Title:     sanitize(title),
           Priority:  priority,
       }
                  │
                  ▼
       渲染各 Broker 的 Topic 模板
                  │
                  ▼
       并发 Publish 到各 Broker
```

---

## 6. Webhook 接口设计

### 6.1 端点

插件注册路径：`{basePath}/message`（由 Gotify 框架分配 basePath）

Gotify 框架对该端点**自动要求用户级 Token 鉴权**，外部服务需在请求头携带：
```
X-Gotify-Key: <user-token>
```
或通过 Basic Auth 传递。

### 6.2 请求格式

```http
POST {basePath}/message
Content-Type: application/json
X-Gotify-Key: <user-token>
```

请求体：

```json
{
  "title": "Server Alert",
  "message": "CPU usage over 90%",
  "priority": 5,
  "extras": {
    "client::display": "text"
  }
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `title` | string | ❌ | 消息标题，默认空字符串 |
| `message` | string | ✅ | 消息正文 |
| `priority` | int | ❌ | 优先级（0-10），默认 0 |
| `extras` | object | ❌ | Gotify 扩展字段，原样透传 |

### 6.3 处理流程

```
1. 解析 JSON 请求体
2. 调用 MessageHandler.SendMessage → 写入 Gotify，返回 MessageID
3. 构建 TemplateData（含 MessageID）
4. 并发向所有 Broker 的所有 Topic 发布消息
   - 每个 Broker 复用长连接
   - 单个 Broker/Topic 失败：记录错误日志，不阻塞其他 Broker
5. 返回 HTTP 202 Accepted（消息已接收并推送至 Gotify）
```

### 6.4 响应格式

**成功（202）**：
```json
{ "id": 12345 }
```

**请求体错误（400）**：
```json
{ "error": "missing required field: message" }
```

**插件未启用（503）**：
```json
{ "error": "plugin not enabled" }
```

### 6.5 错误处理策略

| 场景 | 行为 |
|------|------|
| `SendMessage` 失败 | 返回 502，不发送 MQTT |
| MQTT Broker 连接断开 | 后台自动重连，记录警告日志 |
| 单个 Topic 发布失败 | 记录错误日志，继续其他 Topic |
| DSN 解析失败 | `ValidateAndSetConfig` 阶段拒绝保存，不影响运行 |
| 插件 Disabled 状态收到请求 | 返回 503 |

---

## 7. MQTT 消息载荷

### 7.1 格式（JSON）

```json
{
  "id": 12345,
  "title": "Server Alert",
  "message": "CPU usage over 90%",
  "priority": 5,
  "date": "2024-01-15T10:30:00Z",
  "extras": {
    "client::display": "text"
  }
}
```

| 字段 | 说明 |
|------|------|
| `id` | Gotify MessageID（由 `SendMessage` 返回） |
| `title` | 原始标题（未做 Topic 安全处理） |
| `message` | 消息正文 |
| `priority` | 优先级 |
| `date` | 消息创建时间（RFC3339 格式，UTC） |
| `extras` | Webhook 请求体中的 extras，原样透传 |

---

## 8. MQTT 连接管理

### 8.1 连接建立（Enable）

```
Enable() 调用时：
  1. 从 Storager 读取用户配置
  2. 若配置为空，跳过（插件以仅 Gotify 推送模式运行）
  3. 为每个 DSN 建立持久 MQTT 连接
     - CleanSession = true
     - KeepAlive = 30s
     - ConnectTimeout = 10s
     - 自动重连：初始 1s，最大 60s，指数退避
  4. 连接失败记录日志，不阻止 Enable 成功
```

### 8.2 连接关闭（Disable）

```
Disable() 调用时：
  1. 停止接受新的 Webhook 请求（原子标志位）
  2. 等待当前进行中的 Publish 完成（WaitGroup）
  3. 调用 client.Disconnect(250ms) 优雅关闭
```

### 8.3 重连策略

| 参数 | 值 |
|------|----|
| 初始重连间隔 | 1 秒 |
| 最大重连间隔 | 60 秒 |
| 退避策略 | 指数退避（×2）+ 随机抖动 |
| 重连次数限制 | 无限制 |

---

## 9. 插件信息

```go
plugin.Info{
    ModulePath:  "git.loveyu.info/microservice/gotify-mqtt-plugin",
    Version:     "1.0.0",
    Author:      "loveyu",
    Name:        "gotify-mqtt-plugin",
    Website:     "https://git.loveyu.info/microservice/gotify-mqtt-plugin",
    Description: "Forward Gotify messages to MQTT/MQTTS brokers",
    License:     "MIT",
}
```

---

## 10. 项目结构

```
gotify-mqtt-plugin/
├── .gitlab-ci.yml          # GitLab CI/CD 配置
├── .gitignore
├── Makefile                # 构建脚本（支持多架构）
├── go.mod
├── go.sum
├── plugin.go               # 插件入口：实现所有接口、生命周期管理
├── plugin_test.go          # 接口兼容性 + 集成流程测试
├── config.go               # DSN 解析、Config 结构体、ValidateAndSetConfig
├── config_test.go          # 配置解析与校验测试
├── mqtt.go                 # MQTT Manager：连接池、长连接、Publish
├── mqtt_test.go            # MQTT 发布逻辑单元测试
├── template.go             # Topic 模板渲染与 Topic 安全处理
├── template_test.go        # 模板变量渲染测试
└── README.md               # 用户使用说明（含 Webhook URL 格式）
```

---

## 11. 构建系统

### 11.1 本地构建

```bash
make download-tools     # 下载 gomod-cap 等工具

make build              # 构建所有架构
make build-linux-amd64
make build-linux-arm64
make build-linux-arm-7
```

输出：`build/gotify-mqtt-plugin-linux-{arch}.so`

### 11.2 支持架构

| 架构 | Docker 构建镜像 | 输出文件 |
|------|----------------|----------|
| linux/amd64 | `gotify/build:{ver}-linux-amd64` | `gotify-mqtt-plugin-linux-amd64.so` |
| linux/arm64 | `gotify/build:{ver}-linux-arm64` | `gotify-mqtt-plugin-linux-arm64.so` |
| linux/arm/v7 | `gotify/build:{ver}-linux-arm-7` | `gotify-mqtt-plugin-linux-arm-7.so` |

### 11.3 构建依赖

- Docker
- Go（与目标 Gotify Server 版本严格一致）
- `gomod-cap`（确保 `go.mod` 中依赖版本 ≤ Gotify Server 使用版本）

---

## 12. GitLab CI/CD

### 12.1 Pipeline 阶段

```
test ──▶ build ──▶ release（仅 tag 触发）
```

### 12.2 各阶段详情

| 阶段 | 内容 |
|------|------|
| **test** | `go vet ./...` + `go test ./...` + 接口兼容性验证 |
| **build** | `make download-tools` → `gomod-cap` 对齐依赖 → 三架构并行构建 → 保存 Artifact |
| **release** | 打 tag 触发，将三个 `.so` 附加至 GitLab Release |

---

## 13. 使用示例

### 13.1 用户配置流程（Gotify UI）

1. 进入 Gotify → 插件管理 → gotify-mqtt-plugin → 配置
2. 添加 MQTT 服务器 DSN，例如：
   ```
   mqtt://broker.example.com:1883?topics=gotify/{{.UserID}}/{{.Title}}&qos=1
   ```
3. 保存配置（触发 ValidateAndSetConfig 校验）
4. 启用插件（触发 Enable，建立 MQTT 长连接）
5. 在插件详情页（Displayer）查看 Webhook URL

### 13.2 外部服务调用

```bash
curl -X POST "https://gotify.example.com/plugin/{plugin-id}/custom/message" \
  -H "Content-Type: application/json" \
  -H "X-Gotify-Key: <user-token>" \
  -d '{
    "title": "Alert",
    "message": "High CPU usage detected",
    "priority": 5
  }'
```

### 13.3 Displayer 展示内容

插件详情页自动展示：
- 当前 Webhook 完整 URL
- 各 Broker 连接状态（已连接 / 重连中 / 未配置）
- 请求示例（curl）

---

## 14. 与原方案的主要变更

| 项目 | 原方案 | 新方案 |
|------|--------|--------|
| 配置方式 | 全局环境变量 | `Configurer` + `Storager`（每用户独立） |
| 多租户支持 | ❌ 所有用户共享配置 | ✅ 每用户独立 MQTT 配置 |
| `AppID`/`AppName` 模板变量 | ❌ 来源错误，无法可靠获取 | 已移除 |
| MessageID 获取 | ❌ 与并发发布逻辑矛盾 | ✅ 先发 Gotify 获取 ID，再发 MQTT |
| MQTT 连接方式 | ❌ 未定义（隐含每次新建） | ✅ 长连接 + 指数退避自动重连 |
| Disable 优雅关闭 | ❌ 未定义 | ✅ WaitGroup 等待 + Disconnect |
| HTTP 响应码 | 200 | 202（已接受），含错误码区分 |
