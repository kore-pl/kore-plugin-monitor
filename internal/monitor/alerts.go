package monitor

import "fmt"

// 阈值告警只在返回体里标注 alerts[]：插件不发通知、不发邮件、不发 Webhook，
// 通知归主壳告警中心（出网违 G-10）。阈值可由 monitor.system 的 thresholds 参数覆盖。
const (
	DefaultCPUThreshold    = 90.0
	DefaultMemoryThreshold = 90.0
	DefaultDiskThreshold   = 90.0
	// criticalMargin 是判定 critical 的超出幅度：当前值 - 阈值 ≥ 10 个百分点。
	criticalMargin = 10.0
)

// Thresholds 是三类指标的阈值（百分比）；0 表示关闭该指标的告警。
type Thresholds struct {
	CPUPercent    float64
	MemoryPercent float64
	DiskPercent   float64
}

// defaultThresholds 返回默认阈值：CPU / 内存 / 磁盘均为 90%。
func defaultThresholds() Thresholds {
	return Thresholds{CPUPercent: DefaultCPUThreshold, MemoryPercent: DefaultMemoryThreshold, DiskPercent: DefaultDiskThreshold}
}

// Alert 是单条阈值告警：指标、当前值、阈值、级别与可操作 hint。
type Alert struct {
	Metric    string  `json:"metric"`
	Current   float64 `json:"current"`
	Threshold float64 `json:"threshold"`
	Level     string  `json:"level"`
	Hint      string  `json:"hint"`
}

// evaluateAlerts 按 cpu → memory → disk 的固定顺序生成告警；阈值 0 表示关闭。
func evaluateAlerts(cpu, memory, disk float64, diskMount string, thresholds Thresholds) []Alert {
	alerts := make([]Alert, 0, 3)
	if alert, ok := buildAlert("cpu", cpu, thresholds.CPUPercent, "CPU 使用率",
		"先用 monitor.processes {\"sort\":\"cpu\"} 查看占用最高的进程，再决定是否限制或扩容。"); ok {
		alerts = append(alerts, alert)
	}
	if alert, ok := buildAlert("memory", memory, thresholds.MemoryPercent, "内存使用率",
		"先用 monitor.processes {\"sort\":\"mem\"} 查看内存占用最高的进程，必要时释放或扩容。"); ok {
		alerts = append(alerts, alert)
	}
	diskLabel := "磁盘使用率"
	if diskMount != "" {
		diskLabel = fmt.Sprintf("磁盘 %s 使用率", diskMount)
	}
	if alert, ok := buildAlert("disk", disk, thresholds.DiskPercent, diskLabel,
		"用 monitor.disks 查看各挂载点明细，清理日志或扩容后再复测。"); ok {
		alerts = append(alerts, alert)
	}
	return alerts
}

// buildAlert 在 current ≥ threshold 时生成告警；threshold ≤ 0 表示关闭。
func buildAlert(metric string, current, threshold float64, label, next string) (Alert, bool) {
	if threshold <= 0 || current < threshold {
		return Alert{}, false
	}
	level := "warning"
	if current-threshold >= criticalMargin {
		level = "critical"
	}
	return Alert{
		Metric: metric, Current: current, Threshold: threshold, Level: level,
		Hint: fmt.Sprintf("%s %.2f%% 已超过阈值 %.2f%%：%s", label, current, threshold, next),
	}, true
}

// diskSummary 取用于告警/曲线的磁盘占用：优先 / 挂载点，否则取占用最高的挂载点。
func diskSummary(disks []DiskUsage) (percent float64, mount string) {
	for _, disk := range disks {
		if disk.Mount == "/" {
			return disk.UsedPercent, disk.Mount
		}
	}
	best := -1.0
	for _, disk := range disks {
		if disk.UsedPercent > best {
			best, percent, mount = disk.UsedPercent, disk.UsedPercent, disk.Mount
		}
	}
	if best < 0 {
		return 0, ""
	}
	return percent, mount
}
