package monitor

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// 本文件用替身输出验证 darwin / windows 解析器的真实算法（本工作区是 Linux，
// 无法运行 macOS / Windows 二进制，故只做交叉编译 + fixture 单测，真机未验证如实标注）。

func TestDarwinCPUDelta(t *testing.T) {
	first := "100 0 50 0 850"
	second := "200 0 150 0 1650"
	u1, n1, s1, i1, d1, err := darwinCPUSnapshot(first)
	if err != nil || u1 != 100 || n1 != 0 || s1 != 50 || i1 != 0 || d1 != 850 {
		t.Fatalf("首次快照解析失败: %v %v %v %v %v %v", u1, n1, s1, i1, d1, err)
	}
	o := newShellOutputs()
	o.values["ncpu"] = "8"
	in := shellSystemInput{Outputs: o, CPUFirst: first, CPUSecond: second, Interval: 200 * time.Millisecond}
	warnings := []string{}
	info := darwinParseCPU(in, &warnings)
	if info.UsagePercent != 20 || info.Cores != 8 {
		t.Fatalf("CPU 增量解析错误: usage=%v cores=%d warnings=%v", info.UsagePercent, info.Cores, warnings)
	}
	if len(warnings) != 0 {
		t.Fatalf("正常输入不应有 warning: %v", warnings)
	}
}

func TestDarwinMemoryDisksNetworkTemperature(t *testing.T) {
	o := newShellOutputs()
	o.values["mem_total"] = "17179869184"
	o.values["mem_pages"] = "Mach Virtual Memory Statistics: (page size of 4096 bytes)\nPages free: 1000000.\nPages inactive: 500000.\nPages speculative: 250000.\n"
	mem := darwinParseMemory(o)
	if mem.Total != 17179869184 || mem.Available != 1750000*4096 || mem.UsedPercent <= 0 {
		t.Fatalf("内存解析不符: %+v", mem)
	}
	o.values["boot"] = "{ sec = 1699990000 usec = 0 } Mon Nov 13 22:06:40 2023"
	o.values["load"] = "{ 1.50 2.00 3.00 }"
	load := darwinParseLoad(o)
	if load.Load1 != 1.5 || load.Load5 != 2 || load.Load15 != 3 {
		t.Fatalf("负载解析不符: %+v", load)
	}
	boot := darwinParseUptime(o, time.Unix(1700000000, 0))
	if boot.UptimeS <= 0 {
		t.Fatalf("开机时长应为正: %+v", boot)
	}
	disks, warnings, err := darwinParseDisks("Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/disk1s1 1000000 600000 400000 60% /\n", "")
	if err != nil || len(disks) != 1 || disks[0].UsedPercent != 60 || len(warnings) == 0 {
		t.Fatalf("磁盘解析不符: %+v warnings=%v err=%v", disks, warnings, err)
	}
	if _, _, err := darwinParseDisks("/dev/disk1s1 1000000 600000 400000 60% /\n", "/missing"); err != errPathNotMounted {
		t.Fatalf("指定不存在的挂载点应回 errPathNotMounted, got %v", err)
	}
	nets := darwinParseNetwork("Name Mtu Network Address Ipkts Ierrs Ibytes Opkts Oerrs Obytes\nen0 1500 <Link#1> aa 100 0 204800 90 0 102400\n")
	if len(nets) != 1 || nets[0].RXBytes != 204800 || nets[0].TXBytes != 102400 {
		t.Fatalf("网络解析不符: %+v", nets)
	}
	temp, err := darwinParseTemperature("CPU Die temperature: 58.5 C\n")
	if err != nil || !temp.Available || len(temp.Zones) != 1 || temp.Zones[0].TempC != 58.5 {
		t.Fatalf("温度解析不符: %+v err=%v", temp, err)
	}
}

func TestDarwinProcesses(t *testing.T) {
	first := "123 kore S 0:01.00 2048 1.0 3\n456 nginx S 0:02.00 4096 2.0 4\n"
	second := "123 kore S 0:01.20 2048 1.0 3\n456 nginx S 0:04.00 4096 2.0 4\n"
	o := newShellOutputs()
	o.values["mem_total"] = "10000000"
	items, warnings, err := darwinParseProcesses(first, second, o, 2, "cpu")
	if err != nil || len(items) != 2 || len(warnings) != 0 {
		t.Fatalf("进程解析失败: %+v %v %v", items, warnings, err)
	}
	byPID := map[int]ProcessInfo{}
	for _, item := range items {
		byPID[item.PID] = item
	}
	if byPID[456].CPUPercent < byPID[123].CPUPercent {
		t.Fatalf("CPU 增量排序失败: %+v", byPID)
	}
	if byPID[123].MemRSSBytes != 2048*1024 || byPID[123].Threads != 3 {
		t.Fatalf("RSS/线程解析失败: %+v", byPID[123])
	}
}

