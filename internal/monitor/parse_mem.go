package monitor

import (
	"strconv"
	"strings"
)

// memInfo 是 /proc/meminfo 中本插件需要的内存字段（单位：字节）。
type memInfo struct {
	Total, Available, Free, Buffers, Cached, SwapTotal, SwapFree int64
}

// parseMeminfo 解析 /proc/meminfo（原始单位 kB，统一换算为字节）。
// MemAvailable 缺失（老内核）时回退为 free + buffers + cached，不返回错误。
func parseMeminfo(data string) memInfo {
	values := make(map[string]int64, 16)
	for _, line := range strings.Split(data, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			continue
		}
		kib, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		values[key] = kib * 1024
	}
	info := memInfo{
		Total: values["MemTotal"], Available: values["MemAvailable"], Free: values["MemFree"],
		Buffers: values["Buffers"], Cached: values["Cached"] + values["SReclaimable"],
		SwapTotal: values["SwapTotal"], SwapFree: values["SwapFree"],
	}
	if info.Available == 0 && info.Total > 0 {
		info.Available = info.Free + info.Buffers + info.Cached
	}
	return info
}

// usedBytes 返回 used = total - available（与 free / top 的口径一致）。
func (m memInfo) usedBytes() int64 {
	used := m.Total - m.Available
	if used < 0 {
		return 0
	}
	return used
}

// usedPercent 返回已用内存百分比。
func (m memInfo) usedPercent() float64 {
	if m.Total <= 0 {
		return 0
	}
	return clampPercent(float64(m.usedBytes()) / float64(m.Total) * 100)
}
