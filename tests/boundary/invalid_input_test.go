// 异常输入（invalid input）类边界：
//   - 非法采集参数 / 负数与超范围采样间隔 → 协议错误 E_INVALID，且错误信息带字段名；
//   - 未知方法名 / 未知排序指标名 → E_NOTFOUND / E_INVALID；
//   - 格式错误的 /proc fixture → 不 panic，按字段降级并给出 warning；
//   - 已过期的请求 deadline → E_UPSTREAM，不进入采集。
package boundary

import (
	"strings"
	"testing"
	"time"

	"github.com/pengtang1024/kore-plugin-monitor/internal/monitor"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// TestInvalidParamsRejected 覆盖参数校验的所有拒绝分支：断言具体错误码 +
// 字段名关键词；校验被删除时会返回成功结果，本用例必然 FAIL。
func TestInvalidParamsRejected(t *testing.T) {
	t.Parallel()
	exec := newFixtureCollector(t)
	cases := []struct {
		name        string
		method      string
		params      string
		wantCode    string
		wantMessage string
	}{
		{"history_points 低于下界", "monitor.system", `{"history_points":1}`, pluginapi.E_INVALID, "history_points"},
		{"history_points 高于上界", "monitor.system", `{"history_points":721}`, pluginapi.E_INVALID, "history_points"},
		{"history_interval_s 为零", "monitor.system", `{"history_interval_s":0}`, pluginapi.E_INVALID, "history_interval_s"},
		{"history_interval_s 为负", "monitor.system", `{"history_interval_s":-5}`, pluginapi.E_INVALID, "history_interval_s"},
		{"history_interval_s 超上界", "monitor.system", `{"history_interval_s":3601}`, pluginapi.E_INVALID, "history_interval_s"},
		{"阈值超 100", "monitor.system", `{"thresholds":{"cpu_percent":100.5}}`, pluginapi.E_INVALID, "cpu_percent"},
		{"阈值为负", "monitor.system", `{"thresholds":{"memory_percent":-0.1}}`, pluginapi.E_INVALID, "memory_percent"},
		{"参数是数组", "monitor.system", `[]`, pluginapi.E_INVALID, "JSON 对象"},
		{"参数 JSON 残缺", "monitor.system", `{"history_points":}`, pluginapi.E_INVALID, "参数解析失败"},
		{"参数类型错误", "monitor.system", `{"history_points":"60"}`, pluginapi.E_INVALID, "参数解析失败"},
		{"limit 为零", "monitor.processes", `{"limit":0}`, pluginapi.E_INVALID, "limit"},
		{"limit 超上界", "monitor.processes", `{"limit":501}`, pluginapi.E_INVALID, "limit"},
		{"limit 为负", "monitor.processes", `{"limit":-3}`, pluginapi.E_INVALID, "limit"},
		{"未知排序指标名", "monitor.processes", `{"sort":"disk"}`, pluginapi.E_INVALID, "sort"},
		{"disks 相对路径", "monitor.disks", `{"path":"relative/dir"}`, pluginapi.E_INVALID, "绝对路径"},
		{"disks 路径含 NUL", "monitor.disks", `{"path":"/tmp/\u0000bad"}`, pluginapi.E_INVALID, "非法字符"},
		{"温度参数不是对象", "monitor.temperature", `"x"`, pluginapi.E_INVALID, "JSON 对象"},
		{"未知方法名", "monitor.gpu", `{}`, pluginapi.E_NOTFOUND, "未知方法"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			raw, perr := callRaw(t, tc.method, tc.params, exec, shortCtx())
			if perr == nil {
				t.Fatalf("%s(%s) 未返回协议错误，结果=%s", tc.method, tc.params, raw)
			}
			if raw != nil {
				t.Fatalf("%s 出错时不应带结果: %s", tc.method, raw)
			}
			if perr.Code != tc.wantCode {
				t.Fatalf("%s 错误码=%q，期望 %q（message=%q）", tc.method, perr.Code, tc.wantCode, perr.Message)
			}
			if !strings.Contains(perr.Message, tc.wantMessage) {
				t.Fatalf("%s 错误信息 %q 未包含字段/原因 %q", tc.method, perr.Message, tc.wantMessage)
			}
		})
	}
}

