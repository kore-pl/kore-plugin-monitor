// Package monitor 实现 monitor 插件的方法级执行面：真实读取 Linux /proc 与 /sys。
//
// 硬约束（G-19 / G-21）：本包不内置调度器，不起 ticker / 定时回调 / 周期循环；
// CPU 与进程 CPU% 都在单次请求内做两次采样 + time.Sleep 求 delta。
// 非 Linux 平台由 Call 入口显式返回 E_UNSUPPORTED，不做静默降级。
package monitor

import (
	"errors"
	"fmt"
)

// Linux 采集常量：CLKTCK 固定 100（USER_HZ 的 Linux ABI 事实，CGO_ENABLED=0 下无 libc）。
const (
	CLKTCK              int64 = 100
	DefaultProcRoot           = "/proc"
	DefaultSysRoot            = "/sys"
	DefaultProcessLimit       = 20
	MaxProcessLimit           = 500
	DiskSectorBytes     int64 = 512
)

// 执行面哨兵错误：平台不支持与请求级超时。
var (
	errUnsupportedPlatform = errors.New("monitor 插件只在 Linux 提供真实 /proc 采集")
	errCallTimeout         = errors.New("调用在采样窗口内到达 deadline")
	errPathNotMounted      = errors.New("path 未匹配到任何真实块设备挂载点")
)

// FaultKind 是四类故障注入的类别标识（G-20 / proposal §4.8 S-04）。
type FaultKind string

// 故障类别。
const (
	FaultKill    FaultKind = "kill"
	FaultTimeout FaultKind = "timeout"
	FaultDisk    FaultKind = "disk"
	FaultCgroup  FaultKind = "cgroup"
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

// Executor 是方法执行面：生产实现真实读取 /proc 与 /sys；故障注入实现返回 FaultError。
type Executor interface {
	System(ctx Context, params SystemParams) (SystemResult, error)
	Processes(ctx Context, params ProcessParams) (ProcessesResult, error)
	Disks(ctx Context, params DisksParams) (DisksResult, error)
	Temperature(ctx Context) (TemperatureResult, error)
}
