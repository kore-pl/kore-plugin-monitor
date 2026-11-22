package monitor

import (
	"fmt"
	"strconv"
	"strings"
)

// loadAvg 是 /proc/loadavg 的解析结果。
type loadAvg struct {
	Load1, Load5, Load15 float64
	Running, Total       int
	LastPID              int
}

// parseLoadavg 解析 "0.52 0.58 0.59 2/1234 56789"。
func parseLoadavg(data string) (loadAvg, error) {
	fields := strings.Fields(strings.TrimSpace(data))
	if len(fields) < 5 {
		return loadAvg{}, fmt.Errorf("loadavg 字段数 %d 少于 5", len(fields))
	}
	parseFloat := func(raw string) (float64, error) {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return 0, fmt.Errorf("loadavg 数值 %q 非法: %w", raw, err)
		}
		return value, nil
	}
	info := loadAvg{}
	var err error
	if info.Load1, err = parseFloat(fields[0]); err != nil {
		return info, err
	}
	if info.Load5, err = parseFloat(fields[1]); err != nil {
		return info, err
	}
	if info.Load15, err = parseFloat(fields[2]); err != nil {
		return info, err
	}
	running, total, ok := strings.Cut(fields[3], "/")
	if !ok {
		return info, fmt.Errorf("loadavg running/total 字段 %q 非法", fields[3])
	}
	if info.Running, err = strconv.Atoi(running); err != nil {
		return info, fmt.Errorf("loadavg running 字段非法: %w", err)
	}
	if info.Total, err = strconv.Atoi(total); err != nil {
		return info, fmt.Errorf("loadavg total 字段非法: %w", err)
	}
	if info.LastPID, err = strconv.Atoi(fields[4]); err != nil {
		return info, fmt.Errorf("loadavg last_pid 字段非法: %w", err)
	}
	return info, nil
}

// uptimeInfo 是 /proc/uptime 的解析结果（秒）。
type uptimeInfo struct {
	UptimeS, IdleS int64
}

// parseUptime 解析 "12345.67 98765.43"，截断为整秒。
func parseUptime(data string) (uptimeInfo, error) {
	fields := strings.Fields(strings.TrimSpace(data))
	if len(fields) < 2 {
		return uptimeInfo{}, fmt.Errorf("uptime 字段数 %d 少于 2", len(fields))
	}
	parse := func(raw string) (int64, error) {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return 0, fmt.Errorf("uptime 数值 %q 非法: %w", raw, err)
		}
		return int64(value), nil
	}
	up, err := parse(fields[0])
	if err != nil {
		return uptimeInfo{}, err
	}
	idle, err := parse(fields[1])
	if err != nil {
		return uptimeInfo{}, err
	}
	return uptimeInfo{UptimeS: up, IdleS: idle}, nil
}

// parseMilliCelsius 解析 thermal_zone 的毫摄氏度（"45000" → 45.0）。
func parseMilliCelsius(data string) (float64, error) {
	raw := strings.TrimSpace(data)
	milli, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("thermal 温度 %q 非法: %w", raw, err)
	}
	return round2(float64(milli) / 1000), nil
}
