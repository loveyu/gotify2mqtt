//go:build !windows

package pid

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
)

// File 代表已持有的 PID 文件锁
type File struct {
	path string
	file *os.File
}

// Acquire 尝试独占锁定 PID 文件，写入当前进程 PID。
// 若文件已被其他进程锁定，立即返回错误（非阻塞）。
func Acquire(path string) (*File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("打开 PID 文件 %s: %w", path, err)
	}

	// 非阻塞独占锁：若已有进程持有锁则立即返回 EWOULDBLOCK
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("服务已在运行（pid 文件 %s 已被锁定）", path)
	}

	// 清空并写入当前 PID
	if err := f.Truncate(0); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, fmt.Errorf("清空 PID 文件: %w", err)
	}
	if _, err := f.WriteString(strconv.Itoa(os.Getpid())); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, fmt.Errorf("写入 PID: %w", err)
	}

	return &File{path: path, file: f}, nil
}

// Release 解锁并删除 PID 文件，应在进程退出前调用（通常 defer）。
func (p *File) Release() {
	_ = syscall.Flock(int(p.file.Fd()), syscall.LOCK_UN)
	_ = p.file.Close()
	_ = os.Remove(p.path)
}
