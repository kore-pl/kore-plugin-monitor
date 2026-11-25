package monitor

import (
	"fmt"
	"strings"
	"time"
)

func windowsParseSystem(in shellSystemInput) (systemSnapshot, error) {
	snap := systemSnapshot{Disks: []DiskUsage{}, Network: []NetStat{}, Warnings: []string{}}
	o := in.Outputs
	for _, item := range []struct{ key, label, advice string }{
		{"cpu", "CPU 使用率", "请确认 PowerShell 可读 Win32_PerfFormattedData_PerfOS_Processor"},
		{"mem_total", "内存总量", "请确认 PowerShell 可读 Win32_OperatingSystem"},
		{"mem_pages", "可用内存", "请确认 PowerShell 可读 Win32_OperatingSystem"},
		{"load", "系统负载", "请确认 PowerShell 可读性能计数器"},
		{"boot", "开机时长", "请确认 PowerShell 可读 Win32_OperatingSystem"},
		{"ncpu", "CPU 核数", "请确认 PowerShell 可读 Win32_ComputerSystem"},
		{"disks", "磁盘容量", "请确认 PowerShell 可读 Win32_LogicalDisk"},
		{"network", "网络收发", "请确认 PowerShell 可读网络计数器"},
	} {
		if w := warningFor(o, item.key, item.label, item.advice); w != "" {
			snap.Warnings = append(snap.Warnings, w)
		}
	}
	snap.CPU = windowsParseCPU(in, &snap.Warnings)
	snap.Memory = windowsParseMemory(o)
	snap.Load = windowsParseLoad(o)
	snap.Uptime = windowsParseUptime(o, in.Now)
	if raw := o.get("disks"); raw != "" {
		items, warnings, err := windowsParseDisks(raw, "")
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
	snap.Network = windowsParseNetwork(o.get("network"))
	return snap, nil
}

func windowsParseCPU(in shellSystemInput, warnings *[]string) CPUInfo {
	info := CPUInfo{SampleIntervalMS: in.Interval.Milliseconds()}
	value, err := parseFloatField(in.CPUSecond, "Processor.PercentProcessorTime")
	if err != nil {
		addWarning(warnings, "CPU 使用率采集失败：%v；未填假的百分比", err)
		return info
	}
	info.UsagePercent = clampRate(value, 100)
	if n, err := parseIntField(in.Outputs.get("ncpu"), "NumberOfLogicalProcessors"); err == nil && n > 0 {
		info.Cores = int(n)
	}
	if per := in.Outputs.get("cpu_per"); per != "" {
		var list []PerCPU
		for _, line := range strings.Split(per, "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 || fields[0] == "_Total" {
				continue
			}
			v, err := parseFloatField(fields[1], "PerCPU.PercentProcessorTime")
			if err != nil {
				continue
			}
			list = append(list, PerCPU{Name: "cpu" + fields[0], UsagePercent: clampRate(v, 100)})
		}
		info.PerCPU = list
	}
	return info
}

func windowsParseMemory(o *shellOutputs) MemoryInfo {
	totalKB, err1 := parseFloatField(o.get("mem_total"), "TotalVisibleMemorySize")
	availKB, err2 := parseFloatField(o.get("mem_pages"), "FreePhysicalMemory")
	if err1 != nil || err2 != nil || totalKB <= 0 {
		return MemoryInfo{}
	}
	total, avail := int64(totalKB*1024), int64(availKB*1024)
	if avail > total {
		avail = total
	}
	used := total - avail
	return MemoryInfo{
		Total: total, TotalHuman: humanBytes(total),
		Available: avail, AvailableHuman: humanBytes(avail),
		Used: used, UsedHuman: humanBytes(used),
		Free: avail, FreeHuman: humanBytes(avail),
		UsedPercent: clampRate(float64(used), float64(total)),
	}
}

func windowsParseLoad(o *shellOutputs) LoadInfo {
	value, err := parseFloatField(o.get("load"), "ProcessorQueueLength")
	if err != nil {
		return LoadInfo{}
	}
	return LoadInfo{Load1: round2(value)}
}

func windowsParseUptime(o *shellOutputs, now interface{ Unix() int64 }) UptimeInfo {
	raw := strings.TrimSpace(o.get("boot"))
	boot, err := parseWindowsTimestamp(raw)
	if err != nil || boot <= 0 {
		return UptimeInfo{}
	}
	return UptimeInfo{UptimeS: now.Unix() - boot}
}