// TestBoundaryParamsAccepted 反向验证合法边界值不被误杀（下界 / 上界 / 大小写与空白）。
func TestBoundaryParamsAccepted(t *testing.T) {
	t.Parallel()
	exec := newFixtureCollector(t)
	cases := []struct {
		name   string
		method string
		params string
		schema string
	}{
		{"history_points 下界", "monitor.system", `{"history_points":2,"history_interval_s":1}`, "monitor.system.v1"},
		{"history_points 上界", "monitor.system", `{"history_points":720,"history_interval_s":3600}`, "monitor.system.v1"},
		{"阈值关闭与满值", "monitor.system", `{"thresholds":{"cpu_percent":0,"memory_percent":100,"disk_percent":0}}`, "monitor.system.v1"},
		{"缺省参数 null", "monitor.system", `null`, "monitor.system.v1"},
		{"limit 下界", "monitor.processes", `{"limit":1}`, "monitor.processes.v1"},
		{"limit 上界", "monitor.processes", `{"limit":500}`, "monitor.processes.v1"},
		{"sort 大小写与空白", "monitor.processes", `{"sort":"  MEM "}`, "monitor.processes.v1"},
		{"disks 根路径", "monitor.disks", `{"path":"/"}`, "monitor.disks.v1"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			raw, perr := callRaw(t, tc.method, tc.params, exec, shortCtx())
			if perr != nil {
				t.Fatalf("%s(%s) 合法边界值被拒绝: code=%s message=%s", tc.method, tc.params, perr.Code, perr.Message)
			}
			var probe struct {
				Schema string `json:"schema"`
			}
			decodeInto(t, raw, &probe)
			if probe.Schema != tc.schema {
				t.Fatalf("%s(%s) schema=%q，期望 %q", tc.method, tc.params, probe.Schema, tc.schema)
			}
		})
	}
}

// TestExpiredDeadlineRejected 覆盖"请求已过期"这一非法请求状态：必须在进入采集前失败。
func TestExpiredDeadlineRejected(t *testing.T) {
	t.Parallel()
	exec := newFixtureCollector(t)
	ctx := monitor.Context{Deadline: time.Now().Add(-time.Millisecond).UnixNano()}
	raw, perr := callRaw(t, "monitor.system", `{}`, exec, ctx)
	if perr == nil {
		t.Fatalf("过期 deadline 未返回错误，结果=%s", raw)
	}
	if perr.Code != pluginapi.E_UPSTREAM {
		t.Fatalf("过期 deadline 错误码=%q，期望 E_UPSTREAM（message=%q）", perr.Code, perr.Message)
	}
	if !strings.Contains(perr.Message, "deadline") {
		t.Fatalf("错误信息 %q 未说明 deadline", perr.Message)
	}
}

