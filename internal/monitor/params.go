package monitor

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// SystemParams 是 monitor.system 的已校验入参：per_cpu 打开分核读数，
// include_loopback 保留 lo；history_* 控制历史缓冲；thresholds 覆盖告警阈值。
type SystemParams struct {
	PerCPU           bool
	IncludeLoopback  bool
	HistoryPoints    int
	HistoryIntervalS int
	Thresholds       Thresholds
	thresholdsSet    bool // thresholds 显式出现时为 true（区分"用默认值"与"全部关闭"）
}

// systemWire 用指针区分"未提供"与显式 0（显式 0 阈值表示关闭该指标告警）。
type systemWire struct {
	PerCPU           bool           `json:"per_cpu"`
	IncludeLoopback  bool           `json:"include_loopback"`
	HistoryPoints    *int           `json:"history_points"`
	HistoryIntervalS *int           `json:"history_interval_s"`
	Thresholds       *thresholdWire `json:"thresholds"`
}

// thresholdWire 是 thresholds 的显式形状，字段全部可选。
type thresholdWire struct {
	CPUPercent    *float64 `json:"cpu_percent"`
	MemoryPercent *float64 `json:"memory_percent"`
	DiskPercent   *float64 `json:"disk_percent"`
}

// ProcessParams 是 monitor.processes 的已校验入参。
type ProcessParams struct {
	Limit int    `json:"limit"`
	Sort  string `json:"sort"`
}

// DisksParams 是 monitor.disks 的入参：path 为空表示全量，否则取最长前缀挂载点。
type DisksParams struct {
	Path string `json:"path"`
}

// processWire 用指针区分"未提供 limit"与显式 0。
type processWire struct {
	Limit *int   `json:"limit"`
	Sort  string `json:"sort"`
}

// parseSystemParams 校验 system 入参：历史缓冲与阈值缺省取合理默认值。
func parseSystemParams(raw json.RawMessage) (SystemParams, error) {
	params := SystemParams{
		HistoryPoints:    DefaultHistoryPoints,
		HistoryIntervalS: DefaultHistoryInterval,
		Thresholds:       defaultThresholds(),
	}
	if err := requireObject(raw); err != nil {
		return params, err
	}
	var wire systemWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return params, fmt.Errorf("system 参数解析失败: %w", err)
	}
	params.PerCPU = wire.PerCPU
	params.IncludeLoopback = wire.IncludeLoopback
	if wire.HistoryPoints != nil {
		if *wire.HistoryPoints < MinHistoryPoints || *wire.HistoryPoints > MaxHistoryPoints {
			return params, fmt.Errorf("history_points 必须在 %d..%d 之间", MinHistoryPoints, MaxHistoryPoints)
		}
		params.HistoryPoints = *wire.HistoryPoints
	}
	if wire.HistoryIntervalS != nil {
		if *wire.HistoryIntervalS < MinHistoryInterval || *wire.HistoryIntervalS > MaxHistoryInterval {
			return params, fmt.Errorf("history_interval_s 必须在 %d..%d 之间", MinHistoryInterval, MaxHistoryInterval)
		}
		params.HistoryIntervalS = *wire.HistoryIntervalS
	}
	if wire.Thresholds != nil {
		params.thresholdsSet = true
		params.Thresholds = defaultThresholds()
		if err := applyThresholds(&params.Thresholds, wire.Thresholds); err != nil {
			return params, err
		}
	}
	return params, nil
}

// applyThresholds 把显式阈值合并到默认值上；0 表示关闭该指标告警。
func applyThresholds(target *Thresholds, wire *thresholdWire) error {
	fields := []struct {
		name  string
		value *float64
		dest  *float64
	}{
		{"cpu_percent", wire.CPUPercent, &target.CPUPercent},
		{"memory_percent", wire.MemoryPercent, &target.MemoryPercent},
		{"disk_percent", wire.DiskPercent, &target.DiskPercent},
	}
	for _, field := range fields {
		if field.value == nil {
			continue
		}
		if *field.value < 0 || *field.value > 100 {
			return fmt.Errorf("thresholds.%s 必须在 0..100 之间（0 表示关闭）", field.name)
		}
		*field.dest = *field.value
	}
	return nil
}

// withDefaults 补齐未经 parse 直接构造的 SystemParams（内部调用/单测），保证零值有合理语义。
func (p *SystemParams) withDefaults() {
	if p.HistoryPoints == 0 {
		p.HistoryPoints = DefaultHistoryPoints
	}
	if p.HistoryIntervalS == 0 {
		p.HistoryIntervalS = DefaultHistoryInterval
	}
	if !p.thresholdsSet && p.Thresholds == (Thresholds{}) {
		p.Thresholds = defaultThresholds()
	}
}

// parseProcessParams 校验 processes 入参：limit ∈ [1,500] 默认 20；sort ∈ {cpu,mem} 默认 cpu。
func parseProcessParams(raw json.RawMessage) (ProcessParams, error) {
	params := ProcessParams{Limit: DefaultProcessLimit, Sort: "cpu"}
	if err := requireObject(raw); err != nil {
		return params, err
	}
	var wire processWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return params, fmt.Errorf("processes 参数解析失败: %w", err)
	}
	if wire.Limit != nil {
		params.Limit = *wire.Limit
	}
	if params.Limit < 1 || params.Limit > MaxProcessLimit {
		return params, fmt.Errorf("limit 必须在 1..%d 之间", MaxProcessLimit)
	}
	if wire.Sort != "" {
		params.Sort = strings.ToLower(strings.TrimSpace(wire.Sort))
	}
	if params.Sort != "cpu" && params.Sort != "mem" {
		return params, fmt.Errorf("sort 必须是 cpu 或 mem")
	}
	return params, nil
}

// parseDisksParams 校验 disks 入参：path 可选且必须是绝对路径。
func parseDisksParams(raw json.RawMessage) (DisksParams, error) {
	var params DisksParams
	if err := requireObject(raw); err != nil {
		return params, err
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return params, fmt.Errorf("disks 参数解析失败: %w", err)
	}
	params.Path = strings.TrimSpace(params.Path)
	if params.Path == "" {
		return params, nil
	}
	if !filepath.IsAbs(params.Path) {
		return params, fmt.Errorf("path 必须是绝对路径")
	}
	params.Path = filepath.Clean(params.Path)
	if strings.ContainsRune(params.Path, 0) {
		return params, fmt.Errorf("path 含非法字符")
	}
	return params, nil
}

// requireObject 要求参数载荷是 JSON 对象（缺省/null 视为空对象，其余类型拒绝）。
func requireObject(raw json.RawMessage) error {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return nil
	}
	if s[0] != '{' {
		return fmt.Errorf("params 必须是 JSON 对象")
	}
	return nil
}
