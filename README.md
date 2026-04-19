# gotify2mqtt

[![CI / Release](https://github.com/loveyu/gotify2mqtt/actions/workflows/ci.yml/badge.svg)](https://github.com/loveyu/gotify2mqtt/actions/workflows/ci.yml)
[![GitHub release](https://img.shields.io/github/v/release/loveyu/gotify2mqtt)](https://github.com/loveyu/gotify2mqtt/releases)
[![Docker Image](https://ghcr-badge.egpl.dev/loveyu/gotify2mqtt/size)](https://github.com/loveyu/gotify2mqtt/pkgs/container/gotify2mqtt)

A standalone service that subscribes to one or more [Gotify](https://gotify.net/) servers via WebSocket and forwards messages to MQTT brokers in real time.

## Features

- **Multi-instance** — monitor multiple Gotify servers simultaneously
- **Multi-target routing** — one Gotify stream → multiple targets, each with independent filter rules
- **Multi-broker fan-out** — each target can publish to multiple MQTT brokers in parallel
- **DSN-based config** — all connection parameters (Gotify + MQTT) expressed as a single DSN string
- **Flexible filtering** — filter by App ID, User ID, and message priority range
- **Go-template topics** — dynamically route to different MQTT topics based on message metadata
- **Async publish queues** — non-blocking per-broker queues with configurable depth
- **Auto-reconnect** — exponential backoff for both WebSocket and MQTT connections
- **Single-process guarantee** — PID file with `flock` (Unix) or PID-check (Windows)
- **Graceful shutdown** — handles `SIGTERM` / `SIGINT`, drains queues before exit

## Architecture

```
Gotify Server A ──ws──► Group A ──► Target 1 (filter) ──► Broker 1 (mqtt://)
                                 └──► Target 2 (filter) ──► Broker 2 (mqtts://)
                                                         └──► Broker 3 (wss://)

Gotify Server B ──wss──► Group B ──► Target 1 (filter) ──► Broker 4 (mqtt://)
```

## Installation

### Docker (recommended)

```bash
docker run -d \
  --name gotify2mqtt \
  --restart unless-stopped \
  -v /path/to/config.yaml:/etc/gotify2mqtt/config.yaml \
  ghcr.io/loveyu/gotify2mqtt:latest
```

### Docker Compose

```yaml
services:
  gotify2mqtt:
    image: ghcr.io/loveyu/gotify2mqtt:latest
    restart: unless-stopped
    volumes:
      - ./config.yaml:/etc/gotify2mqtt/config.yaml
```

### Binary

Download the archive for your platform from the [Releases](https://github.com/loveyu/gotify2mqtt/releases) page.

```
gotify2mqtt_{version}_{os}_{arch}.tar.gz   # Linux / macOS
gotify2mqtt_{version}_{os}_{arch}.zip      # Windows
```

```bash
tar -xzf gotify2mqtt_*.tar.gz
./gotify2mqtt -config config.yaml
```

### Build from source

```bash
git clone https://github.com/loveyu/gotify2mqtt.git
cd gotify2mqtt
go build -o gotify2mqtt ./
./gotify2mqtt -config config.yaml
```

## Configuration

Copy `config.example.yaml` and edit it:

```bash
cp config.example.yaml config.yaml
```

### Minimal example

```yaml
groups:
  - name: my-gotify
    gotify: "ws://gotify.example.com:8080?token=YOUR_CLIENT_TOKEN"
    targets:
      - name: all
        filter: {}
        brokers:
          - dsn: "mqtt://localhost:1883"
            topics:
              - "gotify/{{.AppID}}/messages"
```

### Full structure

```yaml
pid_file: /var/run/gotify2mqtt.pid   # optional, default: /tmp/gotify-mqtt-forwarder.pid

groups:
  - name: <string>                   # unique name for this Gotify instance
    gotify: <dsn>                    # Gotify WebSocket DSN (see below)
    targets:
      - name: <string>
        filter:
          app_ids:      [1, 2]       # forward only these App IDs (empty = all)
          user_ids:     [1]          # forward only these User IDs (empty = all)
          priority_min: 0            # minimum priority (inclusive)
          priority_max: 10           # maximum priority (inclusive, optional)
        brokers:
          - dsn: <dsn>               # MQTT broker DSN (see below)
            topics:
              - "your/topic/{{.AppID}}"
```

---

## DSN Reference

### Gotify DSN

```
ws://[user:pass@]host[:port][/basepath]?token=xxx[&params]
wss://[user:pass@]host[:port][/basepath]?token=xxx[&params]
```

| Scheme | Description |
|--------|-------------|
| `ws`   | Plain WebSocket (no TLS) |
| `wss`  | WebSocket over TLS |

`user:pass` in userinfo is **optional** — only needed when a reverse proxy requires HTTP Basic Auth. Special characters must be percent-encoded (`@` → `%40`, `:` → `%3A`).

| Parameter | Default | Description |
|-----------|---------|-------------|
| `token` | **required** | Gotify Client Token |
| `insecure` | `false` | Skip TLS certificate verification |
| `connect_timeout` | `10s` | WebSocket handshake timeout |
| `reconnect_delay` | `1s` | Initial reconnect delay (exponential backoff start) |
| `reconnect_delay_max` | `60s` | Maximum reconnect delay |

**Examples:**
```yaml
# Plain WebSocket
gotify: "ws://gotify.example.com:8080?token=abc123"

# TLS with self-signed certificate
gotify: "wss://gotify.example.com?token=abc123&insecure=true"

# Behind a reverse proxy with Basic Auth
gotify: "wss://proxyuser:p%40ssword@gotify.example.com?token=abc123&reconnect_delay_max=120s"
```

---

### MQTT Broker DSN

```
mqtt://[user:pass@]host[:port][?params]    # plain TCP
mqtts://[user:pass@]host[:port][?params]   # TCP over TLS
ws://[user:pass@]host[:port][/path][?params]    # WebSocket
wss://[user:pass@]host[:port][/path][?params]   # WebSocket over TLS
```

| Parameter | Default | Description |
|-----------|---------|-------------|
| `client_id` | auto | MQTT client ID. Auto = `{group}-{target}-{host}` |
| `qos` | `0` | QoS level: `0`, `1`, or `2` |
| `retain` | `false` | Set the RETAIN flag on published messages |
| `queue_size` | `256` | Async publish queue depth per broker |
| `connect_timeout` | `10s` | Connection timeout |
| `keep_alive` | `30s` | MQTT keep-alive interval |
| `reconnect_delay` | `1s` | Initial reconnect delay |
| `reconnect_delay_max` | `60s` | Maximum reconnect delay |
| `ca` | — | CA certificate file path (`mqtts`/`wss`) |
| `cert` | — | Client certificate path (mutual TLS) |
| `key` | — | Client private key path (mutual TLS) |
| `insecure` | `false` | Skip TLS certificate verification |

**Examples:**
```yaml
# Plain MQTT
dsn: "mqtt://localhost:1883?qos=1"

# MQTT over TLS with CA cert
dsn: "mqtts://user:pass@mqtt.example.com:8883?ca=/etc/ssl/ca.crt&qos=1"

# Mutual TLS (mTLS)
dsn: "mqtts://mqtt.example.com:8883?ca=/etc/ssl/ca.crt&cert=/etc/ssl/client.crt&key=/etc/ssl/client.key"

# MQTT over WebSocket
dsn: "ws://mqtt.example.com:8083/mqtt?queue_size=512"
```

---

## Topic Templates

Topics support [Go templates](https://pkg.go.dev/text/template). Available variables:

| Variable | Type | Description |
|----------|------|-------------|
| `{{.ID}}` | `uint` | Gotify message ID |
| `{{.AppID}}` | `uint` | Application ID |
| `{{.UserID}}` | `uint` | User ID (resolved at startup via `/current/user`) |
| `{{.UserName}}` | `string` | Username |
| `{{.Title}}` | `string` | Message title (special characters sanitized to `_`) |
| `{{.Priority}}` | `int` | Message priority |
| `{{.Date}}` | `time.Time` | Message timestamp |

**Examples:**
```yaml
topics:
  - "gotify/app/{{.AppID}}/messages"
  - "home/alerts/priority/{{.Priority}}"
  - "users/{{.UserName}}/notifications"
```

## Supported Platforms

| Platform | Binary | Docker |
|----------|--------|--------|
| Linux amd64 | ✅ | ✅ |
| Linux arm64 | ✅ | ✅ |
| Linux arm/v7 | ✅ | ✅ |
| Linux arm/v6 | ✅ | ✅ |
| Linux 386 | ✅ | ✅ |
| Linux riscv64 | ✅ | ✅ |
| Linux mips64le | ✅ | — |
| macOS amd64 | ✅ | — |
| macOS arm64 (M-series) | ✅ | — |
| Windows amd64 | ✅ | — |
| Windows arm64 | ✅ | — |

## License

MIT
