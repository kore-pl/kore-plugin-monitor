package monitor

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// placeholderExecutor 是生产执行面：采样返回结构化占位数据，不真读 /proc，
// 不读取运行配置、无副作用；告警只回显规则摘要，不落盘、不注册定时器。
type placeholderExecutor struct{}

// NewExecutor 返回生产执行面（占位口径）。
func NewExecutor() Executor { return placeholderExecutor{} }

// Sample 返回结构化占位采样数据；metrics 为空时返回全部指标。
func (placeholderExecutor) Sample(ctx Context, metrics []string) (SampleResult, error) {
	want := toSet(metrics)
	result := SampleResult{
		Schema:    "monitor.sample.v1",
		Source:    "placeholder",
		SampledAt: time.Unix(0, ctx.Deadline).UTC().Format(time.RFC3339Nano),
	}
	if len(want) == 0 || want["cpu"] {
		result.CPU = CPUSample{UsagePercent: 0, Cores: 0}
	}
	if len(want) == 0 || want["memory"] {
		result.Memory = MemorySample{UsedPercent: 0, TotalKB: 0, AvailableKB: 0}
	}
	if len(want) == 0 || want["disk"] {
		result.Disk = DiskSample{UsedPercent: 0, TotalBytes: 0, FreeBytes: 0}
	}
	return result, nil
}

// SetAlert 返回告警规则的结构化确认，digest 由规则字段计算，不落盘。
func (placeholderExecutor) SetAlert(ctx Context, req AlertRequest) (AlertResult, error) {
	sum := sha256.Sum256([]byte(req.RuleID + "|" + req.Metric + "|" + req.Comparator + "|" + req.EnabledString()))
	return AlertResult{RuleID: req.RuleID, Applied: true, Digest: hex.EncodeToString(sum[:8])}, nil
}

// EnabledString 把 bool 显式转成字符串，避免用 fmt %v 的隐式格式。
func (r AlertRequest) EnabledString() string {
	if r.Enabled {
		return "true"
	}
	return "false"
}

// toSet 把指标列表转成查找集合；无需外部依赖。
func toSet(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, item := range items {
		set[item] = true
	}
	return set
}
