package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pengtang1024/kore-plugin-monitor/internal/monitor"
	"github.com/pengtang1024/kore-plugin-monitor/internal/plugincfg"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// embeddedInfo 是随二进制发布的内嵌清单 manifest.json（只含握手字段，不含 size_bytes）：version / requires_kore /
// capabilities 与安装期 info.json 的握手字段保持一致，由 make dist 前置校验防漂移。
//
//go:embed manifest.json
var embeddedInfo []byte

// manifest 是 info.json 中插件运行期真正需要的字段，其余字段归内核校验。
type manifest struct {
	ID           string   `json:"id"`
	Version      string   `json:"version"`
	Protocol     string   `json:"protocol"`
	RequiresKore string   `json:"requires_kore"`
	Capabilities []string `json:"capabilities"`
}

// 配置环境变量（文件不存在时才生效，优先级 config.json > env > 默认值，见 PLUGIN-CONFIG §4）。
const (
	EnvHistoryPoints    = "KORE_MONITOR_HISTORY_POINTS"
	EnvHistoryInterval  = "KORE_MONITOR_HISTORY_INTERVAL_S"
	EnvCPUThreshold     = "KORE_MONITOR_CPU_THRESHOLD"
	EnvMemoryThreshold  = "KORE_MONITOR_MEMORY_THRESHOLD"
	EnvDiskThreshold    = "KORE_MONITOR_DISK_THRESHOLD"
	EnvProcessLimit     = "KORE_MONITOR_PROCESS_LIMIT"
	EnvProcessSort      = "KORE_MONITOR_PROCESS_SORT"
	EnvSampleIntervalMS = "KORE_MONITOR_SAMPLE_INTERVAL_MS"
)

// 采样间隔边界：太小会放大 /proc 读开销，太大拖住单次调用预算（G-19 请求级采样）。
const (
	minSampleIntervalMS = 5
	maxSampleIntervalMS = 5000
)

// fileValues 是 <state_dir>/config.json 的显式形状（G-24：不引入 object/any）。
// 每个字段都对应采集器真实能力，零值语义与 monitor 包默认值一致。
type fileValues struct {
	HistoryPoints    int    `json:"history_points"`
	HistoryIntervalS int    `json:"history_interval_s"`
	CPUThreshold     int    `json:"cpu_threshold"`
	MemoryThreshold  int    `json:"memory_threshold"`
	DiskThreshold    int    `json:"disk_threshold"`
	ProcessLimit     int    `json:"process_limit"`
	ProcessSort      string `json:"process_sort"`
	SampleIntervalMS int    `json:"sample_interval_ms"`
}

// config 是插件运行期配置：配置契约（config.json）+ 环境变量 + 内嵌清单。
// 指针传递保证 config.set 更新内存后，下一次业务调用立即读到新值（PID 不变）。
type config struct {
	socket       string
	id           string
	token        string
	version      string
	requiresKore string
	capabilities []string
	startedAt    time.Time
	stateDir     string
	store        *plugincfg.Store
	values       fileValues
	source       string
}

// loadConfig 读取协议环境变量、配置契约与内嵌 manifest.json，校验协议主版本与 schema。
func loadConfig() (*config, error) {
	var m manifest
	if err := json.Unmarshal(embeddedInfo, &m); err != nil {
		return nil, fmt.Errorf("解析内嵌 manifest.json: %w", err)
	}
	if m.Protocol != pluginapi.Protocol {
		return nil, fmt.Errorf("info.json.protocol=%q 与 SDK 协议 %q 不一致", m.Protocol, pluginapi.Protocol)
	}
	if err := plugincfg.ValidateSchema(configSchema()); err != nil {
		return nil, fmt.Errorf("config schema 非法: %w", err)
	}
	stateDir := strings.TrimSpace(os.Getenv("KORE_STATE_DIR"))
	store := plugincfg.NewStore(stateDir)
	values, source, err := resolveValues(store)
	if err != nil {
		return nil, err
	}
	cfg := &config{
		socket:       os.Getenv("KORE_PLUGIN_SOCK"),
		id:           os.Getenv("KORE_PLUGIN_ID"),
		token:        os.Getenv("KORE_PLUGIN_TOKEN"),
		version:      m.Version,
		requiresKore: m.RequiresKore,
		capabilities: m.Capabilities,
		startedAt:    time.Now(),
		stateDir:     stateDir,
		store:        store,
		values:       values,
		source:       source,
	}
	if cfg.id == "" {
		cfg.id = m.ID
	}
	if cfg.socket == "" {
		return nil, fmt.Errorf("缺少环境变量 KORE_PLUGIN_SOCK")
	}
	return cfg, nil
}

