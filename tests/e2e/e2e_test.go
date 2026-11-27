// Package e2e 在本机 Linux 上对 monitor 插件做真实 /proc 端到端验证：
// 真实 CPU 差分、内存、负载、磁盘挂载点、网卡、进程 top N、thermal，以及单次调用耗时。
package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pengtang1024/kore-plugin-monitor/internal/monitor"
)

// call 调用一个方法并解码结果，返回本次调用耗时。
func call(t *testing.T, method, params string, out any) time.Duration {
	t.Helper()
	start := time.Now()
	raw, perr := monitor.Call(method, json.RawMessage(params), monitor.NewExecutor(), monitor.Context{})
	elapsed := time.Since(start)
	if perr != nil {
		t.Fatalf("%s 失败: code=%s message=%s", method, perr.Code, perr.Message)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("%s 结果解码失败: %v（原始=%s）", method, err, raw)
	}
	return elapsed
}

// requireLinux 在非 Linux 上跳过真实 /proc 用例（实现本身会返回 E_UNSUPPORTED）。
func requireLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("真实 /proc 采集只在 Linux 上验证")
	}
}

func TestSystemReal(t *testing.T) {
	requireLinux(t)
	var result monitor.SystemResult
	elapsed := call(t, "monitor.system", `{"per_cpu":true}`, &result)
	if result.Schema != "monitor.system.v1" || result.Source != "proc" {
		t.Fatalf("schema/source 不符: %+v", result)
	}
	if result.CPU.UsagePercent < 0 || result.CPU.UsagePercent > 100 {
		t.Fatalf("CPU 使用率越界: %v", result.CPU.UsagePercent)
	}
	if result.CPU.Cores <= 0 || len(result.CPU.PerCPU) != result.CPU.Cores {
		t.Fatalf("分核读数不完整: cores=%d per_cpu=%d", result.CPU.Cores, len(result.CPU.PerCPU))
	}
	if result.Memory.Total <= 0 || result.Memory.Used <= 0 || result.Memory.UsedHuman == "" {
		t.Fatalf("内存字段无效: %+v", result.Memory)
	}
	if result.Uptime.UptimeS <= 0 || result.Load.LastPID <= 0 || result.Load.Total <= 0 {
		t.Fatalf("uptime/load 字段无效: %+v %+v", result.Uptime, result.Load)
	}
	if len(result.Disks) == 0 {
		t.Fatalf("未发现任何真实块设备挂载点: %+v", result.Warnings)
	}
	root := false
	for _, disk := range result.Disks {
		if disk.Mount == "/" && disk.Total > 0 && disk.FSType != "" {
			root = true
			break
		}
	}
	if !root {
		t.Fatalf("磁盘列表缺少有效的 / 挂载点: %+v", result.Disks)
	}
	if len(result.Network) == 0 {
		t.Fatalf("未发现真实网卡（已排除 lo）: %+v", result.Warnings)
	}
	if elapsed > time.Second {
		t.Fatalf("monitor.system 耗时 %s 超过 1s", elapsed)
	}
	t.Logf("CPU=%.2f%% cores=%d mem=%s/%s load=%.2f/%.2f/%.2f uptime=%ds disks=%d net=%d elapsed=%s",
		result.CPU.UsagePercent, result.CPU.Cores, result.Memory.UsedHuman, result.Memory.TotalHuman,
		result.Load.Load1, result.Load.Load5, result.Load.Load15, result.Uptime.UptimeS,
		len(result.Disks), len(result.Network), elapsed)
	for _, disk := range result.Disks {
		t.Logf("disk mount=%s fs=%s used=%s/%s (%.2f%%)", disk.Mount, disk.FSType, disk.UsedHuman, disk.TotalHuman, disk.UsedPercent)
	}
	for _, nic := range result.Network {
		t.Logf("net=%s rx=%s tx=%s", nic.Name, nic.RXHuman, nic.TXHuman)
	}
}

