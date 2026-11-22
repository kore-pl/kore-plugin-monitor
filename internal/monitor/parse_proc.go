package monitor

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// procStat 是 /proc/<pid>/stat 中本插件需要的字段。
type procStat struct {
	PID     int
	Comm    string
	State   byte
	UTicks  uint64
	STicks  uint64
	Threads int
}

// parseProcStat 解析 /proc/<pid>/stat：comm 可能含空格与括号，按首个 '(' 与末个 ')' 切分。
func parseProcStat(data string, pid int) (procStat, error) {
	open := strings.IndexByte(data, '(')
	closeAt := strings.LastIndexByte(data, ')')
	if open < 0 || closeAt <= open {
		return procStat{}, fmt.Errorf("stat 格式非法：缺少 comm 字段")
	}
	info := procStat{PID: pid, Comm: data[open+1 : closeAt]}
	fields := strings.Fields(data[closeAt+1:])
	// fields[0]=state(第3字段)；utime/stime/num_threads 是第14/15/20字段 → 索引11/12/17。
	if len(fields) < 18 {
		return procStat{}, fmt.Errorf("stat 字段数 %d 少于 18", len(fields))
	}
	if len(fields[0]) != 1 {
		return procStat{}, fmt.Errorf("stat state 字段非法: %q", fields[0])
	}
	info.State = fields[0][0]
	var err error
	if info.UTicks, err = parseUintField(fields[11], "utime"); err != nil {
		return procStat{}, err
	}
	if info.STicks, err = parseUintField(fields[12], "stime"); err != nil {
		return procStat{}, err
	}
	threads, err := strconv.Atoi(fields[17])
	if err != nil {
		return procStat{}, fmt.Errorf("stat num_threads 解析失败: %w", err)
	}
	info.Threads = threads
	return info, nil
}

// parseUintField 解析无符号十进制字段，错误信息带字段名便于定位。
func parseUintField(raw, name string) (uint64, error) {
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("stat %s 解析失败: %w", name, err)
	}
	return value, nil
}

// procStatus 是 /proc/<pid>/status 中本插件需要的字段（VmRSS 换算为字节）。
type procStatus struct {
	RSSBytes int64
	Threads  int
}

// parseProcStatus 解析 VmRSS 与 Threads；缺失字段按 0 处理，不报错（内核线程没有 VmRSS）。
func parseProcStatus(data string) procStatus {
	info := procStatus{}
	for _, line := range strings.Split(data, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			continue
		}
		switch key {
		case "VmRSS":
			if kib, err := strconv.ParseInt(fields[0], 10, 64); err == nil {
				info.RSSBytes = kib * 1024
			}
		case "Threads":
			if threads, err := strconv.Atoi(fields[0]); err == nil {
				info.Threads = threads
			}
		}
	}
	return info
}

// procCPUPercent 用两次采样间累计 tick 差计算单进程 CPU 百分比（单核归一，多核可 >100）。
func procCPUPercent(prevTicks, currTicks uint64, elapsed time.Duration) float64 {
	if elapsed <= 0 || currTicks < prevTicks {
		return 0
	}
	deltaSeconds := float64(currTicks-prevTicks) / float64(CLKTCK)
	return round2(deltaSeconds / elapsed.Seconds() * 100)
}

// normalizeCPU 把负值归零；进程瞬时可出现极小负差。
func normalizeCPU(v float64) float64 {
	if v < 0 || math.IsNaN(v) {
		return 0
	}
	return round2(v)
}
