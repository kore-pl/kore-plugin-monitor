package monitor

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// shell 包只做固定 argv 命令执行与通用采样调度；平台命令与解析由
// _darwin.go / _windows.go 提供，设备专有 API 不进入内核（G-16 薄壳边界）。

const (
	shellCommandTimeout = 3 * time.Second
	maxShellOutputBytes = 256 * 1024
)

type commandSpec struct {
	program string
	args    []string
}

type commandRunner func(commandSpec) (string, error)

func runShellCommand(spec commandSpec) (string, error) {
	if spec.program == "" {
		return "", fmt.Errorf("命令名为空")
	}
	ctx, cancel := context.WithTimeout(context.Background(), shellCommandTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, spec.program, spec.args...).Output()
	if err != nil {
		return "", fmt.Errorf("执行 %s 失败: %w", spec.program, err)
	}
	if len(out) > maxShellOutputBytes {
		out = out[:maxShellOutputBytes]
	}
	return string(out), nil
}

type shellCommands struct {
	CPU         commandSpec
	CPUPer      commandSpec
	MemoryTotal commandSpec
	MemoryPages commandSpec
	Load        commandSpec
	BootTime    commandSpec
	NCPU        commandSpec
	Disks       commandSpec
	Network     commandSpec
	Processes   commandSpec
	Temperature commandSpec
}

type shellOutputs struct {
	values map[string]string
	failed map[string]error
}

func newShellOutputs() *shellOutputs {
	return &shellOutputs{values: map[string]string{}, failed: map[string]error{}}
}

func (o *shellOutputs) run(runner commandRunner, key string, spec commandSpec) {
	if spec.program == "" {
		return
	}
	out, err := runner(spec)
	if err != nil {
		o.failed[key] = err
		return
	}
	o.values[key] = out
}

func (o *shellOutputs) get(key string) string { return o.values[key] }

func warningFor(o *shellOutputs, key, label, advice string) string {
	if err, ok := o.failed[key]; ok {
		return fmt.Sprintf("%s 采集失败：%v；%s", label, err, advice)
	}
	return ""
}

type shellSystemInput struct {
	Outputs   *shellOutputs
	CPUFirst  string
	CPUSecond string
	Elapsed   time.Duration
	Interval  time.Duration
	Now       time.Time
}

type systemSnapshot struct {
	CPU      CPUInfo
	Memory   MemoryInfo
	Load     LoadInfo
	Uptime   UptimeInfo
	Disks    []DiskUsage
	Network  []NetStat
	Warnings []string
}

type shellParsers struct {
	system      func(shellSystemInput) (systemSnapshot, error)
	processes   func(first, second string, o *shellOutputs, limit int, sortKey string) ([]ProcessInfo, []string, error)
	disks       func(output string, path string) ([]DiskDetail, []string, error)
	temperature func(output string) (TemperatureResult, error)
}

type shellExecutor struct {
	osName   string
	source   string
	commands shellCommands
	parsers  shellParsers
	runner   commandRunner
	now      func() time.Time
	sleep    func(time.Duration)
	interval time.Duration
	stateDir string
}

func newShellExecutor(osName, source string, commands shellCommands, parsers shellParsers) *shellExecutor {
	return &shellExecutor{
		osName: osName, source: source, commands: commands, parsers: parsers,
		runner: runShellCommand, now: time.Now, sleep: time.Sleep,
		interval: DefaultSampleInterval, stateDir: stateDirFromEnv(),
	}
}

func (s *shellExecutor) SetSampleInterval(d time.Duration) {
	if d > 0 {
		s.interval = d
	}
}

