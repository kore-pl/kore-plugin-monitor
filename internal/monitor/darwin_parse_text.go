package monitor

import (
	"fmt"
	"strings"
	"time"
)

func darwinCPUSnapshot(raw string) (user, nice, system, intr, idle float64, err error) {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) < 5 {
		return 0, 0, 0, 0, 0, fmt.Errorf("kern.cp_time 需要 5 个计数字段，实得 %d", len(fields))
	}
	values := []float64{}
	for _, field := range fields[:5] {
		v, parseErr := parseFloatField(field, "kern.cp_time")
		if parseErr != nil {
			return 0, 0, 0, 0, 0, parseErr
		}
		values = append(values, v)
	}
	return values[0], values[1], values[2], values[3], values[4], nil
}

func darwinParseCPU(in shellSystemInput, warnings *[]string) CPUInfo {
	info := CPUInfo{SampleIntervalMS: in.Interval.Milliseconds()}
	u1, n1, s1, i1, d1, err := darwinCPUSnapshot(in.CPUFirst)
	if err != nil {
		addWarning(warnings, "CPU 首次采样失败：%v；未填假的百分比", err)
		return info
	}
	u2, n2, s2, i2, d2, err := darwinCPUSnapshot(in.CPUSecond)
	if err != nil {
		addWarning(warnings, "CPU 第二次采样失败：%v；未填假的百分比", err)
		return info
	}
	busy := (u2 - u1) + (n2 - n1) + (s2 - s1)
	total := busy + (i2 - i1) + (d2 - d1)
	info.UsagePercent = clampRate(busy, total)
	info.UserPercent = clampRate(u2-u1, total)
	info.SystemPercent = clampRate(s2-s1, total)
	info.IOWaitPercent = clampRate(i2-i1, total)
	info.IdlePercent = clampRate(d2-d1, total)
	if n, err := parseIntField(in.Outputs.get("ncpu"), "hw.ncpu"); err == nil && n > 0 {
		info.Cores = int(n)
	}
	return info
}

func darwinParseSystem(in shellSystemInput) (systemSnapshot, error) {
	snap := systemSnapshot{Disks: []DiskUsage{}, Network: []NetStat{}, Warnings: []string{}}
	o := in.Outputs
	for _, item := range []struct{ key, label, advice string }{
		{"cpu", "CPU 使用率", "请确认 sysctl kern.cp_time 可读"},
		{"mem_total", "内存总量", "请确认 sysctl hw.memsize 可读"},
		{"mem_pages", "内存用量", "请确认 vm_stat 可读"},
		{"load", "系统负载", "请确认 sysctl vm.loadavg 可读"},
		{"boot", "开机时长", "请确认 sysctl kern.boottime 可读"},
		{"ncpu", "CPU 核数", "请确认 sysctl hw.ncpu 可读"},
		{"disks", "磁盘容量", "请确认 df -kP 可读"},
		{"network", "网络收发", "请确认 netstat -ib 可读"},
	} {
		if w := warningFor(o, item.key, item.label, item.advice); w != "" {
			snap.Warnings = append(snap.Warnings, w)
		}
	}
	snap.CPU = darwinParseCPU(in, &snap.Warnings)
	snap.Memory = darwinParseMemory(o)
	snap.Load = darwinParseLoad(o)
	snap.Uptime = darwinParseUptime(o, in.Now)
	if raw := o.get("disks"); raw != "" {
		items, warnings, err := darwinParseDisks(raw, "")
		if err != nil {
			snap.Warnings = append(snap.Warnings, err.Error())
		} else {
			for _, d := range items {
				snap.Disks = append(snap.Disks, DiskUsage{
					Mount: d.Mount, Device: d.Device, FSType: d.FSType,
					Total: d.Total, TotalHuman: d.TotalHuman, Used: d.Used, UsedHuman: d.UsedHuman,
					Avail: d.Avail, AvailHuman: d.AvailHuman, UsedPercent: d.UsedPercent,
				})
			}
			snap.Warnings = append(snap.Warnings, warnings...)
		}
	}
	snap.Network = darwinParseNetwork(o.get("network"))
	return snap, nil
}