// TestSampleAppends 是 monitor.sample 的端到端落点断言（方案 §3.4 / P1 判据②）：
// 每次调用都是真实 /proc 采样（薄包装复用 System，无后台定时器，G-19）；历史间隔 1s，
// 连续快调不落点、跨过间隔后落点，证明 scheduler interval_s 必须 ≥ history_interval_s。
// KORE_STATE_DIR 指向 t.TempDir()（G-29：不碰运行态 history.json）。
func TestSampleAppends(t *testing.T) {
	requireLinux(t)
	t.Setenv("KORE_STATE_DIR", t.TempDir())
	params := `{"history_points":10,"history_interval_s":1}`

	var first monitor.SampleResult
	call(t, "monitor.sample", params, &first)
	if first.Schema != "monitor.sample.v1" || first.Source != "proc" {
		t.Fatalf("schema/source 不符: %+v", first)
	}
	if !first.Appended || first.Appended != first.History.Appended || first.History.Count != 1 {
		t.Fatalf("首次调用应落 1 点: appended=%v history=%+v", first.Appended, first.History)
	}
	if first.History.IntervalS != 1 || first.History.Capacity != 10 {
		t.Fatalf("历史参数未按 sample 入参生效: interval=%d capacity=%d", first.History.IntervalS, first.History.Capacity)
	}

	// 立刻再调（间隔未到）→ appended=false，点数不增长。
	var second monitor.SampleResult
	call(t, "monitor.sample", params, &second)
	if second.Appended || second.History.Count != first.History.Count {
		t.Fatalf("未到间隔不应落点: appended=%v count=%d（首次 %d）", second.Appended, second.History.Count, first.History.Count)
	}

	// 跨过 1s 间隔再调 → appended=true，点数增长（scheduler 周期 ≥ 间隔才有意义）。
	time.Sleep(1100 * time.Millisecond)
	var third monitor.SampleResult
	call(t, "monitor.sample", params, &third)
	if !third.Appended || third.History.Count <= second.History.Count {
		t.Fatalf("跨间隔后应落点并增长: appended=%v count=%d（前 %d）", third.Appended, third.History.Count, second.History.Count)
	}
	t.Logf("sample 落点序列: 首点 count=%d → 未到间隔 count=%d(appended=%v) → 跨间隔 count=%d(appended=%v)",
		first.History.Count, second.History.Count, second.Appended, third.History.Count, third.Appended)
}

func TestSystemCPUVaries(t *testing.T) {
	requireLinux(t)
	var samples []float64
	for attempt := 0; attempt < 3; attempt++ {
		var first, second monitor.SystemResult
		call(t, "monitor.system", `{}`, &first)
		call(t, "monitor.system", `{}`, &second)
		if first.CPU.UsagePercent != second.CPU.UsagePercent {
			t.Logf("两次连续调用 CPU%% 不同（第一次采样）：%v → %v", first.CPU.UsagePercent, second.CPU.UsagePercent)
			return
		}
		samples = append(samples, first.CPU.UsagePercent, second.CPU.UsagePercent)
	}
	t.Fatalf("3 组连续调用 CPU%% 完全相同 %v，无法证明是真实采样", samples)
}

func TestProcessesReal(t *testing.T) {
	requireLinux(t)
	var result monitor.ProcessesResult
	elapsed := call(t, "monitor.processes", `{"limit":5,"sort":"cpu"}`, &result)
	if result.Count != 5 || result.Total < 5 || len(result.Items) != 5 {
		t.Fatalf("期望 5 个进程: count=%d total=%d items=%d", result.Count, result.Total, len(result.Items))
	}
	koreFound := false
	for _, item := range result.Items {
		if item.PID <= 0 || item.Comm == "" || item.Threads < 1 {
			t.Fatalf("进程字段无效: %+v", item)
		}
		if item.CPUPercent < 0 || item.MemRSSBytes < 0 {
			t.Fatalf("进程 CPU/内存为负: %+v", item)
		}
		if strings.Contains(item.Comm, "kore") {
			koreFound = true
		}
		t.Logf("pid=%d comm=%s state=%s cpu=%.2f%% rss=%s threads=%d", item.PID, item.Comm, item.State, item.CPUPercent, item.MemRSSHuman, item.Threads)
	}
	hostKore := hostHasKoreProcess(t)
	var all monitor.ProcessesResult
	call(t, "monitor.processes", `{"limit":500,"sort":"mem"}`, &all)
	for _, item := range all.Items {
		if strings.HasPrefix(item.Comm, "kore") {
			koreFound = true
			t.Logf("全量扫描命中 kore 进程：pid=%d comm=%s rss=%s threads=%d", item.PID, item.Comm, item.MemRSSHuman, item.Threads)
		}
	}
	if hostKore && !koreFound {
		t.Fatalf("宿主机存在 kore* 进程，但插件全量扫描未命中，说明进程枚举不完整")
	}
	if !hostKore {
		t.Logf("本机未运行 kore* 进程（宿主是否运行 kored 取决于环境，不作为失败条件）")
	}
	if result.SampleIntervalMS < 100 || result.SampleIntervalMS > 1000 {
		t.Fatalf("采样间隔异常: %dms", result.SampleIntervalMS)
	}
	if elapsed > time.Second {
		t.Fatalf("monitor.processes 耗时 %s 超过 1s", elapsed)
	}
}

