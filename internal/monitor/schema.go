package monitor

// 对外结果结构：字段全部显式列出，不使用 object / any / map[string]any（数据形状纪律）。
// 大数值用 int64 字节/秒，人类可读串另给 *_human 字段。

// SystemResult 是 monitor.system 的一屏概览结果。
// alerts 只在本返回体里标注（通知归主壳告警中心）；history 是可直接绘图的列式曲线。
type SystemResult struct {
	Schema    string        `json:"schema"`
	Source    string        `json:"source"`
	SampledAt string        `json:"sampled_at"`
	CPU       CPUInfo       `json:"cpu"`
	Memory    MemoryInfo    `json:"memory"`
	Load      LoadInfo      `json:"load"`
	Uptime    UptimeInfo    `json:"uptime"`
	Disks     []DiskUsage   `json:"disks"`
	Network   []NetStat     `json:"network"`
	Alerts    []Alert       `json:"alerts"`
	History   HistoryResult `json:"history"`
	Warnings  []string      `json:"warnings"`
}

// CPUInfo 是 CPU 总使用率与可选分核使用率。
type CPUInfo struct {
	UsagePercent     float64  `json:"usage_percent"`
	UserPercent      float64  `json:"user_percent"`
	SystemPercent    float64  `json:"system_percent"`
	IOWaitPercent    float64  `json:"iowait_percent"`
	IdlePercent      float64  `json:"idle_percent"`
	Cores            int      `json:"cores"`
	SampleIntervalMS int64    `json:"sample_interval_ms"`
	PerCPU           []PerCPU `json:"per_cpu,omitempty"`
}

// PerCPU 是单个逻辑核的使用率。
type PerCPU struct {
	Name         string  `json:"name"`
	UsagePercent float64 `json:"usage_percent"`
}

// MemoryInfo 是内存与 swap 用量（字节 + 人类可读）。
type MemoryInfo struct {
	Total          int64   `json:"total"`
	TotalHuman     string  `json:"total_human"`
	Available      int64   `json:"available"`
	AvailableHuman string  `json:"available_human"`
	Used           int64   `json:"used"`
	UsedHuman      string  `json:"used_human"`
	Free           int64   `json:"free"`
	FreeHuman      string  `json:"free_human"`
	Buffers        int64   `json:"buffers"`
	BuffersHuman   string  `json:"buffers_human"`
	Cached         int64   `json:"cached"`
	CachedHuman    string  `json:"cached_human"`
	SwapTotal      int64   `json:"swap_total"`
	SwapTotalHuman string  `json:"swap_total_human"`
	SwapUsed       int64   `json:"swap_used"`
	SwapUsedHuman  string  `json:"swap_used_human"`
	UsedPercent    float64 `json:"used_percent"`
}

// LoadInfo 是 /proc/loadavg 的负载与可运行进程数。
type LoadInfo struct {
	Load1   float64 `json:"load1"`
	Load5   float64 `json:"load5"`
	Load15  float64 `json:"load15"`
	Running int     `json:"running"`
	Total   int     `json:"total"`
	LastPID int     `json:"last_pid"`
}

// UptimeInfo 是 /proc/uptime 的开机时长与累计空闲秒数。
type UptimeInfo struct {
	UptimeS int64 `json:"uptime_s"`
	IdleS   int64 `json:"idle_s"`
}

// DiskUsage 是单个真实块设备挂载点的用量。
type DiskUsage struct {
	Mount       string  `json:"mount"`
	Device      string  `json:"device"`
	FSType      string  `json:"fs_type"`
	Total       int64   `json:"total"`
	TotalHuman  string  `json:"total_human"`
	Used        int64   `json:"used"`
	UsedHuman   string  `json:"used_human"`
	Avail       int64   `json:"avail"`
	AvailHuman  string  `json:"avail_human"`
	UsedPercent float64 `json:"used_percent"`
}

