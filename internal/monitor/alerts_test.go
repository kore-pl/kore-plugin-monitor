package monitor

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEvaluateAlertsBoundaries 覆盖「正好等于 / 超出 / 远离」阈值与多指标同时超限。
func TestEvaluateAlertsBoundaries(t *testing.T) {
	cases := []struct {
		name      string
		cpu       float64
		memory    float64
		disk      float64
		threshold Thresholds
		want      []string // 期望告警的 metric 顺序
		wantLevel string
	}{
		{"正好等于阈值", 90, 0, 0, Thresholds{CPUPercent: 90}, []string{"cpu"}, "warning"},
		{"超出 10 个百分点为 critical", 100, 0, 0, Thresholds{CPUPercent: 90}, []string{"cpu"}, "critical"},
		{"远离阈值无告警", 50, 20, 30, Thresholds{CPUPercent: 90, MemoryPercent: 90, DiskPercent: 90}, nil, ""},
		{"阈值 0 表示关闭", 100, 100, 100, Thresholds{CPUPercent: 0, MemoryPercent: 0, DiskPercent: 0}, nil, ""},
		{"多指标同时超限按固定顺序", 95, 96, 97, Thresholds{CPUPercent: 90, MemoryPercent: 90, DiskPercent: 90}, []string{"cpu", "memory", "disk"}, "warning"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			alerts := evaluateAlerts(tc.cpu, tc.memory, tc.disk, "/", tc.threshold)
			if len(alerts) != len(tc.want) {
				t.Fatalf("告警数=%d，期望 %d（%+v）", len(alerts), len(tc.want), alerts)
			}
			for i, metric := range tc.want {
				if alerts[i].Metric != metric {
					t.Fatalf("第 %d 条 metric=%q，期望 %q", i, alerts[i].Metric, metric)
				}
				if alerts[i].Level != tc.wantLevel {
					t.Fatalf("%s 级别=%q，期望 %q", metric, alerts[i].Level, tc.wantLevel)
				}
				if alerts[i].Threshold <= 0 || alerts[i].Current < alerts[i].Threshold {
					t.Fatalf("%s 告警字段不合法: %+v", metric, alerts[i])
				}
				if !strings.Contains(alerts[i].Hint, "超过阈值") || !strings.Contains(alerts[i].Hint, "monitor.") {
					t.Fatalf("%s hint 不具可操作性: %q", metric, alerts[i].Hint)
				}
			}
		})
	}
}

// TestEvaluateAlertsDiskLabel 验证磁盘告警带上具体挂载点，hint 指向 monitor.disks。
func TestEvaluateAlertsDiskLabel(t *testing.T) {
	alerts := evaluateAlerts(0, 0, 95, "/var", Thresholds{DiskPercent: 90})
	if len(alerts) != 1 || alerts[0].Metric != "disk" {
		t.Fatalf("期望 1 条磁盘告警: %+v", alerts)
	}
	if !strings.Contains(alerts[0].Hint, "/var") || !strings.Contains(alerts[0].Hint, "monitor.disks") {
		t.Fatalf("磁盘 hint 未带挂载点/处置入口: %q", alerts[0].Hint)
	}
}

// TestParseSystemParamsDefaultsAndThresholds 验证历史参数与阈值合并的显式语义。
func TestParseSystemParamsDefaultsAndThresholds(t *testing.T) {
	params, err := parseSystemParams(json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("空参数解析失败: %v", err)
	}
	if params.HistoryPoints != DefaultHistoryPoints || params.HistoryIntervalS != DefaultHistoryInterval {
		t.Fatalf("历史参数默认值错误: %+v", params)
	}
	if params.Thresholds != defaultThresholds() {
		t.Fatalf("阈值默认值应为 90/90/90: %+v", params.Thresholds)
	}

	params, err = parseSystemParams(json.RawMessage(
		`{"per_cpu":true,"history_points":120,"history_interval_s":30,` +
			`"thresholds":{"memory_percent":75,"disk_percent":0}}`))
	if err != nil {
		t.Fatalf("显式参数解析失败: %v", err)
	}
	if !params.PerCPU || params.HistoryPoints != 120 || params.HistoryIntervalS != 30 {
		t.Fatalf("显式参数未生效: %+v", params)
	}
	want := Thresholds{CPUPercent: DefaultCPUThreshold, MemoryPercent: 75, DiskPercent: 0}
	if params.Thresholds != want {
		t.Fatalf("阈值合并错误: got %+v want %+v", params.Thresholds, want)
	}
}

// TestParseSystemParamsInvalid 验证越界参数一律 E_INVALID（fail-closed）。
func TestParseSystemParamsInvalid(t *testing.T) {
	cases := []struct{ name, params, want string }{
		{"history_points 过小", `{"history_points":1}`, "history_points"},
		{"history_points 过大", `{"history_points":721}`, "history_points"},
		{"history_interval_s 过小", `{"history_interval_s":0}`, "history_interval_s"},
		{"history_interval_s 过大", `{"history_interval_s":3601}`, "history_interval_s"},
		{"阈值负数", `{"thresholds":{"cpu_percent":-1}}`, "cpu_percent"},
		{"阈值超 100", `{"thresholds":{"disk_percent":101}}`, "disk_percent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseSystemParams(json.RawMessage(tc.params)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("params=%s err=%v，期望拒绝并提及 %q", tc.params, err, tc.want)
			}
		})
	}
}

// TestSystemAlertsIntegration 用固定 meminfo 验证阈值边界（等于即告警）与默认关闭路径。
func TestSystemAlertsIntegration(t *testing.T) {
	collector := fixtureCollector(t, map[string]string{
		"meminfo": "MemTotal: 1000 kB\nMemAvailable: 100 kB\n", // used=90%
	}, nil)
	result, err := collector.System(Context{}, SystemParams{Thresholds: Thresholds{MemoryPercent: 90}})
	if err != nil {
		t.Fatalf("system 失败: %v", err)
	}
	if len(result.Alerts) != 1 || result.Alerts[0].Metric != "memory" {
		t.Fatalf("内存 90%% 正好等于阈值 90%% 应告警: %+v", result.Alerts)
	}
	if result.Alerts[0].Current != 90 || result.Alerts[0].Threshold != 90 || result.Alerts[0].Level != "warning" {
		t.Fatalf("告警字段错误: %+v", result.Alerts[0])
	}

	result, err = collector.System(Context{}, SystemParams{Thresholds: Thresholds{MemoryPercent: 90.01}})
	if err != nil {
		t.Fatalf("system 失败: %v", err)
	}
	if len(result.Alerts) != 0 {
		t.Fatalf("90%% 未达到 90.01%% 不应告警: %+v", result.Alerts)
	}
}

// TestSystemNoAlertsJSONIsEmptyArray 验证无告警时 alerts 序列化为 [] 而不是 null。
func TestSystemNoAlertsJSONIsEmptyArray(t *testing.T) {
	collector := fixtureCollector(t, map[string]string{
		"meminfo": "MemTotal: 1000 kB\nMemAvailable: 300 kB\n", // used=70%
	}, nil)
	result, err := collector.System(Context{}, SystemParams{})
	if err != nil {
		t.Fatalf("system 失败: %v", err)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if !strings.Contains(string(payload), `"alerts":[]`) {
		t.Fatalf("无告警时应输出空数组: %s", payload)
	}
}
