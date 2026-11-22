package monitor

// SampleResult 是 monitor.sample 的结构化占位结果（不真读 /proc）。
// 字段显式列出，不使用 object / any（AGENTS.md 数据形状纪律）。
type SampleResult struct {
	Schema    string       `json:"schema"`
	Source    string       `json:"source"`
	SampledAt string       `json:"sampled_at"`
	CPU       CPUSample    `json:"cpu"`
	Memory    MemorySample `json:"memory"`
	Disk      DiskSample   `json:"disk"`
}

// CPUSample 是 CPU 采样占位字段。
type CPUSample struct {
	UsagePercent float64 `json:"usage_percent"`
	Cores        int     `json:"cores"`
}

// MemorySample 是内存采样占位字段。
type MemorySample struct {
	UsedPercent float64 `json:"used_percent"`
	TotalKB     int64   `json:"total_kb"`
	AvailableKB int64   `json:"available_kb"`
}

// DiskSample 是磁盘采样占位字段。
type DiskSample struct {
	UsedPercent float64 `json:"used_percent"`
	TotalBytes  int64   `json:"total_bytes"`
	FreeBytes   int64   `json:"free_bytes"`
}

// AlertRequest 是 monitor.alert.set 的已校验入参。
type AlertRequest struct {
	RuleID     string  `json:"rule_id"`
	Metric     string  `json:"metric"`
	Comparator string  `json:"comparator"`
	Threshold  float64 `json:"threshold"`
	Enabled    bool    `json:"enabled"`
}

// AlertResult 是 monitor.alert.set 的结构化结果。
type AlertResult struct {
	RuleID  string `json:"rule_id"`
	Applied bool   `json:"applied"`
	Digest  string `json:"digest"`
}
