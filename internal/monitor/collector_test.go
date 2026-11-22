package monitor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixtureCollector 构造指向临时目录的采集器，并把采样窗缩到 5ms，避免单测变慢。
func fixtureCollector(t *testing.T, procFiles, sysFiles map[string]string) *Collector {
	t.Helper()
	procRoot := t.TempDir()
	sysRoot := t.TempDir()
	writeFixtures(t, procRoot, procFiles)
	writeFixtures(t, sysRoot, sysFiles)
	collector := NewCollector(procRoot, sysRoot)
	collector.interval = 5 * time.Millisecond
	return collector
}

// writeFixtures 在 root 下写入 name→content，自动创建父目录。
func writeFixtures(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("创建目录失败: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("写入 fixture %s 失败: %v", name, err)
		}
	}
}

func TestSystemPartialWarnings(t *testing.T) {
	// 只给 meminfo / loadavg / uptime，缺 stat/mounts/net/dev：应部分成功 + warnings。
	collector := fixtureCollector(t, map[string]string{
		"meminfo": "MemTotal: 1000 kB\nMemAvailable: 400 kB\n",
		"loadavg": "0.10 0.20 0.30 1/100 999\n",
		"uptime":  "100.5 200.5\n",
	}, nil)
	result, err := collector.System(Context{}, SystemParams{})
	if err != nil {
		t.Fatalf("部分文件缺失不应让调用失败: %v", err)
	}
	if result.Memory.Total != 1000*1024 || result.Memory.Used != 600*1024 {
		t.Fatalf("内存字段错误: %+v", result.Memory)
	}
	if result.Load.Load1 != 0.10 || result.Uptime.UptimeS != 100 {
		t.Fatalf("负载/uptime 字段错误: %+v %+v", result.Load, result.Uptime)
	}
	if result.CPU.UsagePercent != 0 || result.CPU.Cores != 0 {
		t.Fatalf("缺 /proc/stat 时 CPU 应为零值: %+v", result.CPU)
	}
	if len(result.Disks) != 0 || len(result.Network) != 0 {
		t.Fatalf("缺 /proc/mounts 与 net/dev 时应返回空数组: %+v %+v", result.Disks, result.Network)
	}
	if len(result.Warnings) < 3 {
		t.Fatalf("应至少 3 条 warning（stat/mounts/net/dev），得到 %v", result.Warnings)
	}
}

func TestSystemFixtureCPUAndNetwork(t *testing.T) {
	stat := "cpu  10 0 10 80 0 0 0 0 0 0\ncpu0 5 0 5 40 0 0 0 0 0 0\ncpu1 5 0 5 40 0 0 0 0 0 0\n"
	collector := fixtureCollector(t, map[string]string{
		"stat":    stat,
		"meminfo": "MemTotal: 2048 kB\nMemAvailable: 1024 kB\n",
		"loadavg": "0.00 0.00 0.00 1/1 1\n",
		"uptime":  "10.0 5.0\n",
		"net/dev": "Inter-| Receive | Transmit\n face |bytes packets errs drop fifo frame compressed multicast|bytes packets errs drop fifo colls carrier compressed\n" +
			"  eth0: 100 1 0 0 0 0 0 0 200 2 0 0 0 0 0 0\n",
	}, nil)
	result, err := collector.System(Context{}, SystemParams{PerCPU: true})
	if err != nil {
		t.Fatalf("system 失败: %v", err)
	}
	if result.CPU.Cores != 2 || len(result.CPU.PerCPU) != 2 || result.CPU.PerCPU[0].Name != "cpu0" {
		t.Fatalf("分核读数错误: %+v", result.CPU)
	}
	if result.CPU.UsagePercent != 0 {
		t.Fatalf("静态 fixture 两次采样应得 0%%，得到 %v", result.CPU.UsagePercent)
	}
	if len(result.Network) != 1 || result.Network[0].Name != "eth0" || result.Network[0].RXBytes != 100 {
		t.Fatalf("网卡字段错误: %+v", result.Network)
	}
	if result.Memory.UsedPercent != 50 {
		t.Fatalf("内存使用率应为 50，得到 %v", result.Memory.UsedPercent)
	}
}

