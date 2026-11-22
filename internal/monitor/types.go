// Package monitor 实现 monitor 插件的方法级执行面：
// monitor.sample（采样）与 monitor.alert.set（阈值告警配置）。
//
// 硬约束：本包不内置任何调度器，不起周期定时器 / 定时回调 / 周期循环；
// 采样由宿主按需调用方法触发。骨架口径下采样返回结构化占位数据，
// 不真读 /proc，不读取运行配置，不产生任何副作用（G-19 / G-21）。
package monitor

import "fmt"

// FaultKind 是四类故障注入的类别标识（G-20 / proposal §4.8 S-04）。
type FaultKind string

const (
	FaultKill    FaultKind = "kill"    // 进程被 kill -9 终止
	FaultTimeout FaultKind = "timeout" // 调用超时
	FaultDisk    FaultKind = "disk"    // 磁盘满 / ENOSPC
	FaultCgroup  FaultKind = "cgroup"  // cgroup memory.max 超限 / OOM
)

// FaultError 是执行面注入的故障错误，携带类别与上下文说明。
type FaultError struct {
	Kind    FaultKind
	Message string
}

// Error 实现 error 接口。
func (e *FaultError) Error() string { return fmt.Sprintf("%s: %s", e.Kind, e.Message) }

// NewFaultError 构造一类故障错误，供故障注入 executor 使用。
func NewFaultError(kind FaultKind, msg string) error {
	return &FaultError{Kind: kind, Message: msg}
}

// Context 是单次方法调用的请求级上下文，Deadline 来自请求 timeout_ms。
// 它只在调用期存在，不是周期定时器（G-19 允许请求级超时）。
type Context struct {
	Deadline int64 // Unix 纳秒；0 表示未设置
}

// Executor 是方法执行面。生产实现返回占位采样数据；故障注入实现返回 FaultError。
type Executor interface {
	Sample(ctx Context, metrics []string) (SampleResult, error)
	SetAlert(ctx Context, req AlertRequest) (AlertResult, error)
}
