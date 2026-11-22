package monitor

import (
	"os"
	"path/filepath"
)

// collectMemory 读取 /proc/meminfo；失败时返回零值内存 + warning。
func (c *Collector) collectMemory(warnings *[]string) MemoryInfo {
	data, err := os.ReadFile(filepath.Join(c.procRoot, "meminfo"))
	if err != nil {
		addWarning(warnings, "读取 /proc/meminfo 失败: %v", err)
		return MemoryInfo{}
	}
	raw := parseMeminfo(string(data))
	return MemoryInfo{
		Total: raw.Total, TotalHuman: humanBytes(raw.Total),
		Available: raw.Available, AvailableHuman: humanBytes(raw.Available),
		Used: raw.usedBytes(), UsedHuman: humanBytes(raw.usedBytes()),
		Free: raw.Free, FreeHuman: humanBytes(raw.Free),
		Buffers: raw.Buffers, BuffersHuman: humanBytes(raw.Buffers),
		Cached: raw.Cached, CachedHuman: humanBytes(raw.Cached),
		SwapTotal: raw.SwapTotal, SwapTotalHuman: humanBytes(raw.SwapTotal),
		SwapUsed: raw.SwapTotal - raw.SwapFree, SwapUsedHuman: humanBytes(raw.SwapTotal - raw.SwapFree),
		UsedPercent: raw.usedPercent(),
	}
}

// collectLoad 读取 /proc/loadavg；失败只 warning。
func (c *Collector) collectLoad(warnings *[]string) LoadInfo {
	data, err := os.ReadFile(filepath.Join(c.procRoot, "loadavg"))
	if err != nil {
		addWarning(warnings, "读取 /proc/loadavg 失败: %v", err)
		return LoadInfo{}
	}
	info, err := parseLoadavg(string(data))
	if err != nil {
		addWarning(warnings, "解析 /proc/loadavg 失败: %v", err)
		return LoadInfo{}
	}
	return LoadInfo{Load1: info.Load1, Load5: info.Load5, Load15: info.Load15,
		Running: info.Running, Total: info.Total, LastPID: info.LastPID}
}

// collectUptime 读取 /proc/uptime；失败只 warning。
func (c *Collector) collectUptime(warnings *[]string) UptimeInfo {
	data, err := os.ReadFile(filepath.Join(c.procRoot, "uptime"))
	if err != nil {
		addWarning(warnings, "读取 /proc/uptime 失败: %v", err)
		return UptimeInfo{}
	}
	info, err := parseUptime(string(data))
	if err != nil {
		addWarning(warnings, "解析 /proc/uptime 失败: %v", err)
		return UptimeInfo{}
	}
	return UptimeInfo{UptimeS: info.UptimeS, IdleS: info.IdleS}
}

// collectNetwork 读取 /proc/net/dev 并映射为对外结构。
func (c *Collector) collectNetwork(includeLoopback bool, warnings *[]string) []NetStat {
	data, err := os.ReadFile(filepath.Join(c.procRoot, "net/dev"))
	if err != nil {
		addWarning(warnings, "读取 /proc/net/dev 失败: %v", err)
		return []NetStat{}
	}
	stats, err := parseNetDev(string(data), includeLoopback)
	if err != nil {
		addWarning(warnings, "解析 /proc/net/dev 失败: %v", err)
		return []NetStat{}
	}
	result := make([]NetStat, 0, len(stats))
	for _, item := range stats {
		result = append(result, NetStat{
			Name: item.Name, RXBytes: item.RXBytes, TXBytes: item.TXBytes,
			RXPackets: item.RXPackets, TXPackets: item.TXPackets,
			RXErrors: item.RXErrors, TXErrors: item.TXErrors,
			RXHuman: humanBytes(item.RXBytes), TXHuman: humanBytes(item.TXBytes),
		})
	}
	return result
}