func darwinParseMemory(o *shellOutputs) MemoryInfo {
	total, err := parseIntField(o.get("mem_total"), "hw.memsize")
	if err != nil || total <= 0 {
		return MemoryInfo{}
	}
	var pageSize, freePages, inactivePages, speculativePages int64
	for _, line := range strings.Split(o.get("mem_pages"), "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		v, err := parseIntField(strings.ReplaceAll(strings.TrimSpace(parts[1]), ".", ""), parts[0])
		if err != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(parts[0])) {
		case "page size":
			pageSize = v
		case "pages free":
			freePages = v
		case "pages inactive":
			inactivePages = v
		case "pages speculative":
			speculativePages = v
		}
	}
	if pageSize <= 0 {
		pageSize = 4096
	}
	available := (freePages + inactivePages + speculativePages) * pageSize
	if available > total {
		available = total
	}
	used := total - available
	return MemoryInfo{
		Total: total, TotalHuman: humanBytes(total),
		Available: available, AvailableHuman: humanBytes(available),
		Used: used, UsedHuman: humanBytes(used),
		Free: freePages * pageSize, FreeHuman: humanBytes(freePages * pageSize),
		UsedPercent: clampRate(float64(used), float64(total)),
	}
}

func darwinParseLoad(o *shellOutputs) LoadInfo {
	raw := strings.TrimSpace(o.get("load"))
	raw = strings.TrimSuffix(strings.TrimPrefix(raw, "{"), "}")
	fields := strings.Fields(raw)
	if len(fields) < 3 {
		return LoadInfo{}
	}
	var info LoadInfo
	values := []*float64{&info.Load1, &info.Load5, &info.Load15}
	for i, target := range values {
		v, err := parseFloatField(fields[i], "vm.loadavg")
		if err != nil {
			return LoadInfo{}
		}
		*target = round2(v)
	}
	return info
}

func darwinParseUptime(o *shellOutputs, now time.Time) UptimeInfo {
	raw := strings.TrimSpace(o.get("boot"))
	start := strings.Index(raw, "{ sec = ")
	if start < 0 {
		return UptimeInfo{}
	}
	rest := raw[start+len("{ sec = "):]
	end := strings.IndexAny(rest, ", }")
	if end < 0 {
		return UptimeInfo{}
	}
	bootSec, err := parseIntField(rest[:end], "kern.boottime")
	if err != nil || bootSec <= 0 {
		return UptimeInfo{}
	}
	return UptimeInfo{UptimeS: now.Unix() - bootSec}
}

func darwinParseDisks(raw, path string) ([]DiskDetail, []string, error) {
	items := []DiskDetail{}
	warnings := []string{}
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 || fields[0] == "Filesystem" {
			continue
		}
		total, err1 := parseIntField(fields[1], "df 1K-blocks")
		used, err2 := parseIntField(fields[2], "df Used")
		avail, err3 := parseIntField(fields[3], "df Avail")
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		mount := strings.Join(fields[5:], " ")
		if path != "" && mount != path {
			continue
		}
		items = append(items, DiskDetail{
			Mount: mount, Device: fields[0], FSType: "",
			Total: total * 1024, TotalHuman: humanBytes(total * 1024),
			Used: used * 1024, UsedHuman: humanBytes(used * 1024),
			Avail: avail * 1024, AvailHuman: humanBytes(avail * 1024),
			UsedPercent: diskUsedPercent(used, avail),
		})
	}
	if path != "" && len(items) == 0 {
		return items, warnings, errPathNotMounted
	}
	warnings = append(warnings, "macOS 当前采集路径未提供块设备累计读写字节数，相关字段保持 0（不填假数）")
	return items, warnings, nil
}

func darwinParseNetwork(raw string) []NetStat {
	result := []NetStat{}
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 || strings.HasPrefix(fields[6], "lo") {
			continue
		}
		rx, e1 := parseIntField(fields[6], "netstat bytes-in")
		tx, e2 := parseIntField(fields[9], "netstat bytes-out")
		if e1 != nil || e2 != nil {
			continue
		}
		result = append(result, NetStat{
			Name: fields[0], RXBytes: rx, TXBytes: tx,
			RXHuman: humanBytes(rx), TXHuman: humanBytes(tx),
		})
	}
	return result
}