// defaultValues 是代码默认值，全部锚定 monitor 包真实常量（单一真源）。
func defaultValues() fileValues {
	return fileValues{
		HistoryPoints:    monitor.DefaultHistoryPoints,
		HistoryIntervalS: monitor.DefaultHistoryInterval,
		CPUThreshold:     int(monitor.DefaultCPUThreshold),
		MemoryThreshold:  int(monitor.DefaultMemoryThreshold),
		DiskThreshold:    int(monitor.DefaultDiskThreshold),
		ProcessLimit:     monitor.DefaultProcessLimit,
		ProcessSort:      "cpu",
		SampleIntervalMS: int(monitor.DefaultSampleInterval / time.Millisecond),
	}
}

// resolveValues 实现优先级 config.json > env > 代码默认值。
// 以默认值为基线 unmarshal，config.json 只写部分字段也合法；显式 0 阈值不会被误补成默认值。
func resolveValues(store *plugincfg.Store) (fileValues, string, error) {
	values := defaultValues()
	if store.Exists() {
		raw, _, err := store.Read()
		if err != nil {
			return fileValues{}, "", err
		}
		if err := json.Unmarshal(raw, &values); err != nil {
			return fileValues{}, "", fmt.Errorf("解析插件配置 %s: %w", store.Path(), err)
		}
		normalized, perr := validateValues(values)
		if perr != nil {
			return fileValues{}, "", fmt.Errorf("插件配置 %s 非法: %s", store.Path(), perr.Message)
		}
		return normalized, plugincfg.SourceFile, nil
	}

	source := plugincfg.SourceDefault
	envInts := []struct {
		name  string
		value *int
	}{
		{EnvHistoryPoints, &values.HistoryPoints},
		{EnvHistoryInterval, &values.HistoryIntervalS},
		{EnvCPUThreshold, &values.CPUThreshold},
		{EnvMemoryThreshold, &values.MemoryThreshold},
		{EnvDiskThreshold, &values.DiskThreshold},
		{EnvProcessLimit, &values.ProcessLimit},
		{EnvSampleIntervalMS, &values.SampleIntervalMS},
	}
	for _, item := range envInts {
		raw := strings.TrimSpace(os.Getenv(item.name))
		if raw == "" {
			continue
		}
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return fileValues{}, "", fmt.Errorf("%s=%q 非法：必须是整数", item.name, raw)
		}
		*item.value = parsed
		source = plugincfg.SourceEnv
	}
	if sort := strings.TrimSpace(os.Getenv(EnvProcessSort)); sort != "" {
		values.ProcessSort = strings.ToLower(sort)
		source = plugincfg.SourceEnv
	}
	normalized, perr := validateValues(values)
	if perr != nil {
		return fileValues{}, "", perr
	}
	return normalized, source, nil
}

// validateValues 校验并归一化配置；启动加载与 config.set 共用同一套规则。
func validateValues(values fileValues) (fileValues, *pluginapi.Error) {
	invalid := func(msg string) (fileValues, *pluginapi.Error) {
		return fileValues{}, &pluginapi.Error{Code: pluginapi.E_INVALID, Message: msg}
	}
	if values.HistoryPoints < monitor.MinHistoryPoints || values.HistoryPoints > monitor.MaxHistoryPoints {
		return invalid(fmt.Sprintf("history_points=%d 不合法，请改成 %d 到 %d 之间的整数", values.HistoryPoints, monitor.MinHistoryPoints, monitor.MaxHistoryPoints))
	}
	if values.HistoryIntervalS < monitor.MinHistoryInterval || values.HistoryIntervalS > monitor.MaxHistoryInterval {
		return invalid(fmt.Sprintf("history_interval_s=%d 不合法，请改成 %d 到 %d 秒之间的整数", values.HistoryIntervalS, monitor.MinHistoryInterval, monitor.MaxHistoryInterval))
	}
	thresholds := []struct {
		name  string
		value int
	}{
		{"cpu_threshold", values.CPUThreshold},
		{"memory_threshold", values.MemoryThreshold},
		{"disk_threshold", values.DiskThreshold},
	}
	for _, field := range thresholds {
		if field.value < 0 || field.value > 100 {
			return invalid(fmt.Sprintf("%s=%d 不合法，请填 0 到 100 的整数（0 表示关闭该指标告警）", field.name, field.value))
		}
	}
	if values.ProcessLimit < 1 || values.ProcessLimit > monitor.MaxProcessLimit {
		return invalid(fmt.Sprintf("process_limit=%d 不合法，请改成 1 到 %d 之间的整数", values.ProcessLimit, monitor.MaxProcessLimit))
	}
	values.ProcessSort = strings.ToLower(strings.TrimSpace(values.ProcessSort))
	if values.ProcessSort != "cpu" && values.ProcessSort != "mem" {
		return invalid("process_sort 请填写 cpu 或 mem：按 CPU 或内存占用排序进程列表")
	}
	if values.SampleIntervalMS < minSampleIntervalMS || values.SampleIntervalMS > maxSampleIntervalMS {
		return invalid(fmt.Sprintf("sample_interval_ms=%d 不合法，请改成 %d 到 %d 毫秒之间的整数", values.SampleIntervalMS, minSampleIntervalMS, maxSampleIntervalMS))
	}
	return values, nil
}

