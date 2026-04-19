package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"git.loveyu.info/microservice/gotify-mqtt-forwarder/internal/config"
	"git.loveyu.info/microservice/gotify-mqtt-forwarder/internal/forwarder"
	"git.loveyu.info/microservice/gotify-mqtt-forwarder/internal/pid"
)

// version 由 CI 构建时通过 -ldflags "-X main.version=vX.Y.Z" 注入
var version = "dev"

func main() {
	configPath := flag.String("config", "config.yaml", "YAML 配置文件路径")
	flag.Parse()

	log.Printf("gotify-mqtt-forwarder %s 启动", version)

	// 加载并校验配置
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 单进程保证：获取 PID 文件锁
	pidFile, err := pid.Acquire(cfg.PIDFile)
	if err != nil {
		log.Fatalf("启动失败: %v", err)
	}
	defer pidFile.Release()

	// 监听 SIGTERM / SIGINT，触发优雅关闭
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// 启动所有转发 Group
	mgr := forwarder.NewManager(cfg)
	mgr.Start(ctx)

	log.Println("gotify-mqtt-forwarder 已启动，等待消息...")
	<-ctx.Done()

	log.Println("收到退出信号，正在优雅关闭...")
	mgr.Stop()
	log.Println("已退出")
}
