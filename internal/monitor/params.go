package monitor

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// SystemParams 是 monitor.system 的入参：per_cpu 打开分核读数，include_loopback 保留 lo。
type SystemParams struct {
	PerCPU          bool `json:"per_cpu"`
	IncludeLoopback bool `json:"include_loopback"`
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

// parseSystemParams 校验 system 入参：只接受布尔开关，缺省全部 false。
func parseSystemParams(raw json.RawMessage) (SystemParams, error) {
	var params SystemParams
	if err := requireObject(raw); err != nil {
		return params, err
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return params, fmt.Errorf("system 参数解析失败: %w", err)
	}
	return params, nil
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
