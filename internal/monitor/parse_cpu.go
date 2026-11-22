package monitor

import (
	"fmt"
	"strconv"
	"strings"
)

// cpuTimes 是 /proc/stat 中一行 CPU 计数的前 8 个字段（单位 tick）。
type cpuTimes struct {
	user, nice, system, idle, iowait, irq, softirq, steal uint64
}

// total 返回参与使用率计算的总 tick 数（不含 guest，guest 已计入 user）。
func (t cpuTimes) total() uint64 {
	return t.user + t.nice + t.system + t.idle + t.iowait + t.irq + t.softirq + t.steal
}

// idleAll 返回空闲 tick（idle + iowait）。
func (t cpuTimes) idleAll() uint64 { return t.idle + t.iowait }

// cpuBreakdown 是一次 CPU 差分的使用率拆分，均为百分比。
type cpuBreakdown struct {
	Usage, User, System, IOWait, Idle float64
}

// parseCPUStat 解析 /proc/stat：返回总 CPU 行与按核 CPU 行（名称如 cpu0）。
func parseCPUStat(data string) (cpuTimes, map[string]cpuTimes, error) {
	var total cpuTimes
	found := false
	per := make(map[string]cpuTimes)
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "cpu") {
			continue
		}
		times, err := parseCPUTimes(fields[1:])
		if err != nil {
			return cpuTimes{}, nil, fmt.Errorf("%s 解析失败: %w", fields[0], err)
		}
		if fields[0] == "cpu" {
			total, found = times, true
			continue
		}
		per[fields[0]] = times
	}
	if !found {
		return cpuTimes{}, nil, fmt.Errorf("缺少总 cpu 行")
	}
	return total, per, nil
}

// parseCPUTimes 解析 user nice system idle iowait irq softirq steal 字段；缺省尾部按 0 处理。
func parseCPUTimes(fields []string) (cpuTimes, error) {
	if len(fields) < 5 {
		return cpuTimes{}, fmt.Errorf("字段数 %d 少于 5", len(fields))
	}
	values := make([]uint64, 8)
	for i := 0; i < 8 && i < len(fields); i++ {
		value, err := strconv.ParseUint(fields[i], 10, 64)
		if err != nil {
			return cpuTimes{}, fmt.Errorf("第 %d 个字段 %q 非法", i+1, fields[i])
		}
		values[i] = value
	}
	return cpuTimes{
		user: values[0], nice: values[1], system: values[2], idle: values[3],
		iowait: values[4], irq: values[5], softirq: values[6], steal: values[7],
	}, nil
}

// cpuDeltaUsage 用两次 /proc/stat 快照计算使用率：usage=(Δtotal-Δidle)/Δtotal。
func cpuDeltaUsage(prev, curr cpuTimes) cpuBreakdown {
	totalDelta := curr.total() - prev.total()
	if totalDelta == 0 || curr.total() < prev.total() {
		return cpuBreakdown{}
	}
	idleDelta := curr.idleAll() - prev.idleAll()
	busyDelta := totalDelta - idleDelta
	if busyDelta > totalDelta {
		busyDelta = totalDelta
	}
	percent := func(delta uint64) float64 {
		return clampPercent(float64(delta) / float64(totalDelta) * 100)
	}
	return cpuBreakdown{
		Usage:  percent(busyDelta),
		User:   percent(curr.user - prev.user),
		System: percent((curr.system + curr.irq + curr.softirq) - (prev.system + prev.irq + prev.softirq)),
		IOWait: percent(curr.iowait - prev.iowait),
		Idle:   percent(idleDelta),
	}
}