func (s *shellExecutor) window(ctx Context) (time.Duration, error) {
	interval := s.interval
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

func (s *shellExecutor) System(ctx Context, params SystemParams) (SystemResult, error) {
	params.withDefaults()
	interval, err := s.window(ctx)
	if err != nil {
		return SystemResult{}, err
	}
	sampledAt := s.now()
	o := newShellOutputs()
	o.run(s.runner, "cpu", s.commands.CPU)
	first := o.get("cpu")
	start := s.now()
	s.sleep(interval)
	o.run(s.runner, "cpu2", s.commands.CPU)
	elapsed := s.now().Sub(start)
	steps := []struct {
		key string
		cmd commandSpec
	}{
		{"cpu_per", s.commands.CPUPer}, {"mem_total", s.commands.MemoryTotal},
		{"mem_pages", s.commands.MemoryPages}, {"load", s.commands.Load},
		{"boot", s.commands.BootTime}, {"ncpu", s.commands.NCPU},
		{"disks", s.commands.Disks}, {"network", s.commands.Network},
	}
	for _, item := range steps {
		o.run(s.runner, item.key, item.cmd)
	}
	snap, err := s.parsers.system(shellSystemInput{
		Outputs: o, CPUFirst: first, CPUSecond: o.get("cpu2"),
		Elapsed: elapsed, Interval: interval, Now: sampledAt,
	})
	if err != nil {
		return SystemResult{}, err
	}
	result := SystemResult{
		Schema: "monitor.system.v1", Source: s.source, Warnings: snap.Warnings,
		SampledAt: sampledAt.UTC().Format(time.RFC3339Nano),
		CPU:       snap.CPU, Memory: snap.Memory, Load: snap.Load, Uptime: snap.Uptime,
		Disks: snap.Disks, Network: snap.Network, Alerts: []Alert{},
	}
	diskPercent, diskMount := diskSummary(result.Disks)
	result.Alerts = evaluateAlerts(result.CPU.UsagePercent, result.Memory.UsedPercent, diskPercent, diskMount, params.Thresholds)
	result.History = recordHistory(s.stateDir, HistorySample{
		TSUnixMS: sampledAt.UnixMilli(), CPUPercent: result.CPU.UsagePercent,
		MemoryPercent: result.Memory.UsedPercent, Load1: result.Load.Load1, DiskPercent: diskPercent,
	}, params, &result.Warnings)
	return result, nil
}

func (s *shellExecutor) Processes(ctx Context, params ProcessParams) (ProcessesResult, error) {
	result := ProcessesResult{Schema: "monitor.processes.v1", Sort: params.Sort, Limit: params.Limit, Warnings: []string{}}
	interval, err := s.window(ctx)
	if err != nil {
		return result, err
	}
	result.SampleIntervalMS = interval.Milliseconds()
	result.SampledAt = s.now().UTC().Format(time.RFC3339Nano)
	o := newShellOutputs()
	o.run(s.runner, "processes", s.commands.Processes)
	first := o.get("processes")
	start := s.now()
	s.sleep(interval)
	o.run(s.runner, "processes", s.commands.Processes)
	o.run(s.runner, "mem_total", s.commands.MemoryTotal)
	elapsed := s.now().Sub(start)
	items, warnings, err := s.parsers.processes(first, o.get("processes"), o, params.Limit, params.Sort)
	if err != nil {
		return result, err
	}
	result.Warnings = append(result.Warnings, warnings...)
	if elapsed <= 0 {
		addWarning(&result.Warnings, "进程采样间隔过短，CPU 增量不可靠；请稍后刷新")
	}
	sortProcessItems(items, params.Sort)
	result.Total = len(items)
	if len(items) > params.Limit {
		items = items[:params.Limit]
	}
	result.Items = items
	result.Count = len(items)
	return result, nil
}

func (s *shellExecutor) Disks(ctx Context, params DisksParams) (DisksResult, error) {
	result := DisksResult{Schema: "monitor.disks.v1", Path: params.Path, Warnings: []string{}, Items: []DiskDetail{}}
	result.SampledAt = s.now().UTC().Format(time.RFC3339Nano)
	if s.commands.Disks.program == "" {
		addWarning(&result.Warnings, "当前系统没有可用的磁盘容量命令，未返回伪造读数")
		return result, nil
	}
	out, err := s.runner(s.commands.Disks)
	if err != nil {
		addWarning(&result.Warnings, "磁盘容量采集失败：%v；请检查命令是否可用后重试", err)
		return result, nil
	}
	items, warnings, err := s.parsers.disks(out, params.Path)
	if err != nil {
		return result, err
	}
	result.Warnings = append(result.Warnings, warnings...)
	result.Items = items
	result.Count = len(items)
	return result, nil
}

func (s *shellExecutor) Temperature(ctx Context) (TemperatureResult, error) {
	result := TemperatureResult{Schema: "monitor.temperature.v1", Zones: []ThermalZone{}, Warnings: []string{}}
	result.SampledAt = s.now().UTC().Format(time.RFC3339Nano)
	if s.commands.Temperature.program == "" {
		addWarning(&result.Warnings, "当前系统没有可用的温度探针，未填 0 冒充实测")
		return result, nil
	}
	out, err := s.runner(s.commands.Temperature)
	if err != nil {
		addWarning(&result.Warnings, "温度探针不可读：%v；通常需要管理员权限，未填 0 冒充实测", err)
		return result, nil
	}
	res, err := s.parsers.temperature(out)
	if err != nil {
		addWarning(&result.Warnings, "温度数据解析失败：%v；未填 0 冒充实测", err)
		return result, nil
	}
	res.SampledAt = result.SampledAt
	res.Warnings = append(res.Warnings, result.Warnings...)
	return res, nil
}
