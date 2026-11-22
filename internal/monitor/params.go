package monitor

import (
	"encoding/json"
	"fmt"
	"strings"
)

// 参数枚举白名单：数据形状显式，不接收任意键。
var allowedMetrics = map[string]bool{"cpu": true, "memory": true, "disk": true}
var allowedComparators = map[string]bool{"gt": true, "gte": true, "lt": true, "lte": true}

const (
	maxRuleIDLen = 64
	maxThreshold = 100.0
)

// sampleParams 是 monitor.sample 的入参：metrics 为空表示采样全部指标。
type sampleParams struct {
	Metrics []string `json:"metrics,omitempty"`
}

// parseSampleParams 校验 sample 入参：必须是 JSON 对象，metrics 取合法子集。
func parseSampleParams(raw json.RawMessage) (sampleParams, error) {
	p := sampleParams{}
	if err := requireObject(raw); err != nil {
		return p, err
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, fmt.Errorf("sample 参数解析失败: %w", err)
	}
	for _, m := range p.Metrics {
		if !allowedMetrics[m] {
			return p, fmt.Errorf("sample 指标 %q 非法（仅 cpu/memory/disk）", m)
		}
	}
	return p, nil
}

// alertWire 是解析期中间结构：threshold 用指针区分"未提供"与显式 0。
type alertWire struct {
	RuleID     string   `json:"rule_id"`
	Metric     string   `json:"metric"`
	Comparator string   `json:"comparator"`
	Threshold  *float64 `json:"threshold"`
	Enabled    bool     `json:"enabled"`
}

// parseAlertParams 校验 alert.set 入参：必填字段、枚举与阈值范围 [0,100]。
func parseAlertParams(raw json.RawMessage) (AlertRequest, error) {
	var p AlertRequest
	if err := requireObject(raw); err != nil {
		return p, err
	}
	var wire alertWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return p, fmt.Errorf("alert.set 参数解析失败: %w", err)
	}
	if wire.Threshold == nil {
		return p, fmt.Errorf("threshold 为必填字段")
	}
	p = AlertRequest{
		RuleID: wire.RuleID, Metric: wire.Metric, Comparator: wire.Comparator,
		Threshold: *wire.Threshold, Enabled: wire.Enabled,
	}
	if !allowedMetrics[p.Metric] {
		return p, fmt.Errorf("metric %q 非法（仅 cpu/memory/disk）", p.Metric)
	}
	if !allowedComparators[p.Comparator] {
		return p, fmt.Errorf("comparator %q 非法（仅 gt/gte/lt/lte）", p.Comparator)
	}
	if p.Threshold < 0 || p.Threshold > maxThreshold {
		return p, fmt.Errorf("threshold %v 越界（须在 0..100）", p.Threshold)
	}
	if len(p.RuleID) > maxRuleIDLen {
		return p, fmt.Errorf("rule_id 超过 %d 字符", maxRuleIDLen)
	}
	p.RuleID = strings.TrimSpace(p.RuleID)
	if p.RuleID == "" {
		return p, fmt.Errorf("rule_id 为必填字段")
	}
	return p, nil
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