func TestDisksReal(t *testing.T) {
	requireLinux(t)
	var result monitor.DisksResult
	call(t, "monitor.disks", `{}`, &result)
	if result.Count == 0 {
		t.Fatalf("未发现磁盘: %+v", result.Warnings)
	}
	hasInodes := false
	for _, disk := range result.Items {
		if disk.InodesTotal > 0 && disk.InodesUsed >= 0 && disk.InodesFree >= 0 {
			hasInodes = true
		}
		if disk.Reads < 0 || disk.Writes < 0 || disk.ReadBytes < 0 {
			t.Fatalf("读写统计为负: %+v", disk)
		}
		t.Logf("mount=%s dev=%s inodes=%d/%d (%.2f%%) read=%s write=%s reads=%d writes=%d",
			disk.Mount, disk.Device, disk.InodesUsed, disk.InodesTotal, disk.InodesUsedPercent,
			disk.ReadBytesHuman, disk.WriteBytesHuman, disk.Reads, disk.Writes)
	}
	if !hasInodes {
		t.Fatalf("所有条目都没有 inode 数据: %+v", result.Items)
	}
	var single monitor.DisksResult
	call(t, "monitor.disks", `{"path":"/"}`, &single)
	if single.Count != 1 || single.Items[0].Mount != "/" {
		t.Fatalf("path=/ 应只返回 / 挂载点: %+v", single.Items)
	}
}

func TestTemperatureReal(t *testing.T) {
	requireLinux(t)
	var result monitor.TemperatureResult
	call(t, "monitor.temperature", `{}`, &result)
	if result.Available {
		if result.Count == 0 || len(result.Zones) == 0 {
			t.Fatalf("available=true 但 zones 为空: %+v", result)
		}
		for _, zone := range result.Zones {
			if zone.Zone == "" || zone.TempC < -100 || zone.TempC > 200 {
				t.Fatalf("thermal zone 读数异常: %+v", zone)
			}
			t.Logf("zone=%s type=%s temp=%.2fC", zone.Zone, zone.Type, zone.TempC)
		}
		return
	}
	if len(result.Zones) != 0 || result.Count != 0 {
		t.Fatalf("available=false 时应返回空数组: %+v", result)
	}
	t.Logf("本机无 thermal zone，按约定返回 available=false + 空数组（不报错）")
}

func TestSystemRSSIncrement(t *testing.T) {
	requireLinux(t)
	// 预热一次，避免首次调用的一次性分配计入增量。
	call(t, "monitor.system", `{}`, &struct{}{})
	runtime.GC()
	before := readVmRSS(t)
	var result monitor.SystemResult
	call(t, "monitor.system", `{}`, &result)
	after := readVmRSS(t)
	delta := after - before
	t.Logf("monitor.system 单次 RSS: %d KiB → %d KiB（Δ=%d KiB）", before, after, delta)
	if delta > 2048 {
		t.Fatalf("monitor.system 单次 RSS 增量 %d KiB 超过 2 MiB", delta)
	}
}

// hostHasKoreProcess 独立扫描 /proc/*/comm，判断宿主机是否存在 kore* 进程，
// 作为插件进程枚举的 ground truth（仅在存在时断言插件必须也扫到）。
func hostHasKoreProcess(t *testing.T) bool {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		comm, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "comm"))
		if err != nil {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(string(comm)), "kore") {
			return true
		}
	}
	return false
}

// readVmRSS 从 /proc/self/status 读取本进程当前 VmRSS（KiB）。
func readVmRSS(t *testing.T) int64 {
	t.Helper()
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Fatalf("读取 /proc/self/status 失败: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "VmRSS:"))
		if len(fields) == 0 {
			break
		}
		value, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			t.Fatalf("解析 VmRSS 失败: %v", err)
		}
		return value
	}
	t.Fatal("未找到 VmRSS")
	return 0
}
