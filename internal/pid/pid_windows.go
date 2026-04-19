//go:build windows

package pid

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// File 代表已持有的 PID 文件（Windows 实现，无文件锁，依赖 PID 存活检测）
type File struct {
	path string
}

// Acquire 检查 PID 文件中记录的进程是否仍在运行，若是则拒绝启动；
// 否则写入当前 PID。Windows 不支持 flock，使用 PID 存活检测。
func Acquire(path string) (*File, error) {
	if path == "" {
		path = os.TempDir() + `\gotify2mqtt.pid`
	}

	// 检查已有 PID 文件
	if data, err := os.ReadFile(path); err == nil {
		pidStr := strings.TrimSpace(string(data))
		if pid, err := strconv.Atoi(pidStr); err == nil {
			if proc, err := os.FindProcess(pid); err == nil {
				// Windows 的 FindProcess 永远成功，需用 Signal(0) 探测
				if err := proc.Signal(os.Signal(nil)); err == nil {
					return nil, fmt.Errorf("服务已在运行（PID %d，pid 文件 %s）", pid, path)
				}
			}
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("创建 PID 目录: %w", err)
	}

	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return nil, fmt.Errorf("写入 PID 文件: %w", err)
	}
	return &File{path: path}, nil
}

// Release 删除 PID 文件
func (p *File) Release() {
	_ = os.Remove(p.path)
}