// configSchema 是 config.get 返回的表单描述（PLUGIN-CONFIG §2）：显式类型、中文 label/help、
// 每个字段必填 key/label/type/default/required/help，int 带 min/max，select 带非空 options。
// 本仓没有凭据类字段，故不声明 sensitive 字段；通用掩码能力由 plugincfg 按 schema 驱动保留（G-30）。
func configSchema() plugincfg.Schema {
	defaults := defaultValues()
	intField := func(key, label string, value, min, max int, help string) plugincfg.Field {
		return plugincfg.Field{
			Key: key, Label: label, Type: plugincfg.TypeInt,
			Default: mustJSON(value), Required: true, Help: help,
			Min: intPtr(min), Max: intPtr(max),
		}
	}
	return plugincfg.Schema{
		Title: "系统监控采集配置",
		Fields: []plugincfg.Field{
			intField("history_points", "历史曲线保留点数", defaults.HistoryPoints,
				monitor.MinHistoryPoints, monitor.MaxHistoryPoints,
				"monitor.system 返回的历史曲线最多保留多少个采样点；调小后立即裁剪旧点。"),
			intField("history_interval_s", "历史采样间隔（秒）", defaults.HistoryIntervalS,
				monitor.MinHistoryInterval, monitor.MaxHistoryInterval,
				"距上次采样达到该秒数才追加一个历史点；不启用任何后台定时器。"),
			intField("cpu_threshold", "CPU 告警阈值（%）", defaults.CPUThreshold, 0, 100,
				"CPU 使用率达到该值即在结果里标注告警；0 表示关闭 CPU 告警。"),
			intField("memory_threshold", "内存告警阈值（%）", defaults.MemoryThreshold, 0, 100,
				"内存使用率达到该值即在结果里标注告警；0 表示关闭内存告警。"),
			intField("disk_threshold", "磁盘告警阈值（%）", defaults.DiskThreshold, 0, 100,
				"磁盘使用率达到该值即在结果里标注告警；0 表示关闭磁盘告警。"),
			intField("process_limit", "进程列表默认条数", defaults.ProcessLimit, 1, monitor.MaxProcessLimit,
				"monitor.processes 未指定 limit 时返回的进程数上限。"),
			{
				Key: "process_sort", Label: "进程列表默认排序", Type: plugincfg.TypeSelect,
				Default:  mustJSON(defaults.ProcessSort),
				Required: true,
				Help:     "monitor.processes 未指定 sort 时的排序依据：按 CPU 或内存占用降序。",
				Options:  []string{"cpu", "mem"},
			},
			intField("sample_interval_ms", "CPU 采样间隔（毫秒）", defaults.SampleIntervalMS,
				minSampleIntervalMS, maxSampleIntervalMS,
				"CPU 与进程 CPU% 两次采样的请求内等待间隔；不是周期轮询，保存后立即生效。"),
		},
	}
}

// configGet 组装 config.get 结果：schema + 当前值（敏感字段掩码）+ 来源。
func configGet(cfg *config) (json.RawMessage, *pluginapi.Error) {
	values, err := json.Marshal(cfg.values)
	if err != nil {
		return nil, &pluginapi.Error{Code: pluginapi.E_INTERNAL, Message: "序列化当前配置失败: " + err.Error()}
	}
	masked, err := plugincfg.MaskValues(configSchema(), values)
	if err != nil {
		return nil, &pluginapi.Error{Code: pluginapi.E_INTERNAL, Message: "掩码当前配置失败: " + err.Error()}
	}
	result := plugincfg.GetResult{Schema: configSchema(), Values: masked, Source: cfg.source}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, &pluginapi.Error{Code: pluginapi.E_INTERNAL, Message: "序列化 config.get 结果失败: " + err.Error()}
	}
	return encoded, nil
}