// TestMalformedProcContentDegrades 注入格式错误的 /proc 内容：
// 解析失败必须降级为 warning + 零值，而不是 panic 或整次调用失败。
func TestMalformedProcContentDegrades(t *testing.T) {
	t.Parallel()
	procRoot, sysRoot := t.TempDir(), t.TempDir()
	writeTree(t, procRoot, map[string]string{
		"stat":      "this is not a cpu line at all\n",
		"meminfo":   "MemTotal: not-a-number kB\nMemAvailable: also-bad kB\n",
		"loadavg":   "x y z\n",
		"uptime":    "x y\n",
		"mounts":    "bogus mount line\n",
		"diskstats": "short\n",
		"net/dev":   "eth0: 1 2 3\n",
	})
	collector := monitor.NewCollector(procRoot, sysRoot)
	result, err := collector.System(shortCtx(), monitor.SystemParams{})
	if err != nil {
		t.Fatalf("格式错误的 /proc 不应让整次调用失败: %v", err)
	}
	assertSystemShape(t, result)
	if result.CPU.UsagePercent != 0 || result.CPU.Cores != 0 {
		t.Fatalf("stat 不可解析时 CPU 应为零值，得到 %+v", result.CPU)
	}
	if result.Memory.Total != 0 {
		t.Fatalf("meminfo 不可解析时应降级为 0，得到 %d", result.Memory.Total)
	}
	if result.Load.Load1 != 0 || result.Uptime.UptimeS != 0 {
		t.Fatalf("loadavg/uptime 不可解析时应降级为 0: %+v %+v", result.Load, result.Uptime)
	}
	if len(result.Disks) != 0 || len(result.Network) != 0 {
		t.Fatalf("mounts/net 不可解析时应返回空数组: disks=%v network=%v", result.Disks, result.Network)
	}
	requireWarning(t, result.Warnings, "读取 /proc/stat 失败")
	requireWarning(t, result.Warnings, "解析 /proc/loadavg 失败")
	requireWarning(t, result.Warnings, "解析 /proc/uptime 失败")
	requireWarning(t, result.Warnings, "解析 /proc/net/dev 失败")
}

// TestMalformedProcStatSkippedWithWarning 覆盖进程扫描的坏数据：
// stat 格式非法的进程被跳过并计数，缺 status 的进程记 0 字节，二者都必须有 warning。
func TestMalformedProcStatSkippedWithWarning(t *testing.T) {
	t.Parallel()
	procRoot, sysRoot := newFixture(t)
	writeTree(t, procRoot, map[string]string{
		"9999/stat": "9999 (broken no close paren\n",
		"7777/stat": "7777 (no-status) S 1 1 1 0 -1 4194560 1 0 0 0 1 2 0 0 20 0 1 0 1 0\n",
	})
	collector := monitor.NewCollector(procRoot, sysRoot)
	result, err := collector.Processes(shortCtx(), monitor.ProcessParams{Limit: 10, Sort: "cpu"})
	if err != nil {
		t.Fatalf("单个进程数据非法不应让整次扫描失败: %v", err)
	}
	if result.Schema != "monitor.processes.v1" {
		t.Fatalf("schema 不符: %q", result.Schema)
	}
	if result.Total != 3 || len(result.Items) != 3 {
		t.Fatalf("应从 3 个可解析进程出结果（1234/5678/7777），得到 total=%d items=%d", result.Total, len(result.Items))
	}
	requireWarning(t, result.Warnings, "已跳过")
	requireWarning(t, result.Warnings, "status 不可读")
}

// TestMalformedThermalSkippedWithWarning 覆盖 /sys 温度文件的坏数据与缺文件。
func TestMalformedThermalSkippedWithWarning(t *testing.T) {
	t.Parallel()
	sysRoot := t.TempDir()
	writeTree(t, sysRoot, map[string]string{
		"class/thermal/thermal_zone0/temp": "not-a-number\n",
		"class/thermal/thermal_zone1/type": "x86_pkg_temp\n",
	})
	collector := monitor.NewCollector(t.TempDir(), sysRoot)
	result, err := collector.Temperature(shortCtx())
	if err != nil {
		t.Fatalf("非法 thermal 数据不应让调用失败: %v", err)
	}
	if result.Schema != "monitor.temperature.v1" {
		t.Fatalf("schema 不符: %q", result.Schema)
	}
	if result.Available || result.Count != 0 {
		t.Fatalf("两个 zone 都不可用时应为 available=false count=0: %+v", result)
	}
	requireWarning(t, result.Warnings, "解析失败")
	requireWarning(t, result.Warnings, "读取失败")
}