func TestProcessesFixtureSortAndLimit(t *testing.T) {
	procFiles := map[string]string{
		"1234/stat":   procStatFixture("big", "S", 100, 0, 12),
		"1234/status": "VmRSS:\t2048 kB\nThreads:\t12\n",
		"5678/stat":   procStatFixture("small", "R", 200, 100, 3),
		"5678/status": "VmRSS:\t1024 kB\nThreads:\t3\n",
	}
	collector := fixtureCollector(t, procFiles, nil)
	result, err := collector.Processes(Context{}, ProcessParams{Limit: 2, Sort: "mem"})
	if err != nil {
		t.Fatalf("processes 失败: %v", err)
	}
	if result.Total != 2 || result.Count != 2 {
		t.Fatalf("进程总数错误: total=%d count=%d", result.Total, result.Count)
	}
	if result.Items[0].PID != 1234 || result.Items[0].MemRSSBytes != 2048*1024 {
		t.Fatalf("mem 排序错误: %+v", result.Items)
	}
	if result.Items[0].Threads != 12 || result.Items[1].Comm != "small" {
		t.Fatalf("进程字段错误: %+v", result.Items)
	}
	limited, err := collector.Processes(Context{}, ProcessParams{Limit: 1, Sort: "cpu"})
	if err != nil {
		t.Fatalf("processes 失败: %v", err)
	}
	if limited.Count != 1 || limited.Total != 2 {
		t.Fatalf("limit 截断错误: count=%d total=%d", limited.Count, limited.Total)
	}
}

// procStatFixture 生成合法 /proc/<pid>/stat（50 个字段），只填插件读取的 utime/stime/threads。
func procStatFixture(comm, state string, utime, stime, threads int) string {
	fields := make([]string, 49)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0] = state
	fields[1] = "1"
	fields[11] = itoa(utime)
	fields[12] = itoa(stime)
	fields[17] = itoa(threads)
	return "1234 (" + comm + ") " + strings.Join(fields, " ") + "\n"
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	digits := ""
	for v > 0 {
		digits = string(rune('0'+v%10)) + digits
		v /= 10
	}
	return digits
}

func TestTemperatureMissing(t *testing.T) {
	collector := fixtureCollector(t, nil, nil)
	result, err := collector.Temperature(Context{})
	if err != nil {
		t.Fatalf("无 thermal zone 不应报错: %v", err)
	}
	if result.Available || result.Count != 0 || len(result.Zones) != 0 {
		t.Fatalf("无 zone 时应 available=false + 空数组: %+v", result)
	}
}

func TestTemperatureFixture(t *testing.T) {
	collector := fixtureCollector(t, nil, map[string]string{
		"class/thermal/thermal_zone0/temp": "45000\n",
		"class/thermal/thermal_zone0/type": "x86_pkg_temp\n",
		"class/thermal/thermal_zone1/temp": "not-a-number\n",
	})
	result, err := collector.Temperature(Context{})
	if err != nil {
		t.Fatalf("temperature 失败: %v", err)
	}
	if !result.Available || result.Count != 1 {
		t.Fatalf("应只有 1 个有效 zone: %+v", result)
	}
	if result.Zones[0].Zone != "thermal_zone0" || result.Zones[0].TempC != 45 || result.Zones[0].Type != "x86_pkg_temp" {
		t.Fatalf("zone 字段错误: %+v", result.Zones[0])
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("非法 zone 应产生 1 条 warning: %v", result.Warnings)
	}
}

func TestDisksPathNotMounted(t *testing.T) {
	collector := fixtureCollector(t, map[string]string{
		"mounts": "tmpfs /run tmpfs rw 0 0\nproc /proc proc rw 0 0\n",
	}, nil)
	_, err := collector.Disks(Context{}, DisksParams{Path: "/"})
	if !errors.Is(err, errPathNotMounted) {
		t.Fatalf("无真实块设备挂载点时应返回 errPathNotMounted，得到 %v", err)
	}
}

func TestWindowPastDeadline(t *testing.T) {
	collector := fixtureCollector(t, nil, nil)
	ctx := Context{Deadline: time.Now().Add(-time.Second).UnixNano()}
	if _, err := collector.window(ctx); !errors.Is(err, errCallTimeout) {
		t.Fatalf("deadline 已过应返回超时错误，得到 %v", err)
	}
}

func TestDisksMissingMountsDegrades(t *testing.T) {
	collector := fixtureCollector(t, nil, nil)
	result, err := collector.Disks(Context{}, DisksParams{})
	if err != nil {
		t.Fatalf("/proc/mounts 缺失不应让 disks 整次失败: %v", err)
	}
	if result.Count != 0 || len(result.Items) != 0 || len(result.Warnings) == 0 {
		t.Fatalf("应按空列表 + warning 降级: %+v", result)
	}
}

func TestProcessesMissingProcDegrades(t *testing.T) {
	collector := NewCollector(filepath.Join(t.TempDir(), "missing"), t.TempDir())
	collector.interval = 5 * time.Millisecond
	result, err := collector.Processes(Context{}, ProcessParams{Limit: 5, Sort: "cpu"})
	if err != nil {
		t.Fatalf("/proc 不可读不应让 processes 整次失败: %v", err)
	}
	if result.Count != 0 || len(result.Warnings) == 0 {
		t.Fatalf("应按空列表 + warning 降级: %+v", result)
	}
}