// configSet 按「校验 → 原子写 → 更新内存 → 返回 applied」顺序保存配置（PLUGIN-CONFIG §3）。
func configSet(raw json.RawMessage, cfg *config) (json.RawMessage, *pluginapi.Error) {
	var params plugincfg.SetParams
	if len(raw) == 0 || string(raw) == "null" {
		return nil, &pluginapi.Error{Code: pluginapi.E_INVALID, Message: "缺少必填字段 values"}
	}
	if raw[0] != '{' {
		return nil, &pluginapi.Error{Code: pluginapi.E_INVALID, Message: "params 必须是 JSON 对象"}
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, &pluginapi.Error{Code: pluginapi.E_INVALID, Message: "params 解析失败: " + err.Error()}
	}
	if len(params.Values) == 0 || string(params.Values) == "null" {
		return nil, &pluginapi.Error{Code: pluginapi.E_INVALID, Message: "缺少必填字段 values"}
	}
	if params.Values[0] != '{' {
		return nil, &pluginapi.Error{Code: pluginapi.E_INVALID, Message: "values 必须是 JSON 对象"}
	}
	// 以当前值为基线做局部更新：未提供的字段保持原值。
	candidate := cfg.values
	if err := json.Unmarshal(params.Values, &candidate); err != nil {
		return nil, &pluginapi.Error{Code: pluginapi.E_INVALID, Message: "values 解析失败: " + err.Error()}
	}
	normalized, perr := validateValues(candidate)
	if perr != nil {
		return nil, perr
	}
	if cfg.store == nil || cfg.store.Path() == "" {
		return nil, &pluginapi.Error{
			Code:    pluginapi.E_INTERNAL,
			Message: "KORE_STATE_DIR 未设置，无法保存插件配置",
			Detail:  json.RawMessage(`{"hint":"插件由内核启动时会注入 KORE_STATE_DIR；本地单独调试请先设置该环境变量。"}`),
		}
	}
	payload, marshalErr := json.Marshal(normalized)
	if marshalErr != nil {
		return nil, &pluginapi.Error{Code: pluginapi.E_INTERNAL, Message: "序列化配置失败: " + marshalErr.Error()}
	}
	if writeErr := cfg.store.Write(payload); writeErr != nil {
		return nil, &pluginapi.Error{
			Code:    pluginapi.E_INTERNAL,
			Message: "保存插件配置失败: " + writeErr.Error(),
			Detail:  json.RawMessage(`{"hint":"检查 KORE_STATE_DIR 是否可写（目录需 0700 且属主为运行用户）。"}`),
		}
	}
	cfg.values = normalized
	cfg.source = plugincfg.SourceFile
	result := plugincfg.SetResult{
		Saved: true, Applied: true, RestartRequired: false,
		Warnings: []string{"配置已保存并立即生效：下一次 monitor.* 调用即使用新值，无需重启 kored 或插件进程。"},
	}
	encoded, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		return nil, &pluginapi.Error{Code: pluginapi.E_INTERNAL, Message: "序列化 config.set 结果失败: " + marshalErr.Error()}
	}
	return encoded, nil
}

// applyConfigDefaults 把已保存的配置注入业务入参：请求未显式提供的字段取插件配置值。
// 这样 config.set 后下一次调用即用新值（PLUGIN-CONFIG §5/§10），不引入任何后台轮询（G-19）。
func applyConfigDefaults(method string, raw json.RawMessage, cfg *config) json.RawMessage {
	fields, ok := configDefaultsFor(method, cfg)
	if !ok {
		return raw
	}
	// 只对合法 JSON 对象注入默认值；null/空/非对象原样交回底层校验，
	// 避免把 "x" 这类非法入参悄悄规整成 {} 而绕过 E_INVALID（回归防护）。
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed[0] != '{' || !json.Valid(raw) {
		return raw
	}
	obj := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw
	}
	for key, value := range fields {
		if _, exists := obj[key]; !exists {
			obj[key] = value
		}
	}
	merged, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return merged
}

// configDefaultsFor 返回每种业务方法的可注入默认字段（只列该方法真实消费的键）。
func configDefaultsFor(method string, cfg *config) (map[string]json.RawMessage, bool) {
	v := cfg.values
	switch method {
	case "monitor.system", "system":
		thresholds, _ := json.Marshal(map[string]int{
			"cpu_percent": v.CPUThreshold, "memory_percent": v.MemoryThreshold, "disk_percent": v.DiskThreshold,
		})
		return map[string]json.RawMessage{
			"history_points":     mustJSON(v.HistoryPoints),
			"history_interval_s": mustJSON(v.HistoryIntervalS),
			"thresholds":         thresholds,
		}, true
	case "monitor.processes", "processes":
		return map[string]json.RawMessage{
			"limit": mustJSON(v.ProcessLimit),
			"sort":  mustJSON(v.ProcessSort),
		}, true
	default:
		return nil, false
	}
}

// sampleInterval 返回当前的请求内采样间隔（config.set 热更新后下一次调用即生效）。
func sampleInterval(cfg *config) time.Duration {
	return time.Duration(cfg.values.SampleIntervalMS) * time.Millisecond
}

// mustJSON 把标量编成 json.RawMessage（schema 的 default 字段）。
func mustJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return encoded
}

// intPtr 返回整数指针（schema 的 min/max）。
func intPtr(value int) *int { return &value }
