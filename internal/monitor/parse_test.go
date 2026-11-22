package monitor

import (
	"strings"
	"testing"
	"time"
)

func TestCPUStatDelta(t *testing.T) {
	prevData := "cpu  100 20 30 800 10 5 5 0 0 0\ncpu0 50 10 15 400 5 2 3 0 0 0\ncpu1 50 10 15 400 5 3 2 0 0 0\n"
	currData := "cpu  110 20 35 825 10 5 5 0 0 0\ncpu0 55 10 18 412 5 2 3 0 0 0\ncpu1 55 10 17 413 5 3 2 0 0 0\n"
	prev, prevPer, err := parseCPUStat(prevData)
	if err != nil {
		t.Fatalf("prev 解析失败: %v", err)
	}
	curr, currPer, err := parseCPUStat(currData)
	if err != nil {
		t.Fatalf("curr 解析失败: %v", err)
	}
	if len(prevPer) != 2 || len(currPer) != 2 {
		t.Fatalf("分核数量 prev=%d curr=%d，期望 2", len(prevPer), len(currPer))
	}
	// Δtotal=40，Δidle=(835-810)=25 → busy=15 → 37.5%；Δuser=10 → 25%。
	got := cpuDeltaUsage(prev, curr)
	if got.Usage != 37.5 || got.User != 25 || got.System != 12.5 || got.IOWait != 0 {
		t.Fatalf("delta 计算错误: %+v", got)
	}
	if got := cpuDeltaUsage(prev, prev); got.Usage != 0 {
		t.Fatalf("零 delta 应为 0，得到 %+v", got)
	}
}

func TestParseMeminfo(t *testing.T) {
	fixture := "MemTotal:       16384000 kB\nMemFree:         1000000 kB\nMemAvailable:    8192000 kB\n" +
		"Buffers:          200000 kB\nCached:          4000000 kB\nSReclaimable:     500000 kB\n" +
		"SwapTotal:       2097152 kB\nSwapFree:        1048576 kB\n"
	info := parseMeminfo(fixture)
	if info.Total != 16384000*1024 || info.Available != 8192000*1024 || info.Free != 1000000*1024 {
		t.Fatalf("内存字段换算错误: %+v", info)
	}
	if info.Cached != (4000000+500000)*1024 {
		t.Fatalf("cached 应含 SReclaimable: %d", info.Cached)
	}
	if info.usedBytes() != (16384000-8192000)*1024 {
		t.Fatalf("used 口径错误: %d", info.usedBytes())
	}
	if info.usedPercent() != 50 {
		t.Fatalf("used_percent=%v，期望 50", info.usedPercent())
	}
}

func TestParseMeminfoFallbackAvailable(t *testing.T) {
	info := parseMeminfo("MemTotal: 1000 kB\nMemFree: 100 kB\nBuffers: 50 kB\nCached: 150 kB\n")
	if info.Available != 300*1024 {
		t.Fatalf("MemAvailable 缺失时应回退 free+buffers+cached，得到 %d", info.Available)
	}
}

func TestParseLoadavg(t *testing.T) {
	info, err := parseLoadavg("0.52 0.58 0.59 2/1234 56789\n")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if info.Load1 != 0.52 || info.Load5 != 0.58 || info.Load15 != 0.59 || info.Running != 2 || info.Total != 1234 || info.LastPID != 56789 {
		t.Fatalf("loadavg 字段错误: %+v", info)
	}
	if _, err := parseLoadavg("bad"); err == nil {
		t.Fatal("非法 loadavg 应报错")
	}
}

func TestParseUptime(t *testing.T) {
	info, err := parseUptime("12345.67 98765.43\n")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if info.UptimeS != 12345 || info.IdleS != 98765 {
		t.Fatalf("uptime 字段错误: %+v", info)
	}
}

func TestParseMountsFiltersVirtual(t *testing.T) {
	fixture := strings.Join([]string{
		"/dev/nvme0n1p2 / ext4 rw,relatime 0 0",
		"tmpfs /run tmpfs rw,nosuid 0 0",
		"proc /proc proc rw 0 0",
		"sysfs /sys sysfs rw 0 0",
		"/dev/sda1 /home xfs rw 0 0",
		"/dev/mapper/vg-lv /var ext4 rw 0 0",
		"overlay /var/lib/docker/overlay2/x overlay rw 0 0",
		"/dev/nvme0n1p1 /boot/efi vfat rw 0 0",
		"/dev/sdb1 /mnt/my\\040disk btrfs rw 0 0",
	}, "\n")
	mounts := parseMounts(fixture)
	if len(mounts) != 5 {
		t.Fatalf("应保留 5 个真实块设备挂载点，得到 %d: %+v", len(mounts), mounts)
	}
	byMount := map[string]mountEntry{}
	for _, entry := range mounts {
		byMount[entry.Mount] = entry
	}
	if _, ok := byMount["/"]; !ok {
		t.Fatal("缺少 / 挂载点")
	}
	if entry, ok := byMount["/mnt/my disk"]; !ok || entry.Device != "/dev/sdb1" {
		t.Fatalf("八进制转义未还原: %+v", mounts)
	}
	if _, ok := byMount["/var"]; !ok {
		t.Fatal("LVM 挂载点应保留")
	}
	for _, entry := range mounts {
		if entry.FSType == "tmpfs" || entry.FSType == "proc" || entry.FSType == "sysfs" || entry.FSType == "overlay" {
			t.Fatalf("虚拟文件系统未过滤: %+v", entry)
		}
	}
}