// NetStat 是单个网卡的累计收发统计。
type NetStat struct {
	Name      string `json:"name"`
	RXBytes   int64  `json:"rx_bytes"`
	TXBytes   int64  `json:"tx_bytes"`
	RXPackets int64  `json:"rx_packets"`
	TXPackets int64  `json:"tx_packets"`
	RXErrors  int64  `json:"rx_errors"`
	TXErrors  int64  `json:"tx_errors"`
	RXHuman   string `json:"rx_human"`
	TXHuman   string `json:"tx_human"`
}

// ProcessesResult 是 monitor.processes 的 top N 结果。
type ProcessesResult struct {
	Schema           string        `json:"schema"`
	SampledAt        string        `json:"sampled_at"`
	SampleIntervalMS int64         `json:"sample_interval_ms"`
	Sort             string        `json:"sort"`
	Limit            int           `json:"limit"`
	Count            int           `json:"count"`
	Total            int           `json:"total"`
	Items            []ProcessInfo `json:"items"`
	Warnings         []string      `json:"warnings"`
}

// ProcessInfo 是单个进程的 CPU / 内存 / 线程快照。cpu_percent 按单核归一，多核可超过 100。
// name 是 comm 的可读别名（二者相同），mem_percent 是 RSS / 物理内存总量。
type ProcessInfo struct {
	PID         int     `json:"pid"`
	Comm        string  `json:"comm"`
	Name        string  `json:"name"`
	State       string  `json:"state"`
	CPUPercent  float64 `json:"cpu_percent"`
	MemRSSBytes int64   `json:"mem_rss_bytes"`
	MemRSSHuman string  `json:"mem_rss_human"`
	MemPercent  float64 `json:"mem_percent"`
	Threads     int     `json:"threads"`
}

// DisksResult 是 monitor.disks 的磁盘详表（含 inode 与读写统计）。
type DisksResult struct {
	Schema    string       `json:"schema"`
	SampledAt string       `json:"sampled_at"`
	Path      string       `json:"path"`
	Count     int          `json:"count"`
	Items     []DiskDetail `json:"items"`
	Warnings  []string     `json:"warnings"`
}

// DiskDetail 是挂载点粒度用量 + inode + 块设备读写累计值。
type DiskDetail struct {
	Mount             string  `json:"mount"`
	Device            string  `json:"device"`
	FSType            string  `json:"fs_type"`
	Total             int64   `json:"total"`
	TotalHuman        string  `json:"total_human"`
	Used              int64   `json:"used"`
	UsedHuman         string  `json:"used_human"`
	Avail             int64   `json:"avail"`
	AvailHuman        string  `json:"avail_human"`
	UsedPercent       float64 `json:"used_percent"`
	InodesTotal       int64   `json:"inodes_total"`
	InodesUsed        int64   `json:"inodes_used"`
	InodesFree        int64   `json:"inodes_free"`
	InodesUsedPercent float64 `json:"inodes_used_percent"`
	ReadBytes         int64   `json:"read_bytes"`
	ReadBytesHuman    string  `json:"read_bytes_human"`
	WriteBytes        int64   `json:"write_bytes"`
	WriteBytesHuman   string  `json:"write_bytes_human"`
	Reads             int64   `json:"reads"`
	Writes            int64   `json:"writes"`
}

// TemperatureResult 是 thermal zone 列表；无 zone 时 available=false 且 zones 为空数组。
type TemperatureResult struct {
	Schema    string        `json:"schema"`
	SampledAt string        `json:"sampled_at"`
	Available bool          `json:"available"`
	Count     int           `json:"count"`
	Zones     []ThermalZone `json:"zones"`
	Warnings  []string      `json:"warnings"`
}

// ThermalZone 是单个 /sys/class/thermal/thermal_zoneN 的读数。
type ThermalZone struct {
	Zone  string  `json:"zone"`
	TempC float64 `json:"temp_c"`
	Type  string  `json:"type"`
}