type darwinProcSample struct {
	comm  string
	state string
	time  float64
	rss   int64
	pcpu  float64
	thr   int64
}

func darwinParseProcLine(line string) (int, darwinProcSample, error) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 7 {
		return 0, darwinProcSample{}, fmt.Errorf("ps 行字段不足：%q", line)
	}
	pid, err := parseIntField(fields[0], "ps pid")
	if err != nil {
		return 0, darwinProcSample{}, err
	}
	ticks, err := parseTimeTicks(fields[3])
	if err != nil {
		return 0, darwinProcSample{}, err
	}
	rss, err := parseIntField(fields[4], "ps rss")
	if err != nil {
		return 0, darwinProcSample{}, err
	}
	pcpu, err := parseFloatField(fields[5], "ps pcpu")
	if err != nil {
		return 0, darwinProcSample{}, err
	}
	thr, err := parseIntField(fields[6], "ps threads")
	if err != nil {
		return 0, darwinProcSample{}, err
	}
	return int(pid), darwinProcSample{comm: fields[1], state: fields[2], time: ticks, rss: rss * 1024, pcpu: pcpu, thr: thr}, nil
}

func darwinParseProcesses(first, second string, o *shellOutputs, limit int, sortKey string) ([]ProcessInfo, []string, error) {
	current := map[int]darwinProcSample{}
	for _, line := range strings.Split(second, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		pid, sample, err := darwinParseProcLine(line)
		if err == nil {
			current[pid] = sample
		}
	}
	previous := map[int]darwinProcSample{}
	for _, line := range strings.Split(first, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		pid, sample, err := darwinParseProcLine(line)
		if err == nil {
			previous[pid] = sample
		}
	}
	var totalMemory float64
	if n, err := parseIntField(o.get("mem_total"), "hw.memsize"); err == nil && n > 0 {
		totalMemory = float64(n)
	}
	items := make([]ProcessInfo, 0, len(current))
	for pid, sample := range current {
		cpu := normalizeCPU(sample.pcpu)
		if prev, ok := previous[pid]; ok && sample.time >= prev.time {
			// ps 的 TIME 以秒计，CPU% 按单核归一。
			cpu = normalizeCPU((sample.time - prev.time) * 100)
		}
		memPercent := 0.0
		if totalMemory > 0 {
			memPercent = clampRate(float64(sample.rss), totalMemory)
		}
		items = append(items, ProcessInfo{
			PID: pid, Comm: sample.comm, Name: sample.comm, State: sample.state,
			CPUPercent: cpu, MemRSSBytes: sample.rss, MemRSSHuman: humanBytes(sample.rss),
			MemPercent: memPercent, Threads: int(sample.thr),
		})
	}
	return items, []string{}, nil
}

func darwinParseTemperature(raw string) (TemperatureResult, error) {
	result := TemperatureResult{Zones: []ThermalZone{}, Warnings: []string{}, Available: false}
	for _, line := range strings.Split(raw, "\n") {
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "temperature") && !strings.Contains(lower, "temp") {
			continue
		}
		// powermetrics 常见形态："CPU Die temperature: 58.5 C" / "temperature 58.5"。
		fields := strings.Fields(strings.ReplaceAll(line, ":", " "))
		for _, field := range fields {
			candidate := strings.Trim(field, "\t ,")
			v, err := parseFloatField(candidate, "powermetrics temperature")
			if err != nil || v < -50 || v > 200 {
				continue
			}
			result.Zones = append(result.Zones, ThermalZone{Zone: fmt.Sprintf("smc-%d", len(result.Zones)+1), TempC: round2(v), Type: "smc"})
			break
		}
	}
	result.Count = len(result.Zones)
	result.Available = result.Count > 0
	if !result.Available {
		result.Warnings = append(result.Warnings, "powermetrics 未输出可读温度传感器：可用=false；Mac 上首次运行可能需 sudo")
	}
	return result, nil
}
