package monitor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DefaultSampleInterval 是 CPU / 进程 CPU% 的两次采样间隔。
// 它是请求内的 time.Sleep，不是 ticker：一次调用只采样一次，不常驻后台。
const DefaultSampleInterval = 200 * time.Millisecond

// Collector 是真实 /proc 与 /sys 采集执行面；root 可注入，便于 fixture 单测。
type Collector struct {
	procRoot string
	sysRoot  string
	stateDir string
	interval time.Duration
	now      func() time.Time
	sleep    func(time.Duration)
}

// NewExecutor 返回生产执行面；平台分支由 newPlatformExecutor 注入，
// Linux 保持原 /proc 路径不变，darwin/windows 走各自平台文件里的真实命令采集。
func NewExecutor() Executor { return newPlatformExecutor() }

// NewCollector 构造采集器；root 为空时回落到标准挂载点。
// stateDir 是插件状态目录（KORE_STATE_DIR），用于持久化历史曲线；未设置时只保留进程内缓冲。
func NewCollector(procRoot, sysRoot string) *Collector {
	if procRoot == "" {
		procRoot = DefaultProcRoot
	}
	if sysRoot == "" {
		sysRoot = DefaultSysRoot
	}
	return &Collector{
		procRoot: procRoot, sysRoot: sysRoot, stateDir: stateDirFromEnv(),
		interval: DefaultSampleInterval,
		now:      time.Now, sleep: time.Sleep,
	}
}

// SetSampleInterval 覆盖请求内两次采样的等待间隔（配置契约 sample_interval_ms）。
// 它仍是单次调用内的 time.Sleep，不引入 ticker / 后台 goroutine（G-19）。
func (c *Collector) SetSampleInterval(d time.Duration) {
	if d > 0 {
		c.interval = d
	}
}

// System 返回一屏概览：CPU / 内存 / 负载 / uptime / 磁盘 / 网络，并标注阈值告警与历史曲线。
// 任何单个 /proc 文件读取失败只追加 warning，不中断整次调用。
func (c *Collector) System(ctx Context, params SystemParams) (SystemResult, error) {
	params.withDefaults()
	result := SystemResult{
		Schema: "monitor.system.v1", Source: "proc", Warnings: []string{}, Alerts: []Alert{},
	}
	interval, err := c.window(ctx)
	if err != nil {
		return result, err
	}
	sampledAt := c.now()
	result.SampledAt = sampledAt.UTC().Format(time.RFC3339Nano)
	result.CPU = c.collectCPU(interval, params.PerCPU, &result.Warnings)
	result.Memory = c.collectMemory(&result.Warnings)
	result.Load = c.collectLoad(&result.Warnings)
	result.Uptime = c.collectUptime(&result.Warnings)
	result.Disks = c.diskUsages(&result.Warnings)
	result.Network = c.collectNetwork(params.IncludeLoopback, &result.Warnings)
	diskPercent, diskMount := diskSummary(result.Disks)
	result.Alerts = evaluateAlerts(result.CPU.UsagePercent, result.Memory.UsedPercent, diskPercent, diskMount, params.Thresholds)
	result.History = c.recordHistory(HistorySample{
		TSUnixMS: sampledAt.UnixMilli(), CPUPercent: result.CPU.UsagePercent,
		MemoryPercent: result.Memory.UsedPercent, Load1: result.Load.Load1, DiskPercent: diskPercent,
	}, params, &result.Warnings)
	return result, nil
}

// window 结合请求 deadline 计算本次采样间隔：预算不足时缩短窗口，deadline 已过则报错。
func (c *Collector) window(ctx Context) (time.Duration, error) {
	interval := c.interval
	if ctx.Deadline == 0 {
		return interval, nil
	}
	remaining := time.Until(time.Unix(0, ctx.Deadline))
	if remaining <= 0 {
		return 0, fmt.Errorf("%w（剩余 %s）", errCallTimeout, remaining)
	}
	if remaining < interval+50*time.Millisecond {
		interval = remaining / 2
		if interval < 5*time.Millisecond {
			interval = 5 * time.Millisecond
		}
	}
	return interval, nil
}

// collectCPU 读取两次 /proc/stat 并求差分；单次读取失败只降级为 0 + warning。
func (c *Collector) collectCPU(interval time.Duration, perCPU bool, warnings *[]string) CPUInfo {
	info := CPUInfo{SampleIntervalMS: interval.Milliseconds()}
	prevTotal, prevPer, err := c.readCPUSnapshot()
	if err != nil {
		addWarning(warnings, "读取 /proc/stat 失败: %v", err)
		return info
	}
	c.sleep(interval)
	currTotal, currPer, err := c.readCPUSnapshot()
	if err != nil {
		addWarning(warnings, "第二次读取 /proc/stat 失败: %v", err)
		return info
	}
	breakdown := cpuDeltaUsage(prevTotal, currTotal)
	info.UsagePercent = breakdown.Usage
	info.UserPercent = breakdown.User
	info.SystemPercent = breakdown.System
	info.IOWaitPercent = breakdown.IOWait
	info.IdlePercent = breakdown.Idle
	info.Cores = len(currPer)
	if perCPU {
		info.PerCPU = make([]PerCPU, 0, len(currPer))
		for _, name := range sortedCPUKeys(currPer) {
			prev, ok := prevPer[name]
			if !ok {
				continue
			}
			info.PerCPU = append(info.PerCPU, PerCPU{Name: name, UsagePercent: cpuDeltaUsage(prev, currPer[name]).Usage})
		}
	}
	return info
}

// readCPUSnapshot 读取 /proc/stat 并解析总 CPU 与分核计数。
func (c *Collector) readCPUSnapshot() (cpuTimes, map[string]cpuTimes, error) {
	data, err := os.ReadFile(filepath.Join(c.procRoot, "stat"))
	if err != nil {
		return cpuTimes{}, nil, err
	}
	return parseCPUStat(string(data))
}

// sortedCPUKeys 按核编号数值排序（cpu2 在 cpu10 之前）。
func sortedCPUKeys(per map[string]cpuTimes) []string {
	keys := make([]string, 0, len(per))
	for key := range per {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return cpuIndex(keys[i]) < cpuIndex(keys[j])
	})
	return keys
}

// cpuIndex 从 cpuN 取出 N；无法解析时返回大值排在末尾。
func cpuIndex(name string) int {
	value, err := strconv.Atoi(strings.TrimPrefix(name, "cpu"))
	if err != nil {
		return 1 << 30
	}
	return value
}

// addWarning 追加去重后的告警；重复告警只保留第一条。
func addWarning(warnings *[]string, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	for _, existing := range *warnings {
		if existing == message {
			return
		}
	}
	*warnings = append(*warnings, message)
}

// resolveSymlink 是 filepath.EvalSymlinks 的薄封装，供 platform 层调用。
func resolveSymlink(path string) (string, error) { return filepath.EvalSymlinks(path) }