func windowsParseDisks(raw, path string) ([]DiskDetail, []string, error) {
	items := []DiskDetail{}
	warnings := []string{}
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		total, e1 := parseIntField(fields[2], "Win32_LogicalDisk.Size")
		free, e2 := parseIntField(fields[3], "Win32_LogicalDisk.FreeSpace")
		if e1 != nil || e2 != nil || total <= 0 {
			continue
		}
		mount := strings.TrimSpace(fields[4])
		if path != "" && !strings.EqualFold(mount, path) {
			continue
		}
		used := total - free
		items = append(items, DiskDetail{
			Mount: mount, Device: fields[0], FSType: fields[1],
			Total: total, TotalHuman: humanBytes(total), Used: used, UsedHuman: humanBytes(used),
			Avail: free, AvailHuman: humanBytes(free), UsedPercent: clampRate(float64(used), float64(total)),
		})
	}
	if path != "" && len(items) == 0 {
		return items, warnings, errPathNotMounted
	}
	warnings = append(warnings, "Windows 当前采集路径未提供块设备累计读写字节数，相关字段保持 0（不填假数）")
	return items, warnings, nil
}

func windowsParseNetwork(raw string) []NetStat {
	result := []NetStat{}
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		rx, e1 := parseIntField(fields[1], "BytesReceivedPersec")
		tx, e2 := parseIntField(fields[2], "BytesSentPersec")
		rxp, e3 := parseIntField(fields[3], "PacketsReceivedPersec")
		txp, e4 := parseIntField(fields[4], "PacketsSentPersec")
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
			continue
		}
		result = append(result, NetStat{
			Name: fields[0], RXBytes: rx, TXBytes: tx, RXPackets: rxp, TXPackets: txp,
			RXHuman: humanBytes(rx), TXHuman: humanBytes(tx),
		})
	}
	return result
}

type windowsProcSample struct {
	name string
	cpu  float64
	rss  int64
	thr  int64
}

func windowsParseProcesses(first, second string, o *shellOutputs, limit int, sortKey string) ([]ProcessInfo, []string, error) {
	current := map[int]windowsProcSample{}
	for _, line := range strings.Split(second, "\n") {
		parts := strings.Split(strings.TrimSpace(line), "|")
		if len(parts) != 5 {
			continue
		}
		pid, err := parseIntField(parts[0], "ps pid")
		if err != nil {
			continue
		}
		cpu, err := parseFloatField(parts[2], "ps cpu")
		if err != nil {
			continue
		}
		rss, err := parseIntField(parts[3], "ps rss")
		if err != nil {
			continue
		}
		thr, err := parseIntField(parts[4], "ps threads")
		if err != nil {
			continue
		}
		current[int(pid)] = windowsProcSample{name: parts[1], cpu: cpu, rss: rss, thr: thr}
	}
	var totalMemory float64
	if n, err := parseFloatField(o.get("mem_total"), "TotalVisibleMemorySize"); err == nil && n > 0 {
		totalMemory = n * 1024
	}
	items := make([]ProcessInfo, 0, len(current))
	for pid, sample := range current {
		memPercent := 0.0
		if totalMemory > 0 {
			memPercent = clampRate(float64(sample.rss), totalMemory)
		}
		items = append(items, ProcessInfo{
			PID: pid, Comm: sample.name, Name: sample.name, State: "",
			CPUPercent: clampRate(sample.cpu, 100), MemRSSBytes: sample.rss,
			MemRSSHuman: humanBytes(sample.rss), MemPercent: memPercent, Threads: int(sample.thr),
		})
	}
	return items, []string{}, nil
}

func windowsParseTemperature(raw string) (TemperatureResult, error) {
	result := TemperatureResult{Zones: []ThermalZone{}, Warnings: []string{}, Available: false}
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		decidegrees, err := parseFloatField(fields[1], "MSAcpi_ThermalZoneTemperature")
		if err != nil || decidegrees <= 0 {
			continue
		}
		tempC := (decidegrees - 2732) / 10
		result.Zones = append(result.Zones, ThermalZone{Zone: fields[0], TempC: round2(tempC), Type: "acpi"})
	}
	result.Count = len(result.Zones)
	result.Available = result.Count > 0
	if !result.Available {
		result.Warnings = append(result.Warnings, "WMI 未输出可读温度探针：可用=false；很多 Windows 主机无该 WMI 类，未填 0 冒充实测")
	}
	return result, nil
}

// parseWindowsTimestamp 解析 PowerShell ToString("o") 的 ISO8601 时间戳为 Unix 秒。
func parseWindowsTimestamp(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("LastBootUpTime 为空")
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05.0000000Z07:00",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.Unix(), nil
		}
	}
	return 0, fmt.Errorf("解析 LastBootUpTime 失败：%q", raw)
}