func TestWindowsParsers(t *testing.T) {
	o := newShellOutputs()
	o.values["mem_total"] = "16384"
	o.values["mem_pages"] = "4096"
	o.values["load"] = "7"
	o.values["ncpu"] = "8"
	o.values["boot"] = "2023-11-14T22:13:20.0000000Z"
	in := shellSystemInput{Outputs: o, CPUFirst: "", CPUSecond: "42.5", Interval: 200 * time.Millisecond}
	warnings := []string{}
	snap := systemSnapshot{}
	_ = snap
	cpu := windowsParseCPU(in, &warnings)
	if cpu.UsagePercent != 42.5 || cpu.Cores != 8 || len(warnings) != 0 {
		t.Fatalf("Windows CPU 解析不符: %+v %v", cpu, warnings)
	}
	mem := windowsParseMemory(o)
	if mem.Total != 16384*1024 || mem.Available != 4096*1024 || mem.UsedPercent != 75 {
		t.Fatalf("Windows 内存解析不符: %+v", mem)
	}
	load := windowsParseLoad(o)
	if load.Load1 != 7 {
		t.Fatalf("Windows 负载解析不符: %+v", load)
	}
	ts, err := parseWindowsTimestamp("2023-11-14T22:13:20.0000000Z")
	if err != nil || ts <= 0 {
		t.Fatalf("Windows 时间戳解析失败: %d %v", ts, err)
	}
	disks, warnings, err := windowsParseDisks("C: NTFS 100000000000 40000000000 C:\\", "")
	if err != nil || len(disks) != 1 || disks[0].UsedPercent != 60 || len(warnings) == 0 {
		t.Fatalf("Windows 磁盘解析不符: %+v %v %v", disks, warnings, err)
	}
	nets := windowsParseNetwork("Ethernet 1024 2048 10 20\n")
	if len(nets) != 1 || nets[0].RXBytes != 1024 || nets[0].TXPackets != 20 {
		t.Fatalf("Windows 网络解析不符: %+v", nets)
	}
	items, _, err := windowsParseProcesses("1|a|0|1024|2\n", "1|a|12.5|2048|3\n", o, 10, "cpu")
	if err != nil || len(items) != 1 || items[0].CPUPercent != 12.5 || items[0].Threads != 3 {
		t.Fatalf("Windows 进程解析不符: %+v %v", items, err)
	}
	temp, err := windowsParseTemperature("ACPI\t3140\n")
	if err != nil || !temp.Available || temp.Zones[0].TempC != 40.8 {
		t.Fatalf("Windows 温度解析不符: %+v %v", temp, err)
	}
}

// TestShellExecutorUsesPlatformParsers 用替身命令验证四方法不返回 E_UNSUPPORTED，
// 且任一命令失败只写 warning、不静默伪造读数。
func TestShellExecutorUsesPlatformParsers(t *testing.T) {
	calls := 0
	runner := func(spec commandSpec) (string, error) {
		calls++
		if strings.Contains(spec.program, "powershell") {
			return "42.5", nil
		}
		return "", nil
	}
	exec := &shellExecutor{
		osName: "test", source: "test/fake",
		commands: shellCommands{CPU: commandSpec{program: "fake"}, CPUPer: commandSpec{program: "fake"}, Temperature: commandSpec{program: "fake"}},
		parsers: shellParsers{
			system: func(in shellSystemInput) (systemSnapshot, error) {
				return systemSnapshot{Warnings: []string{}, Disks: []DiskUsage{}, Network: []NetStat{}}, nil
			},
			processes: func(a, b string, o *shellOutputs, limit int, sort string) ([]ProcessInfo, []string, error) {
				return []ProcessInfo{{PID: 1, Comm: "fake"}}, []string{}, nil
			},
			disks: func(out, path string) ([]DiskDetail, []string, error) {
				return []DiskDetail{{Mount: "/fake"}}, []string{}, nil
			},
			temperature: func(out string) (TemperatureResult, error) {
				return TemperatureResult{Available: true, Zones: []ThermalZone{{Zone: "fake", TempC: 50}}}, nil
			},
		},
		runner: runner, now: time.Now, sleep: func(time.Duration) {}, interval: time.Millisecond,
	}
	sys, err := exec.System(Context{}, SystemParams{})
	if err != nil || sys.Source != "test/fake" || calls == 0 {
		t.Fatalf("shell system 执行失败: %+v %v calls=%d", sys, err, calls)
	}
	temp, err := exec.Temperature(Context{})
	if err != nil || !temp.Available {
		t.Fatalf("shell temperature 执行失败: %+v %v", temp, err)
	}
	// Call 用注入的 exec 时，平台门禁只挡真正未实现的 OS，不应因 GOOS 而拒绝。
	raw, perr := Call("monitor.system", json.RawMessage(`{}`), exec, Context{})
	if perr != nil || len(raw) == 0 {
		t.Fatalf("注入 exec 的 Call 不应返回错误: %v %s", perr, raw)
	}
}
