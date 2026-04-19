package forwarder

import (
	"context"
	"log"

	"git.loveyu.info/microservice/gotify-mqtt-forwarder/internal/config"
)

// Manager 统一管理所有 Group 的生命周期
type Manager struct {
	cfg    *config.Config
	groups []*Group
}

// NewManager 创建 Manager
func NewManager(cfg *config.Config) *Manager {
	return &Manager{cfg: cfg}
}

// Start 初始化所有 Group 并启动各自的 WebSocket goroutine
func (m *Manager) Start(ctx context.Context) {
	for _, gc := range m.cfg.Groups {
		g, err := newGroup(gc)
		if err != nil {
			log.Printf("[manager] 初始化 group %s 失败: %v，跳过", gc.Name, err)
			continue
		}
		m.groups = append(m.groups, g)
		g.wg.Add(1)
		go g.Run(ctx)
		log.Printf("[manager] group %s 已启动", gc.Name)
	}
}

// Stop 优雅关闭所有 Group（等待 WebSocket goroutine 退出后关闭 Broker）
func (m *Manager) Stop() {
	for _, g := range m.groups {
		g.wg.Wait()
		g.Stop()
		log.Printf("[manager] group %s 已关闭", g.cfg.Name)
	}
}