func TestParseNetDev(t *testing.T) {
	fixture := "Inter-|   Receive                                                |  Transmit\n" +
		" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n" +
		"    lo: 1000 10 0 0 0 0 0 0 1000 10 0 0 0 0 0 0\n" +
		"  eth0: 5000 50 2 0 0 0 0 0 8000 80 3 0 0 0 0 0\n"
	all, err := parseNetDev(fixture, true)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("应解析 2 个网卡，得到 %d", len(all))
	}
	noLoop, err := parseNetDev(fixture, false)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(noLoop) != 1 || noLoop[0].Name != "eth0" {
		t.Fatalf("排除 lo 后应只剩 eth0: %+v", noLoop)
	}
	eth := noLoop[0]
	if eth.RXBytes != 5000 || eth.TXBytes != 8000 || eth.RXPackets != 50 || eth.RXErrors != 2 || eth.TXErrors != 3 {
		t.Fatalf("网卡字段映射错误: %+v", eth)
	}
}

func TestParseProcStat(t *testing.T) {
	rest := make([]string, 20)
	for i := range rest {
		rest[i] = "0"
	}
	rest[0] = "S"
	rest[1] = "1"
	rest[11] = "120"
	rest[12] = "45"
	rest[17] = "7"
	data := "1234 (kore worker (x)) " + strings.Join(rest, " ") + "\n"
	info, err := parseProcStat(data, 1234)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if info.PID != 1234 || info.Comm != "kore worker (x)" || info.State != 'S' {
		t.Fatalf("stat 公共字段错误: %+v", info)
	}
	if info.UTicks != 120 || info.STicks != 45 || info.Threads != 7 {
		t.Fatalf("stat tick/线程字段错误: %+v", info)
	}
	if _, err := parseProcStat("bad", 1); err == nil {
		t.Fatal("非法 stat 应报错")
	}
}

func TestProcCPUPercent(t *testing.T) {
	// 200ms 内累计 20 tick = 0.2s CPU → 单核 100%。
	if got := procCPUPercent(1000, 1020, 200*time.Millisecond); got != 100 {
		t.Fatalf("cpu_percent=%v，期望 100", got)
	}
	if got := procCPUPercent(1020, 1000, 200*time.Millisecond); got != 0 {
		t.Fatalf("tick 回绕应为 0，得到 %v", got)
	}
	if got := procCPUPercent(1000, 1020, 0); got != 0 {
		t.Fatalf("elapsed=0 应为 0，得到 %v", got)
	}
}

func TestParseDiskstats(t *testing.T) {
	fixture := "   8       0 sda 1000 0 2048 0 2000 0 4096 0 0 0 0\n" +
		" 259       0 nvme0n1p2 300 0 600 0 400 0 800 0 0 0 0\n"
	stats := parseDiskstats(fixture)
	if stats["sda"].Reads != 1000 || stats["sda"].ReadBytes != 2048*512 {
		t.Fatalf("sda 统计错误: %+v", stats["sda"])
	}
	if stats["nvme0n1p2"].Writes != 400 || stats["nvme0n1p2"].WriteBytes != 800*512 {
		t.Fatalf("nvme 统计错误: %+v", stats["nvme0n1p2"])
	}
}

func TestParseThermal(t *testing.T) {
	temp, err := parseMilliCelsius("45000\n")
	if err != nil || temp != 45 {
		t.Fatalf("thermal 解析错误: temp=%v err=%v", temp, err)
	}
	if _, err := parseMilliCelsius("not-a-number"); err == nil {
		t.Fatal("非法温度应报错")
	}
}

func TestMatchMount(t *testing.T) {
	mounts := []mountEntry{{Mount: "/", Device: "/dev/sda1"}, {Mount: "/home", Device: "/dev/sdb1"}}
	if entry, ok := matchMount(mounts, "/home/pt/file"); !ok || entry.Mount != "/home" {
		t.Fatalf("应匹配 /home: %+v ok=%v", entry, ok)
	}
	if entry, ok := matchMount(mounts, "/var/log"); !ok || entry.Mount != "/" {
		t.Fatalf("应回落到 /: %+v ok=%v", entry, ok)
	}
	if _, ok := matchMount(nil, "/"); ok {
		t.Fatal("空挂载表不应匹配")
	}
}

func TestSortProcessItems(t *testing.T) {
	items := []ProcessInfo{
		{PID: 3, CPUPercent: 1, MemRSSBytes: 300},
		{PID: 1, CPUPercent: 9, MemRSSBytes: 100},
		{PID: 2, CPUPercent: 1, MemRSSBytes: 200},
	}
	sortProcessItems(items, "cpu")
	if items[0].PID != 1 || items[1].PID != 2 || items[2].PID != 3 {
		t.Fatalf("cpu 排序错误: %+v", items)
	}
	sortProcessItems(items, "mem")
	if items[0].PID != 3 || items[1].PID != 2 || items[2].PID != 1 {
		t.Fatalf("mem 排序错误: %+v", items)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{0: "0 B", 512: "512 B", 1024: "1.0 KiB", 3 * 1024 * 1024 * 1024: "3.0 GiB"}
	for input, want := range cases {
		if got := humanBytes(input); got != want {
			t.Fatalf("humanBytes(%d)=%q，期望 %q", input, got, want)
		}
	}
}
