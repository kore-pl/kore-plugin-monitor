//go:build linux

package monitor

// Linux 路径保持不变：生产执行面仍是真实 /proc 与 /sys 读取。
func newPlatformExecutor() Executor {
	return NewCollector(DefaultProcRoot, DefaultSysRoot)
}
